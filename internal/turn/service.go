// Package turn orchestrates one chat turn independently of HTTP: session
// lookup and locking, prompt construction, queueing, CLI execution with the
// resume fallback, registry update and statistics.
//
// The HTTP layer depends only on the Service interface; Service depends only
// on the interfaces of its collaborators (session.Store, session.Locker,
// runner.Executor, runner.Limiter, stats.Recorder), so each can be replaced
// or faked independently.
package turn

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/r3n3r0/agent-bridge/internal/config"
	"github.com/r3n3r0/agent-bridge/internal/prompt"
	"github.com/r3n3r0/agent-bridge/internal/redact"
	"github.com/r3n3r0/agent-bridge/internal/runner"
	"github.com/r3n3r0/agent-bridge/internal/session"
	"github.com/r3n3r0/agent-bridge/internal/stats"
)

const (
	maxErrorDetail = 2000
	maxLoggedText  = 500
)

// ProgressFunc receives intermediate assistant texts while a turn runs.
type ProgressFunc func(text string)

// Service executes chat turns.
type Service interface {
	// Admit runs the checks that must fail before any response is sent:
	// session busy (with on_busy reject) and full queue. After a nil
	// error the caller must call Execute with the same request, which
	// releases whatever Admit acquired.
	Admit(req *Request) error
	// Execute runs the turn. Errors are always of type *Error.
	Execute(ctx context.Context, req *Request, progress ProgressFunc) (*Result, error)
}

// Request is a validated turn, with per-request overrides already applied.
type Request struct {
	RequestID  string
	Agent      *config.Agent
	Spec       runner.Spec
	Cwd        string
	Messages   []prompt.Message
	SessionKey string
	Reset      bool

	unlock func() // session lock taken by Admit
}

// InternalKey is the registry key of the request's session.
func (r *Request) InternalKey() string { return session.InternalKey(r.Agent.Name, r.SessionKey) }

// Result is a completed turn.
type Result struct {
	Content          string
	FinishReason     string // "stop" or "length"
	PromptTokens     int64
	CompletionTokens int64
	Turns            int
	CostUSD          float64
	DurationMs       int64
}

// Kind classifies an Error.
type Kind int

// Error kinds.
const (
	KindInvalid     Kind = iota + 1 // the request cannot be executed as sent
	KindSessionBusy                 // session in use and on_busy is reject
	KindQueueFull                   // no slot and no room (or time) to wait
	KindCanceled                    // the caller went away
	KindShutdown                    // the server is stopping
	KindUpstream                    // the CLI failed or returned an error
	KindTimeout                     // the agent timeout expired
)

// Error is the error type returned by Service.
type Error struct {
	Kind    Kind
	Message string
}

func (e *Error) Error() string { return e.Message }

// newError builds an Error; the message is always redacted, whoever
// consumes it.
func newError(k Kind, format string, a ...any) *Error {
	return &Error{Kind: k, Message: redact.String(fmt.Sprintf(format, a...))}
}

// Deps are the collaborators of the default Service.
type Deps struct {
	Store    session.Store
	Locker   session.Locker
	Executor runner.Executor
	Limiter  runner.Limiter
	Recorder stats.Recorder
	Logger   *slog.Logger
}

// Settings configure the default Service.
type Settings struct {
	Binary   string
	AuthMode config.AuthMode
	OnBusy   string
	// BaseEnv is the environment the child environment is derived from.
	BaseEnv []string
	// HardStop, when done, terminates every running turn.
	HardStop context.Context
}

type service struct {
	Deps
	Settings
}

var _ Service = (*service)(nil)

// NewService returns the default Service.
func NewService(d Deps, s Settings) Service {
	if d.Logger == nil {
		d.Logger = slog.Default()
	}
	if s.HardStop == nil {
		s.HardStop = context.Background()
	}
	return &service{Deps: d, Settings: s}
}

func (s *service) Admit(req *Request) error {
	if s.OnBusy == config.OnBusyReject {
		unlock, err := s.Locker.TryLock(req.InternalKey())
		if err != nil {
			return newError(KindSessionBusy, "session %s is busy", req.SessionKey)
		}
		req.unlock = unlock
	}
	if s.Limiter.Full() {
		if req.unlock != nil {
			req.unlock()
			req.unlock = nil
		}
		return newError(KindQueueFull, "too many requests waiting, retry later")
	}
	return nil
}

func (s *service) Execute(ctx context.Context, req *Request, progress ProgressFunc) (*Result, error) {
	// The turn stops when the caller goes away or the server hard-stops.
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	defer context.AfterFunc(s.HardStop, cancel)()

	t := &execution{service: s, req: req, progress: progress}
	t.log = s.Logger.With("request_id", req.RequestID, "agent", req.Agent.Name, "session_key", req.SessionKey)
	t.rec = stats.Turn{RequestID: req.RequestID, Agent: req.Agent.Name, SessionKey: req.SessionKey}
	res, err := t.run(ctx)
	if err != nil {
		return nil, err // keep the interface nil when there is no error
	}
	return res, nil
}

