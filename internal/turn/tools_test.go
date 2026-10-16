package turn

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/r3n3r0/agent-bridge/internal/config"
	"github.com/r3n3r0/agent-bridge/internal/prompt"
	"github.com/r3n3r0/agent-bridge/internal/runner"
	"github.com/r3n3r0/agent-bridge/internal/session"
	"github.com/r3n3r0/agent-bridge/internal/tools"
)

// callTool behaves like the CLI calling a client function through the MCP
// server described in req: it reads the generated files and forwards the
// call to the broker.
func callTool(ctx context.Context, t *testing.T, req runner.Request, name, args string) (tools.Result, error) {
	t.Helper()
	cfgPath := ""
	for i := 0; i+1 < len(req.Args); i++ {
		if req.Args[i] == "--mcp-config" {
			cfgPath = req.Args[i+1]
		}
	}
	if cfgPath == "" {
		return tools.Result{}, errors.New("no client functions configured")
	}
	data, err := os.ReadFile(cfgPath)
	if err != nil {
		return tools.Result{}, err
	}
	var cfg struct {
		MCPServers map[string]struct {
			Command string   `json:"command"`
			Args    []string `json:"args"`
		} `json:"mcpServers"`
	}
	if err := json.Unmarshal(data, &cfg); err != nil {
		return tools.Result{}, err
	}
	srv := cfg.MCPServers[tools.ServerName]
	if srv.Command != "helper" || srv.Args[0] != "mcp-tools" || srv.Args[1] != "-spec" {
		t.Errorf("unexpected MCP server %+v", srv)
	}
	spec, err := tools.ReadSpec(srv.Args[2])
	if err != nil {
		return tools.Result{}, err
	}
	return tools.Forward(ctx, http.DefaultClient, spec, name, json.RawMessage(args))
}

type toolFixture struct {
	*fixture
	broker *tools.HTTPBroker
}

func newToolFixture(t *testing.T, wait time.Duration, run func(int, runner.Request) (*runner.Outcome, error)) *toolFixture {
	t.Helper()
	broker, err := tools.NewHTTPBroker()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { broker.Close() })
	f := newFixture(t, config.OnBusyWait, run)
	f.lim = runner.NewFIFOLimiter(2, 2, 0)
	hard, stop := context.WithCancel(context.Background())
	t.Cleanup(stop)
	f.svc = NewService(Deps{
		Store:    f.store,
		Locker:   session.NewKeyLocker(),
		Executor: f.exec,
		Limiter:  f.lim,
		Recorder: f.rec,
		Broker:   broker,
	}, Settings{
		Binary: "cli", AuthMode: config.AuthAPI, HardStop: hard,
		ToolHelper: []string{"helper", "mcp-tools"}, ToolWaitTimeout: wait, ToolBatchWindow: 50 * time.Millisecond,
	})
	return &toolFixture{fixture: f, broker: broker}
}

var weather = tools.Definition{Name: "get_weather", Description: "Weather by city",
	Parameters: json.RawMessage(`{"type":"object","properties":{"city":{"type":"string"}}}`)}

func toolRequest(msgs ...prompt.Message) *Request {
	r := request(msgs...)
	r.Tools = []tools.Definition{weather}
	r.ParallelToolCalls = true
	return r
}

func withResults(calls []tools.Call, results ...string) []prompt.Message {
	msgs := []prompt.Message{user("weather?")}
	a := prompt.Message{Role: prompt.RoleAssistant}
	for _, c := range calls {
		a.ToolCalls = append(a.ToolCalls, prompt.ToolCall{ID: c.ID, Name: c.Name, Arguments: c.Arguments})
	}
	msgs = append(msgs, a)
	for i, c := range calls {
		msgs = append(msgs, prompt.Message{Role: prompt.RoleTool, ToolCallID: c.ID, Name: c.Name, Text: results[i]})
	}
	return msgs
}

