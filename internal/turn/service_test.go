package turn

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/r3n3r0/agent-bridge/internal/config"
	"github.com/r3n3r0/agent-bridge/internal/prompt"
	"github.com/r3n3r0/agent-bridge/internal/runner"
	"github.com/r3n3r0/agent-bridge/internal/session"
	"github.com/r3n3r0/agent-bridge/internal/stats"
)

// fakeExecutor implements runner.Executor without starting processes.
type fakeExecutor struct {
	mu    sync.Mutex
	calls []runner.Request
	run   func(call int, req runner.Request) (*runner.Outcome, error)
}

func (f *fakeExecutor) Run(ctx context.Context, req runner.Request) (*runner.Outcome, error) {
	f.mu.Lock()
	f.calls = append(f.calls, req)
	n := len(f.calls)
	f.mu.Unlock()
	return f.run(n, req)
}

// fakeRecorder implements stats.Recorder.
type fakeRecorder struct {
	mu    sync.Mutex
	turns []stats.Turn
}

func (f *fakeRecorder) Record(t stats.Turn) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.turns = append(f.turns, t)
	return nil
}

func (f *fakeRecorder) last() stats.Turn {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.turns[len(f.turns)-1]
}

func success(sessionID, text string) *runner.Outcome {
	return &runner.Outcome{
		SessionID: sessionID,
		Model:     "model-x",
		Result: &runner.ResultEvent{
			Subtype: runner.SubtypeSuccess, Result: text, SessionID: sessionID, CostUSD: 0.5, NumTurns: 3, DurationMs: 7,
			Usage: runner.Usage{InputTokens: 1, OutputTokens: 2, CacheReadTokens: 3, CacheCreationTokens: 4},
		},
	}
}

type fixture struct {
	svc   Service
	exec  *fakeExecutor
	rec   *fakeRecorder
	store session.Store
	lim   *runner.FIFOLimiter
	logs  *bytes.Buffer
	stop  context.CancelFunc
}

func newFixture(t *testing.T, onBusy string, run func(int, runner.Request) (*runner.Outcome, error)) *fixture {
	t.Helper()
	f := &fixture{
		exec:  &fakeExecutor{run: run},
		rec:   &fakeRecorder{},
		store: session.NewMemoryStore(session.Policy{}),
		lim:   runner.NewFIFOLimiter(1, 0, 0),
		logs:  &bytes.Buffer{},
	}
	hard, stop := context.WithCancel(context.Background())
	t.Cleanup(stop)
	f.stop = stop
	f.svc = NewService(Deps{
		Store:    f.store,
		Locker:   session.NewKeyLocker(),
		Executor: f.exec,
		Limiter:  f.lim,
		Recorder: f.rec,
		Logger:   slog.New(slog.NewTextHandler(f.logs, &slog.HandlerOptions{Level: slog.LevelDebug})),
	}, Settings{Binary: "cli", AuthMode: config.AuthSubscription, OnBusy: onBusy, HardStop: hard})
	return f
}

var agent = &config.Agent{Name: "coder", Model: "sonnet", ToolsEnabled: true, Timeout: time.Minute}

func request(msgs ...prompt.Message) *Request {
	return &Request{RequestID: "r1", Agent: agent, Spec: runner.Spec{Model: "sonnet", ToolsEnabled: true}, Cwd: "/w", Messages: msgs, SessionKey: "k"}
}

func user(text string) prompt.Message { return prompt.Message{Role: prompt.RoleUser, Text: text} }
func assistant(text string) prompt.Message {
	return prompt.Message{Role: prompt.RoleAssistant, Text: text}
}

func kindOf(t *testing.T, err error) Kind {
	t.Helper()
	var e *Error
	if !errors.As(err, &e) {
		t.Fatalf("error %v is not *Error", err)
	}
	return e.Kind
}