// execution holds the state of one Execute call.
type execution struct {
	*service
	req      *Request
	progress ProgressFunc
	log      *slog.Logger
	rec      stats.Turn
}

func (t *execution) canceled(what string) *Error {
	if t.HardStop.Err() != nil {
		return newError(KindShutdown, "server shutting down")
	}
	return newError(KindCanceled, "request canceled %s", what)
}

func (t *execution) run(ctx context.Context) (*Result, *Error) {
	key := t.req.InternalKey()
	unlock := t.req.unlock
	if unlock == nil {
		var err error
		if unlock, err = t.Locker.Lock(ctx, key); err != nil {
			return nil, t.finish(stats.OutcomeCanceled, t.canceled("while waiting for the session"))
		}
	}
	defer unlock()

	if t.req.Reset {
		t.storeErr(t.Store.Delete(key))
	}
	entry, found := t.Store.Get(key)
	resumed := found && entry.CLISessionID != "" && entry.Cwd == t.req.Cwd
	if found && !resumed {
		t.log.Debug("working directory changed, starting a new session", "old_cwd", entry.Cwd, "cwd", t.req.Cwd)
	}
	hash := instructionsHash(t.req.Messages)
	if resumed && hash != "" && entry.InstructionsHash != "" && hash != entry.InstructionsHash {
		t.log.Debug("system messages differ from the first turn and are ignored on resumed sessions")
	}
	text, err := prompt.Build(t.req.Messages, resumed)
	if err != nil {
		return nil, t.finish(stats.OutcomeError, newError(KindInvalid, "%v", err))
	}

	queueStart := time.Now()
	release, err := t.Limiter.Acquire(ctx)
	t.rec.QueueMs = time.Since(queueStart).Milliseconds()
	switch {
	case errors.Is(err, runner.ErrQueueFull), errors.Is(err, runner.ErrQueueTimeout):
		return nil, t.finish(stats.OutcomeError, newError(KindQueueFull, "too many requests waiting, retry later"))
	case err != nil:
		return nil, t.finish(stats.OutcomeCanceled, t.canceled("while queued"))
	}
	defer release()

	newEntry := func() session.Entry {
		return session.Entry{Agent: t.req.Agent.Name, Cwd: t.req.Cwd, CreatedAt: time.Now().UTC(), InstructionsHash: hash}
	}
	resumeID := ""
	if resumed {
		resumeID = entry.CLISessionID
	} else {
		entry = newEntry()
	}
	out, runErr := t.exec(ctx, text, resumeID)
	if resumed && resumeMissing(out, runErr) {
		t.log.Warn("stored session no longer exists, retrying once as a new session", "claude_session_id", entry.CLISessionID)
		t.storeErr(t.Store.Delete(key))
		resumed, entry = false, newEntry()
		if text, err = prompt.Build(t.req.Messages, false); err != nil {
			return nil, t.finish(stats.OutcomeError, newError(KindInvalid, "%v", err))
		}
		out, runErr = t.exec(ctx, text, "")
	}

	t.rec.Resumed = resumed
	t.rec.Model = out.Model
	t.rec.CLISessionID = out.SessionID
	if out.SessionID != "" {
		entry.CLISessionID = out.SessionID
		entry.LastUsedAt = time.Now().UTC()
		entry.Turns++
		t.storeErr(t.Store.Put(key, entry))
	}
	if t.AuthMode == config.AuthSubscription && out.APIKeySource != "" && out.APIKeySource != "none" {
		t.log.Error("subscription mode but the CLI reports an API key as credential source; check the environment of the server",
			"api_key_source", out.APIKeySource)
	}
	if res := out.Result; res != nil {
		t.rec.Turns = res.NumTurns
		t.rec.DurationMs = res.DurationMs
		t.rec.CostUSD = res.CostUSD
		t.rec.InputTokens = res.Usage.InputTokens
		t.rec.OutputTokens = res.Usage.OutputTokens
		t.rec.CacheReadTokens = res.Usage.CacheReadTokens
		t.rec.CacheCreationTokens = res.Usage.CacheCreationTokens
	}
	if t.rec.DurationMs == 0 {
		t.rec.DurationMs = out.Duration.Milliseconds()
	}
	return t.classify(out, runErr)
}