func TestToolCallRoundTrip(t *testing.T) {
	f := newToolFixture(t, time.Minute, func(n int, req runner.Request) (*runner.Outcome, error) {
		req.OnEvent(runner.Event{Kind: runner.KindText, Text: "Let me check."})
		req.OnEvent(runner.Event{Kind: runner.KindUsage, Usage: &runner.Usage{InputTokens: 10, OutputTokens: 4}})
		res, err := callTool(context.Background(), t, req, "get_weather", `{"city":"Rome"}`)
		if err != nil {
			return &runner.Outcome{Stderr: err.Error()}, runner.ErrNoResult
		}
		return success("s1", "Rome: "+res.Content), nil
	})
	res, err := f.svc.Execute(context.Background(), toolRequest(user("weather?")), nil)
	if err != nil {
		t.Fatal(err)
	}
	if res.FinishReason != FinishToolCalls || len(res.ToolCalls) != 1 || res.Content != "Let me check." ||
		res.PromptTokens != 10 || res.CompletionTokens != 4 {
		t.Fatalf("first stop %+v", res)
	}
	call := res.ToolCalls[0]
	if !strings.HasPrefix(call.ID, "call_") || call.Name != "get_weather" || call.Arguments != `{"city":"Rome"}` {
		t.Fatalf("call %+v", call)
	}
	args := strings.Join(f.exec.calls[0].Args, " ")
	if !strings.Contains(args, "--allowedTools mcp__functions") || !strings.Contains(args, "--mcp-config") {
		t.Fatalf("args %s", args)
	}
	if !strings.Contains(strings.Join(f.exec.calls[0].Env, " "), "MCP_TOOL_TIMEOUT=60000") {
		t.Fatal("tool timeout not set")
	}
	if f.lim.Active() != 1 {
		t.Fatal("waiting turn must keep its slot")
	}

	// The follow-up request carries only the results, no session header.
	next := toolRequest(withResults(res.ToolCalls, "sunny, 24C")...)
	next.SessionKey = "another-generated-key"
	if err := f.svc.Admit(next); err != nil {
		t.Fatal(err)
	}
	res, err = f.svc.Execute(context.Background(), next, nil)
	if err != nil {
		t.Fatal(err)
	}
	if res.FinishReason != FinishStop || res.Content != "Rome: sunny, 24C" {
		t.Fatalf("final %+v", res)
	}
	if len(f.exec.calls) != 1 {
		t.Fatalf("the same process must continue, got %d runs", len(f.exec.calls))
	}
	waitUntil(t, func() bool { return f.lim.Active() == 0 })
	cfg := f.exec.calls[0].Args[len(f.exec.calls[0].Args)-1]
	if _, err := os.Stat(cfg); !os.IsNotExist(err) {
		t.Fatalf("temporary MCP configuration %s left behind", cfg)
	}
}

func TestParallelToolCalls(t *testing.T) {
	f := newToolFixture(t, time.Minute, func(n int, req runner.Request) (*runner.Outcome, error) {
		var wg sync.WaitGroup
		out := make([]string, 2)
		for i, city := range []string{"Rome", "Paris"} {
			wg.Add(1)
			go func(i int, city string) {
				defer wg.Done()
				r, err := callTool(context.Background(), t, req, "get_weather", `{"city":"`+city+`"}`)
				if err == nil {
					out[i] = r.Content
				}
			}(i, city)
		}
		wg.Wait()
		return success("s", strings.Join(out, ",")), nil
	})
	res, err := f.svc.Execute(context.Background(), toolRequest(user("weather?")), nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.ToolCalls) != 2 {
		t.Fatalf("calls %+v", res.ToolCalls)
	}
	results := map[string]string{`{"city":"Rome"}`: "R", `{"city":"Paris"}`: "P"}
	ordered := []string{results[res.ToolCalls[0].Arguments], results[res.ToolCalls[1].Arguments]}
	res, err = f.svc.Execute(context.Background(), toolRequest(withResults(res.ToolCalls, ordered...)...), nil)
	if err != nil || res.Content != "R,P" {
		t.Fatalf("final %+v %v", res, err)
	}
}