func TestExecuteSuccessAndResume(t *testing.T) {
	f := newFixture(t, config.OnBusyWait, func(n int, req runner.Request) (*runner.Outcome, error) {
		req.OnEvent(runner.Event{Kind: runner.KindText, Text: "working"})
		return success("sid-"+string(rune('0'+n)), "done"), nil
	})
	var progress []string
	res, err := f.svc.Execute(context.Background(), request(user("hi")), func(s string) { progress = append(progress, s) })
	if err != nil {
		t.Fatal(err)
	}
	if res.Content != "done" || res.FinishReason != "stop" || res.PromptTokens != 8 || res.CompletionTokens != 2 ||
		res.Turns != 3 || res.CostUSD != 0.5 || res.DurationMs != 7 {
		t.Fatalf("result %+v", res)
	}
	if len(progress) != 1 || progress[0] != "working" {
		t.Fatalf("progress %v", progress)
	}
	if f.exec.calls[0].Stdin != "hi" || f.exec.calls[0].Dir != "/w" || f.exec.calls[0].Binary != "cli" {
		t.Fatalf("request %+v", f.exec.calls[0])
	}
	if r := f.rec.last(); r.Outcome != stats.OutcomeSuccess || r.CLISessionID != "sid-1" || r.Resumed {
		t.Fatalf("record %+v", r)
	}

	_, err = f.svc.Execute(context.Background(), request(user("hi"), assistant("done"), user("next")), nil)
	if err != nil {
		t.Fatal(err)
	}
	second := f.exec.calls[1]
	if second.Stdin != "next" || !strings.Contains(strings.Join(second.Args, " "), "--resume sid-1") {
		t.Fatalf("second call %+v", second)
	}
	e, _ := f.store.Get("coder|k")
	if e.CLISessionID != "sid-2" || e.Turns != 2 {
		t.Fatalf("entry %+v", e)
	}
	if !f.rec.last().Resumed {
		t.Fatal("not marked resumed")
	}
}

func TestExecuteResumeMissingRetries(t *testing.T) {
	f := newFixture(t, config.OnBusyWait, func(n int, req runner.Request) (*runner.Outcome, error) {
		if strings.Contains(strings.Join(req.Args, " "), "--resume") {
			return &runner.Outcome{Stderr: "No conversation found with session ID: old"}, runner.ErrNoResult
		}
		return success("fresh", "ok"), nil
	})
	_ = f.store.Put("coder|k", session.Entry{CLISessionID: "old", Cwd: "/w", LastUsedAt: time.Now()})
	_, err := f.svc.Execute(context.Background(), request(user("a"), assistant("b"), user("c")), nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(f.exec.calls) != 2 || !strings.Contains(f.exec.calls[1].Stdin, "<conversation_history>") {
		t.Fatalf("calls %+v", f.exec.calls)
	}
	if e, _ := f.store.Get("coder|k"); e.CLISessionID != "fresh" {
		t.Fatalf("entry %+v", e)
	}
}

func TestExecuteOutcomes(t *testing.T) {
	cases := []struct {
		name    string
		out     *runner.Outcome
		err     error
		kind    Kind
		outcome string
	}{
		{"max turns", &runner.Outcome{Result: &runner.ResultEvent{Subtype: runner.SubtypeMaxTurns, IsError: true, Result: "partial"}}, nil, 0, stats.OutcomeMaxTurns},
		{"error result", &runner.Outcome{Result: &runner.ResultEvent{Subtype: "error_during_execution", IsError: true, Result: "boom"}}, nil, KindUpstream, stats.OutcomeError},
		{"timeout", &runner.Outcome{}, runner.ErrTimeout, KindTimeout, stats.OutcomeTimeout},
		{"canceled", &runner.Outcome{}, runner.ErrCanceled, KindCanceled, stats.OutcomeCanceled},
		{"no result", &runner.Outcome{Stderr: "fatal sk-ant-secret"}, runner.ErrNoResult, KindUpstream, stats.OutcomeError},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := newFixture(t, config.OnBusyWait, func(int, runner.Request) (*runner.Outcome, error) { return c.out, c.err })
			res, err := f.svc.Execute(context.Background(), request(user("x")), nil)
			if c.kind == 0 {
				if err != nil || res.FinishReason != "length" || res.Content != "partial" {
					t.Fatalf("res %+v err %v", res, err)
				}
			} else if got := kindOf(t, err); got != c.kind {
				t.Fatalf("kind %d, want %d", got, c.kind)
			}
			if err != nil && strings.Contains(err.Error(), "sk-ant-") {
				t.Fatalf("secret in error: %v", err)
			}
			if f.rec.last().Outcome != c.outcome {
				t.Fatalf("outcome %s", f.rec.last().Outcome)
			}
		})
	}
}