// classify maps the process outcome to a Result or an Error.
func (t *execution) classify(out *runner.Outcome, runErr error) (*Result, *Error) {
	switch {
	case runErr == nil:
		res := out.Result
		var finish, outcome string
		switch {
		case res.Subtype == runner.SubtypeSuccess && !res.IsError:
			finish, outcome = "stop", stats.OutcomeSuccess
		case res.Subtype == runner.SubtypeMaxTurns:
			finish, outcome = "length", stats.OutcomeMaxTurns
		default:
			msg := fmt.Sprintf("CLI turn failed (subtype %q)", res.Subtype)
			if res.Result != "" {
				msg += ": " + redact.Tail(res.Result, maxErrorDetail)
			}
			return nil, t.finish(stats.OutcomeError, newError(KindUpstream, "%s", msg))
		}
		t.finish(outcome, nil)
		promptTokens := res.Usage.InputTokens + res.Usage.CacheCreationTokens + res.Usage.CacheReadTokens
		return &Result{
			Content:          res.Result,
			FinishReason:     finish,
			PromptTokens:     promptTokens,
			CompletionTokens: res.Usage.OutputTokens,
			Turns:            res.NumTurns,
			CostUSD:          res.CostUSD,
			DurationMs:       t.rec.DurationMs,
		}, nil
	case errors.Is(runErr, runner.ErrTimeout):
		return nil, t.finish(stats.OutcomeTimeout,
			newError(KindTimeout, "turn exceeded the agent timeout of %s", t.req.Agent.Timeout))
	case errors.Is(runErr, runner.ErrCanceled):
		return nil, t.finish(stats.OutcomeCanceled, t.canceled("while running"))
	default:
		msg := runErr.Error()
		if tail := strings.TrimSpace(redact.Tail(out.Stderr, maxErrorDetail)); tail != "" {
			msg += ": " + tail
		}
		return nil, t.finish(stats.OutcomeError, newError(KindUpstream, "%s", msg))
	}
}

func (t *execution) exec(ctx context.Context, text, resumeID string) (*runner.Outcome, error) {
	spec := t.req.Spec
	spec.ResumeID = resumeID
	req := runner.Request{
		Binary:  t.Binary,
		Args:    runner.Args(spec),
		Env:     runner.Env(t.BaseEnv, t.AuthMode),
		Dir:     t.req.Cwd,
		Stdin:   text,
		Timeout: t.req.Agent.Timeout,
		OnEvent: t.onEvent,
	}
	t.log.Debug("starting CLI", "resume", resumeID != "", "prompt", redact.Truncate(text, maxLoggedText))
	return t.Executor.Run(ctx, req)
}

func (t *execution) onEvent(ev runner.Event) {
	switch ev.Kind {
	case runner.KindInit:
		t.log.Debug("CLI session started", "claude_session_id", ev.SessionID, "model", ev.Model, "api_key_source", ev.APIKeySource)
	case runner.KindText:
		t.log.Debug("assistant text", "text", redact.Truncate(ev.Text, maxLoggedText))
		if t.progress != nil {
			t.progress(ev.Text)
		}
	case runner.KindToolUse:
		t.log.Debug("tool use", "tool", ev.ToolName)
	case runner.KindMalformed:
		t.log.Debug("ignored non-JSON output line")
	case runner.KindIgnored:
		t.log.Debug("ignored event", "type", ev.Type)
	}
}

func (t *execution) storeErr(err error) {
	if err != nil {
		t.log.Warn("session store write failed", "error", err)
	}
}

// finish records statistics and the end-of-turn log line, and passes e
// through.
func (t *execution) finish(outcome string, e *Error) *Error {
	rec := t.rec
	rec.Outcome = outcome
	if err := t.Recorder.Record(rec); err != nil {
		t.log.Warn("usage log write failed", "error", err)
	}
	attrs := []any{
		"model", rec.Model, "claude_session_id", rec.CLISessionID, "resumed", rec.Resumed,
		"turns", rec.Turns, "duration_ms", rec.DurationMs, "queue_ms", rec.QueueMs, "cost_usd", rec.CostUSD,
		"input_tokens", rec.InputTokens, "output_tokens", rec.OutputTokens,
		"cache_read_tokens", rec.CacheReadTokens, "cache_creation_tokens", rec.CacheCreationTokens,
		"outcome", outcome,
	}
	if e != nil {
		t.log.Warn("turn finished", append(attrs, "error", e.Message)...)
	} else {
		t.log.Info("turn finished", attrs...)
	}
	return e
}

// resumeMissing reports whether a resumed turn failed because the stored
// session does not exist any more.
func resumeMissing(out *runner.Outcome, err error) bool {
	if errors.Is(err, runner.ErrTimeout) || errors.Is(err, runner.ErrCanceled) {
		return false
	}
	text := strings.ToLower(out.Stderr)
	if out.Result != nil {
		if !out.Result.IsError && out.Result.Subtype == runner.SubtypeSuccess {
			return false
		}
		text += "\n" + strings.ToLower(out.Result.Result)
	}
	return strings.Contains(text, "no conversation found")
}

func instructionsHash(msgs []prompt.Message) string {
	text := prompt.Instructions(msgs)
	if text == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(text))
	return hex.EncodeToString(sum[:8])
}