func TestSequentialToolCallsAndMissingResult(t *testing.T) {
	f := newToolFixture(t, time.Minute, func(n int, req runner.Request) (*runner.Outcome, error) {
		var wg sync.WaitGroup
		out := make([]string, 2)
		for i, city := range []string{"Rome", "Paris"} {
			wg.Add(1)
			go func(i int, city string) {
				defer wg.Done()
				r, _ := callTool(context.Background(), t, req, "get_weather", `{"city":"`+city+`"}`)
				out[i] = r.Content
				if r.IsError {
					out[i] = "error"
				}
			}(i, city)
		}
		wg.Wait()
		return success("s", strings.Join(out, ",")), nil
	})
	req := toolRequest(user("weather?"))
	req.ParallelToolCalls = false
	res, err := f.svc.Execute(context.Background(), req, nil)
	if err != nil || len(res.ToolCalls) != 1 {
		t.Fatalf("first batch %+v %v", res, err)
	}
	first := res.ToolCalls[0]

	// With parallel_tool_calls false the second call comes in its own batch.
	res, err = f.svc.Execute(context.Background(), toolRequest(withResults([]tools.Call{first}, "ok-1")...), nil)
	if err != nil || len(res.ToolCalls) != 1 || res.ToolCalls[0].ID == first.ID {
		t.Fatalf("second batch %+v %v", res, err)
	}
	second := res.ToolCalls[0]

	msgs := withResults([]tools.Call{second}, "ok-2")
	res, err = f.svc.Execute(context.Background(), toolRequest(msgs...), nil)
	if err != nil || res.FinishReason != FinishStop || len(f.exec.calls) != 1 {
		t.Fatalf("final %+v %v", res, err)
	}
	if res.Content != "ok-1,ok-2" && res.Content != "ok-2,ok-1" {
		t.Fatalf("content %q", res.Content)
	}

	// A result for a call nobody waits for does not resume anything: a
	// new process starts from the history.
	f.exec.run = func(n int, req runner.Request) (*runner.Outcome, error) { return success("s", "fresh"), nil }
	res, err = f.svc.Execute(context.Background(), toolRequest(withResults([]tools.Call{{ID: "call_gone", Name: "get_weather"}}, "late")...), nil)
	if err != nil || res.Content != "fresh" || len(f.exec.calls) != 2 {
		t.Fatalf("unknown id %+v %v", res, err)
	}
}

func TestMissingResultIsError(t *testing.T) {
	f := newToolFixture(t, time.Minute, func(n int, req runner.Request) (*runner.Outcome, error) {
		r, _ := callTool(context.Background(), t, req, "get_weather", `{}`)
		if !r.IsError {
			return success("s", "no error"), nil
		}
		return success("s", "error: "+r.Content), nil
	})
	res, err := f.svc.Execute(context.Background(), toolRequest(user("x")), nil)
	if err != nil {
		t.Fatal(err)
	}
	// A tool message with a different id does not match the waiting turn.
	msgs := withResults(res.ToolCalls, "unused")
	msgs[len(msgs)-1].ToolCallID = "call_wrong"
	req := toolRequest(msgs...)
	if lt := f.svc.(*service).waitingTurn(req); lt != nil {
		t.Fatal("a wrong id must not match")
	}
	// Resuming with a request that lacks the result of the waiting call
	// gives the model an error result for it.
	lt := f.svc.(*service).waitingTurn(toolRequest(withResults(res.ToolCalls, "x")...))
	if lt == nil {
		t.Fatal("waiting turn not found")
	}
	got, e := lt.resume(context.Background(), req, nil)
	if e != nil || !strings.HasPrefix(got.Content, "error: The caller did not return") {
		t.Fatalf("got %+v %v", got, e)
	}
}