func TestExecuteShutdown(t *testing.T) {
	var f *fixture
	f = newFixture(t, config.OnBusyWait, func(int, runner.Request) (*runner.Outcome, error) {
		f.stop()
		return &runner.Outcome{}, runner.ErrCanceled
	})
	_, err := f.svc.Execute(context.Background(), request(user("x")), nil)
	if kindOf(t, err) != KindShutdown {
		t.Fatalf("got %v", err)
	}
}

func TestExecuteInvalidPrompt(t *testing.T) {
	f := newFixture(t, config.OnBusyWait, nil)
	_, err := f.svc.Execute(context.Background(), request(user(strings.Repeat("x", prompt.MaxBytes+1))), nil)
	if kindOf(t, err) != KindInvalid || len(f.exec.calls) != 0 {
		t.Fatalf("got %v", err)
	}
}

func TestAdmit(t *testing.T) {
	block := make(chan struct{})
	f := newFixture(t, config.OnBusyReject, func(int, runner.Request) (*runner.Outcome, error) {
		<-block
		return success("s", "ok"), nil
	})
	first := request(user("x"))
	if err := f.svc.Admit(first); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		_, err := f.svc.Execute(context.Background(), first, nil)
		done <- err
	}()
	// Same session: busy.
	if kindOf(t, f.svc.Admit(request(user("y")))) != KindSessionBusy {
		t.Fatal("busy session admitted")
	}
	// Other session, single slot taken and no queue: full, and its lock is released.
	for f.lim.Active() == 0 {
		time.Sleep(time.Millisecond)
	}
	other := request(user("z"))
	other.SessionKey = "other"
	if kindOf(t, f.svc.Admit(other)) != KindQueueFull {
		t.Fatal("full queue admitted")
	}
	close(block)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if err := f.svc.Admit(other); err != nil {
		t.Fatalf("lock of rejected request not released: %v", err)
	}
	if _, err := f.svc.Execute(context.Background(), other, nil); err != nil {
		t.Fatal(err)
	}
}

func TestAPIKeySourceWarning(t *testing.T) {
	f := newFixture(t, config.OnBusyWait, func(int, runner.Request) (*runner.Outcome, error) {
		o := success("s", "ok")
		o.APIKeySource = "ANTHROPIC_API_KEY"
		return o, nil
	})
	if _, err := f.svc.Execute(context.Background(), request(user("x")), nil); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(f.logs.String(), "level=ERROR") {
		t.Fatal("no error logged")
	}
}

func TestStructuredOutputAndMaxTokens(t *testing.T) {
	outcomes := []*runner.Outcome{
		{Result: &runner.ResultEvent{Subtype: runner.SubtypeSuccess, Result: "ignored text", StructuredOutput: json.RawMessage(`{ "a" : 1 }`)}},
		{Result: &runner.ResultEvent{Subtype: runner.SubtypeSuccess, Result: "no json"}},
		{Result: &runner.ResultEvent{Subtype: runner.SubtypeStructuredOutputFail, IsError: true}},
	}
	f := newFixture(t, config.OnBusyWait, func(n int, req runner.Request) (*runner.Outcome, error) {
		return outcomes[n-1], nil
	})
	req := request(user("x"))
	req.Spec.JSONSchema = `{"type":"object"}`
	req.MaxOutputTokens = 256
	res, err := f.svc.Execute(context.Background(), req, nil)
	if err != nil || res.Content != `{"a":1}` {
		t.Fatalf("structured %+v %v", res, err)
	}
	call := f.exec.calls[0]
	if argAfter(call.Args, "--json-schema") != `{"type":"object"}` || !strings.Contains(strings.Join(call.Env, " "), "CLAUDE_CODE_MAX_OUTPUT_TOKENS=256") {
		t.Fatalf("call %q %q", call.Args, call.Env)
	}
	for _, want := range []string{"without structured output", "matching the JSON schema"} {
		r := request(user("x"))
		r.Spec.JSONSchema = `{"type":"object"}`
		r.SessionKey = want
		_, err := f.svc.Execute(context.Background(), r, nil)
		if kindOf(t, err) != KindUpstream || !strings.Contains(err.Error(), want) {
			t.Fatalf("got %v", err)
		}
	}
}