func TestToolWaitTimeoutThenFallback(t *testing.T) {
	runs := 0
	var mu sync.Mutex
	f := newToolFixture(t, 100*time.Millisecond, func(n int, req runner.Request) (*runner.Outcome, error) {
		mu.Lock()
		runs++
		mu.Unlock()
		if n == 1 {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if _, err := callTool(ctx, t, req, "get_weather", `{}`); err != nil {
				return &runner.Outcome{}, runner.ErrCanceled
			}
			return success("s", "unexpected"), nil
		}
		return success("s2", "from history: "+req.Stdin), nil
	})
	res, err := f.svc.Execute(context.Background(), toolRequest(user("weather?")), nil)
	if err != nil {
		t.Fatal(err)
	}
	waitUntil(t, func() bool { return f.lim.Active() == 0 })
	if r := f.rec.last(); r.Outcome != "timeout" {
		t.Fatalf("outcome %s", r.Outcome)
	}
	res, err = f.svc.Execute(context.Background(), toolRequest(withResults(res.ToolCalls, "sunny")...), nil)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res.Content, `name="get_weather">sunny</tool>`) || runs != 2 {
		t.Fatalf("fallback %+v (runs %d)", res, runs)
	}
}

func TestToolChoice(t *testing.T) {
	f := newToolFixture(t, time.Minute, func(n int, req runner.Request) (*runner.Outcome, error) {
		return success("s", "ok"), nil
	})
	req := toolRequest(user("x"))
	req.ToolChoice = ToolChoice{Mode: ToolChoiceNone}
	if _, err := f.svc.Execute(context.Background(), req, nil); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(strings.Join(f.exec.calls[0].Args, " "), "mcp__functions") {
		t.Fatal("tool_choice none must not expose functions")
	}

	req = toolRequest(user("x"))
	req.ToolChoice = ToolChoice{Mode: ToolChoiceFunction, Function: "get_weather"}
	req.ParallelToolCalls = false
	if _, err := f.svc.Execute(context.Background(), req, nil); err != nil {
		t.Fatal(err)
	}
	args := strings.Join(f.exec.calls[1].Args, " ")
	if !strings.Contains(args, `must call the tool "get_weather" of the "functions" MCP server`) {
		t.Fatalf("args %s", args)
	}
	if !strings.Contains(args, "at most one tool") {
		t.Fatalf("args %s", args)
	}

	req = toolRequest(user("x"))
	req.ToolChoice = ToolChoice{Mode: ToolChoiceRequired}
	_, _ = f.svc.Execute(context.Background(), req, nil)
	if !strings.Contains(strings.Join(f.exec.calls[2].Args, " "), "at least one tool") {
		t.Fatal("required not expressed")
	}
}

func TestClientDisconnectWhileAttachedStopsTurn(t *testing.T) {
	f := newToolFixture(t, time.Minute, func(n int, req runner.Request) (*runner.Outcome, error) {
		// A CLI that keeps working until it is stopped.
		if _, err := callTool(context.Background(), t, req, "get_weather", `{}`); err != nil {
			return &runner.Outcome{}, runner.ErrCanceled
		}
		return &runner.Outcome{}, runner.ErrCanceled
	})
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		waitUntil(t, func() bool { return f.lim.Active() == 1 })
		cancel()
	}()
	// Depending on timing the request is canceled while running or after
	// the call was returned; either way nothing must keep running.
	res, err := f.svc.Execute(ctx, toolRequest(user("x")), nil)
	if err == nil && res.FinishReason == FinishToolCalls {
		// Paused: the client leaves without answering; the turn stays
		// until its wait timeout, so stop it like a server shutdown.
		lt := f.svc.(*service).waitingTurn(toolRequest(withResults(res.ToolCalls, "x")...))
		lt.abort(newError(KindCanceled, "test"))
	} else if kindOf(t, err) != KindCanceled {
		t.Fatalf("got %v", err)
	}
	waitUntil(t, func() bool { return f.lim.Active() == 0 })
}

func waitUntil(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatal("condition not met")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestStatelessToolCalls(t *testing.T) {
	f := newToolFixture(t, time.Minute, func(n int, req runner.Request) (*runner.Outcome, error) {
		if n == 1 {
			req.OnEvent(runner.Event{Kind: runner.KindUsage, Usage: &runner.Usage{InputTokens: 7, OutputTokens: 3}})
			if _, err := callTool(context.Background(), t, req, "get_weather", `{"city":"Rome"}`); err != nil {
				return &runner.Outcome{SessionID: "s1"}, runner.ErrCanceled
			}
			return success("s1", "must not happen"), nil
		}
		return success("s2", "from history: "+req.Stdin), nil
	})
	req := toolRequest(user("weather?"))
	req.StatelessTools = true
	req.SessionKey = "conv"
	res, err := f.svc.Execute(context.Background(), req, nil)
	if err != nil || res.FinishReason != FinishToolCalls || len(res.ToolCalls) != 1 {
		t.Fatalf("first step %+v %v", res, err)
	}
	waitUntil(t, func() bool { return f.lim.Active() == 0 })
	if r := f.rec.last(); r.Outcome != "tool_calls" || r.InputTokens != 7 || r.OutputTokens != 3 {
		t.Fatalf("record %+v", r)
	}
	if _, ok := f.store.Get("coder|conv"); ok {
		t.Fatal("a session stopped on function calls must not be resumable")
	}
	if f.svc.(*service).waitingTurn(toolRequest(withResults(res.ToolCalls, "x")...)) != nil {
		t.Fatal("stateless turn must not wait")
	}

	next := toolRequest(withResults(res.ToolCalls, "sunny")...)
	next.StatelessTools = true
	next.SessionKey = "conv"
	res, err = f.svc.Execute(context.Background(), next, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(f.exec.calls) != 2 || strings.Contains(strings.Join(f.exec.calls[1].Args, " "), "--resume") ||
		!strings.Contains(res.Content, `name="get_weather">sunny</tool>`) {
		t.Fatalf("second step %+v (args %q)", res, f.exec.calls[1].Args)
	}
}

func TestSystemModes(t *testing.T) {
	f := newFixture(t, config.OnBusyWait, func(int, runner.Request) (*runner.Outcome, error) { return success("s", "ok"), nil })
	sys := prompt.Message{Role: prompt.RoleSystem, Text: "You are the planner."}
	cases := []struct {
		mode, agentPrompt, flag, value string
	}{
		{config.SystemReplace, "", "--system-prompt", "You are the planner."},
		{config.SystemAppend, "", "--append-system-prompt", "You are the planner."},
		{config.SystemReplace, "base rules", "--system-prompt", "base rules\n\nYou are the planner."},
	}
	for i, c := range cases {
		req := request(sys, user("go"))
		req.SessionKey = fmt.Sprintf("k%d", i)
		req.SystemMode = c.mode
		req.Spec.SystemPrompt = c.agentPrompt
		if _, err := f.svc.Execute(context.Background(), req, nil); err != nil {
			t.Fatal(err)
		}
		call := f.exec.calls[len(f.exec.calls)-1]
		if v := argAfter(call.Args, c.flag); v != c.value {
			t.Fatalf("case %d: %s = %q (args %q)", i, c.flag, v, call.Args)
		}
		if call.Stdin != "go" {
			t.Fatalf("case %d: system text left in the prompt: %q", i, call.Stdin)
		}
	}
	// Default: system messages stay in the prompt.
	req := request(sys, user("go"))
	req.SessionKey = "default"
	_, _ = f.svc.Execute(context.Background(), req, nil)
	call := f.exec.calls[len(f.exec.calls)-1]
	if !strings.Contains(call.Stdin, "<instructions>\nYou are the planner.") || argAfter(call.Args, "--system-prompt") != "" {
		t.Fatalf("default mode: %q %q", call.Stdin, call.Args)
	}
}

func argAfter(args []string, name string) string {
	for i := 0; i+1 < len(args); i++ {
		if args[i] == name {
			return args[i+1]
		}
	}
	return ""
}
