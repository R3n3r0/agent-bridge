// Package turn orchestrates one chat turn independently of HTTP: session
// lookup and locking, prompt construction, queueing, CLI execution with the
// resume fallback, client function calls, registry update and statistics.
//
// The HTTP layer depends only on the Service interface; Service depends only
// on the interfaces of its collaborators (session.Store, session.Locker,
// runner.Executor, runner.Limiter, stats.Recorder, tools.Broker), so each can
// be replaced or faked independently.
//
// A turn is a CLI process. When the request defines functions (OpenAI
// "tools") and the model calls one, Execute returns the calls with finish
// reason "tool_calls" while the process stays alive, waiting. The next
// request carrying the results (role "tool" messages with the same
// tool_call_id) resumes it, exactly like a follow-up request to the OpenAI
// API. If no results arrive in time the process is stopped; a later request
// with the results then starts a new process from the full history.
package turn

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/r3n3r0/agent-bridge/internal/config"
	"github.com/r3n3r0/agent-bridge/internal/media"
	"github.com/r3n3r0/agent-bridge/internal/prompt"
	"github.com/r3n3r0/agent-bridge/internal/redact"
	"github.com/r3n3r0/agent-bridge/internal/runner"
	"github.com/r3n3r0/agent-bridge/internal/session"
	"github.com/r3n3r0/agent-bridge/internal/stats"
	"github.com/r3n3r0/agent-bridge/internal/tools"
)

const (
	maxErrorDetail = 2000
	maxLoggedText  = 500

	defaultToolWaitTimeout = 10 * time.Minute
	defaultToolBatchWindow = 300 * time.Millisecond
)

// Finish reasons, as in the OpenAI API.
const (
	FinishStop      = "stop"
	FinishLength    = "length"
	FinishToolCalls = "tool_calls"
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
	// Release gives back what Admit acquired, for a request that will not
	// be executed after all. It is a no-op when there is nothing to release.
	Release(req *Request)
	// Execute runs the turn, or resumes the turn waiting for the function
	// results contained in req. Errors are always of type *Error.
	Execute(ctx context.Context, req *Request, progress ProgressFunc) (*Result, error)
}

// Tool choice modes, as in the OpenAI API.
const (
	ToolChoiceAuto     = "auto"
	ToolChoiceNone     = "none"
	ToolChoiceRequired = "required"
	ToolChoiceFunction = "function"
)

// ToolChoice is the parsed OpenAI tool_choice.
type ToolChoice struct {
	Mode     string // one of the ToolChoice* constants; empty means auto
	Function string // with ToolChoiceFunction
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

	// Client functions.
	Tools             []tools.Definition
	ToolChoice        ToolChoice
	ParallelToolCalls bool
	// LegacyFunctions marks the deprecated "functions" API: its results
	// carry no call id, so the paused process cannot be resumed and is
	// stopped as soon as the call is returned; the follow-up request then
	// starts from the full history.
	LegacyFunctions bool
	// StatelessTools stops the process as soon as function calls are
	// returned: the client owns the conversation state and the next
	// request starts a new process from the history it sends.
	StatelessTools bool
	// MaxOutputTokens limits each model request of the turn (0 = default).
	MaxOutputTokens int
	// SystemMode is how system and developer messages reach the model:
	// config.SystemAsPrompt (default), SystemAppend or SystemReplace.
	SystemMode string

	unlock func()    // session lock taken by Admit
	attach *liveTurn // waiting turn this request resumes
}

// InternalKey is the registry key of the request's session.
func (r *Request) InternalKey() string { return session.InternalKey(r.Agent.Name, r.SessionKey) }

// Result is a completed turn, or a turn paused on function calls.
type Result struct {
	Content          string
	FinishReason     string // FinishStop, FinishLength or FinishToolCalls
	ToolCalls        []tools.Call
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
	// Broker routes client function calls; nil disables client functions
	// (the tools field of requests is then ignored).
	Broker tools.Broker
	// Images loads the images referenced by requests; nil rejects requests
	// that contain images.
	Images media.Resolver
	Logger *slog.Logger
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
	// ToolHelper is the command (and leading arguments) that serves client
	// functions over MCP, e.g. the bridge binary with "mcp-tools".
	ToolHelper []string
	// ToolWaitTimeout bounds the wait for function results (default 10m).
	ToolWaitTimeout time.Duration
	// ToolBatchWindow is how long calls are collected before being returned
	// together as parallel tool calls (default 300ms).
	ToolBatchWindow time.Duration
	// MaxImages bounds the images of one turn (0 = no limit).
	MaxImages int
}

type service struct {
	Deps
	Settings

	parkMu sync.Mutex
	parked map[string]*liveTurn // tool call id -> turn waiting for it
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
	if s.ToolWaitTimeout <= 0 {
		s.ToolWaitTimeout = defaultToolWaitTimeout
	}
	if s.ToolBatchWindow <= 0 {
		s.ToolBatchWindow = defaultToolBatchWindow
	}
	return &service{Deps: d, Settings: s, parked: map[string]*liveTurn{}}
}

func (s *service) Admit(req *Request) error {
	if lt := s.waitingTurn(req); lt != nil {
		req.attach = lt // resuming a turn that already holds lock and slot
		return nil
	}
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

func (s *service) Release(req *Request) {
	if req.unlock != nil {
		req.unlock()
		req.unlock = nil
	}
	req.attach = nil
}

func (s *service) Execute(ctx context.Context, req *Request, progress ProgressFunc) (*Result, error) {
	lt := req.attach
	if lt == nil {
		lt = s.waitingTurn(req)
	}
	var res *Result
	var err *Error
	if lt != nil {
		res, err = lt.resume(ctx, req, progress)
	} else {
		res, err = s.start(ctx, req, progress)
	}
	if err != nil {
		return nil, err // keep the interface nil when there is no error
	}
	return res, nil
}

// waitingTurn returns the turn waiting for the function results in req.
func (s *service) waitingTurn(req *Request) *liveTurn {
	s.parkMu.Lock()
	defer s.parkMu.Unlock()
	for id := range prompt.ToolResults(req.Messages) {
		if lt := s.parked[id]; lt != nil && lt.req.Agent.Name == req.Agent.Name {
			return lt
		}
	}
	return nil
}

func (s *service) park(ids []string, lt *liveTurn) {
	s.parkMu.Lock()
	defer s.parkMu.Unlock()
	for _, id := range ids {
		s.parked[id] = lt
	}
}

func (s *service) unpark(ids []string) {
	s.parkMu.Lock()
	defer s.parkMu.Unlock()
	for _, id := range ids {
		delete(s.parked, id)
	}
}

// start prepares and launches a new turn, then waits for its first stop.
func (s *service) start(ctx context.Context, req *Request, progress ProgressFunc) (*Result, *Error) {
	lt := &liveTurn{svc: s, req: req, stops: make(chan stopPoint, 4)}
	lt.log = s.Logger.With("request_id", req.RequestID, "agent", req.Agent.Name, "session_key", req.SessionKey)
	lt.rec = stats.Turn{RequestID: req.RequestID, Agent: req.Agent.Name, SessionKey: req.SessionKey}

	key := req.InternalKey()
	unlock := req.unlock
	if unlock == nil {
		var err error
		if unlock, err = s.Locker.Lock(ctx, key); err != nil {
			return nil, lt.finish(stats.OutcomeCanceled, lt.canceled("while waiting for the session"))
		}
	}
	held := true
	defer func() {
		if held {
			unlock()
		}
	}()

	if req.Reset {
		lt.storeErr(s.Store.Delete(key))
	}
	entry, found := s.Store.Get(key)
	resumed := found && entry.CLISessionID != "" && entry.Cwd == req.Cwd
	if found && !resumed {
		lt.log.Debug("working directory changed, starting a new session", "old_cwd", entry.Cwd, "cwd", req.Cwd)
	}
	hash := instructionsHash(req.Messages)
	if resumed && hash != "" && entry.InstructionsHash != "" && hash != entry.InstructionsHash {
		lt.log.Debug("system messages differ from the first turn and are ignored on resumed sessions")
	}
	applySystemMode(req)
	in, perr := s.prepareInput(ctx, req, resumed)
	if perr != nil {
		outcome := stats.OutcomeError
		if perr.Kind == KindCanceled {
			outcome = stats.OutcomeCanceled
		}
		return nil, lt.finish(outcome, perr)
	}

	queueStart := time.Now()
	release, err := s.Limiter.Acquire(ctx)
	lt.rec.QueueMs = time.Since(queueStart).Milliseconds()
	switch {
	case errors.Is(err, runner.ErrQueueFull), errors.Is(err, runner.ErrQueueTimeout):
		return nil, lt.finish(stats.OutcomeError, newError(KindQueueFull, "too many requests waiting, retry later"))
	case err != nil:
		return nil, lt.finish(stats.OutcomeCanceled, lt.canceled("while queued"))
	}

	// From here on the process may outlive this request (it can wait for
	// function results), so it gets its own context.
	lt.ctx, lt.cancel = context.WithCancel(context.Background())
	stopOnHard := context.AfterFunc(s.HardStop, lt.cancel)
	if err := lt.setupClientTools(); err != nil {
		stopOnHard()
		lt.cancel()
		release()
		return nil, lt.finish(stats.OutcomeError, newError(KindUpstream, "cannot prepare client functions: %v", err))
	}
	if !resumed {
		entry = session.Entry{Agent: req.Agent.Name, Cwd: req.Cwd, CreatedAt: time.Now().UTC(), InstructionsHash: hash}
	}
	held = false
	go lt.run(func() { stopOnHard(); release(); unlock() }, entry, resumed, in, hash)
	return lt.wait(ctx, progress)
}

// stopPoint is where a waiting consumer gets control back: a batch of
// function calls, or the end of the turn.
type stopPoint struct {
	res   *Result
	err   *Error
	final bool
}

type pendingCall struct {
	call   tools.Call
	result chan tools.Result
}

// liveTurn is a running CLI process and its function-call state.
type liveTurn struct {
	svc    *service
	req    *Request
	log    *slog.Logger
	rec    stats.Turn // owned by the run goroutine after start
	ctx    context.Context
	cancel context.CancelFunc
	stops  chan stopPoint // closed after the final stop

	toolsDir   string
	unregister func()

	mu         sync.Mutex
	progress   ProgressFunc
	texts      []string      // assistant texts since the last stop
	lastUsage  *runner.Usage // usage of the latest assistant message
	batch      []*pendingCall
	batchTimer *time.Timer
	waiting    []*pendingCall // returned to the client, awaiting results
	parkTimer  *time.Timer
	parkedIDs  []string
	finished   bool
	reason     *Error // why the turn was stopped from inside
	// stoppedForCalls is set when the process is stopped right after
	// returning function calls (stateless or deprecated functions mode).
	stoppedForCalls bool
}

var _ tools.Handler = (*liveTurn)(nil)

// setupClientTools registers the turn with the broker and writes the MCP
// configuration exposing the request's functions.
func (lt *liveTurn) setupClientTools() error {
	req, s := lt.req, lt.svc
	if len(req.Tools) == 0 || req.ToolChoice.Mode == ToolChoiceNone || s.Broker == nil {
		return nil
	}
	reg, err := s.Broker.Register(lt)
	if err != nil {
		return err
	}
	dir, err := os.MkdirTemp("", "agent-bridge-functions-")
	if err != nil {
		reg.Unregister()
		return err
	}
	cfg, err := tools.WriteFiles(dir, tools.Spec{Endpoint: reg.Endpoint, Token: reg.Token, Tools: req.Tools}, s.ToolHelper)
	if err != nil {
		reg.Unregister()
		os.RemoveAll(dir)
		return err
	}
	lt.unregister, lt.toolsDir = reg.Unregister, dir
	req.Spec.ClientTools = cfg
	if extra := toolInstructions(req); extra != "" {
		if req.Spec.SystemPrompt == "" {
			req.Spec.SystemPrompt = extra
		} else {
			req.Spec.SystemPrompt += "\n\n" + extra
		}
	}
	return nil
}

// toolInstructions turns tool_choice and parallel_tool_calls into
// instructions, the only way to express them to the model.
func toolInstructions(req *Request) string {
	server := tools.ServerName
	var out []string
	switch req.ToolChoice.Mode {
	case ToolChoiceRequired:
		out = append(out, fmt.Sprintf("Before answering you must call at least one tool of the %q MCP server.", server))
	case ToolChoiceFunction:
		out = append(out, fmt.Sprintf("Before answering you must call the tool %s%s.", tools.ToolPrefix, req.ToolChoice.Function))
	}
	if !req.ParallelToolCalls {
		out = append(out, fmt.Sprintf("Call at most one tool of the %q MCP server at a time.", server))
	}
	return strings.Join(out, " ")
}

// wait gives control to the consumer until the next stop point. If ctx ends
// first, the turn is stopped.
func (lt *liveTurn) wait(ctx context.Context, progress ProgressFunc) (*Result, *Error) {
	lt.mu.Lock()
	lt.progress = progress
	lt.mu.Unlock()
	defer func() {
		lt.mu.Lock()
		lt.progress = nil
		lt.mu.Unlock()
	}()
	select {
	case sp, ok := <-lt.stops:
		if ok {
			return sp.res, sp.err
		}
	case <-ctx.Done():
		lt.abort(nil)
	}
	var last stopPoint
	for sp := range lt.stops {
		last = sp
	}
	if last.err == nil {
		last.err = newError(KindCanceled, "request canceled")
	}
	return nil, last.err
}

// resume delivers the function results in req and waits for the next stop.
func (lt *liveTurn) resume(ctx context.Context, req *Request, progress ProgressFunc) (*Result, *Error) {
	results := prompt.ToolResults(req.Messages)
	lt.mu.Lock()
	if lt.finished {
		lt.mu.Unlock()
		req.attach = nil
		return lt.svc.start(ctx, req, progress)
	}
	if lt.parkTimer != nil {
		lt.parkTimer.Stop()
	}
	lt.svc.unpark(lt.parkedIDs)
	lt.parkedIDs = nil
	waiting := lt.waiting
	lt.waiting = nil
	lt.progress = progress
	lt.mu.Unlock()

	for _, pc := range waiting {
		r, ok := results[pc.call.ID]
		res := tools.Result{Content: r}
		if !ok {
			res = tools.Result{Content: "The caller did not return a result for this call.", IsError: true}
		}
		pc.result <- res
	}
	lt.mu.Lock()
	if len(lt.batch) > 0 && lt.batchTimer == nil {
		lt.batchTimer = time.AfterFunc(lt.svc.ToolBatchWindow, lt.flush)
	}
	lt.mu.Unlock()
	return lt.wait(ctx, progress)
}

// Call implements tools.Handler: it queues a call made by the model and
// blocks until the client sends its result.
func (lt *liveTurn) Call(ctx context.Context, name string, arguments []byte) (tools.Result, error) {
	pc := &pendingCall{
		call:   tools.Call{ID: "call_" + randomHex(12), Name: name, Arguments: string(arguments)},
		result: make(chan tools.Result, 1),
	}
	lt.mu.Lock()
	if lt.finished {
		lt.mu.Unlock()
		return tools.Result{}, tools.ErrClosed
	}
	lt.batch = append(lt.batch, pc)
	if lt.batchTimer == nil && len(lt.waiting) == 0 {
		lt.batchTimer = time.AfterFunc(lt.svc.ToolBatchWindow, lt.flush)
	}
	lt.mu.Unlock()
	lt.log.Debug("client function called", "function", name, "tool_call_id", pc.call.ID)

	select {
	case r := <-pc.result:
		return r, nil
	case <-ctx.Done():
		return tools.Result{}, ctx.Err()
	case <-lt.ctx.Done():
		return tools.Result{}, tools.ErrClosed
	}
}

// flush returns the collected calls to the consumer and parks the turn.
func (lt *liveTurn) flush() {
	lt.mu.Lock()
	defer lt.mu.Unlock()
	lt.batchTimer = nil
	if lt.finished || len(lt.batch) == 0 || len(lt.waiting) > 0 {
		return
	}
	n := len(lt.batch)
	if !lt.req.ParallelToolCalls || lt.req.LegacyFunctions {
		// With parallel calls disabled, later calls of the batch wait for
		// the next round; in stateless mode the model makes them again.
		n = 1
	}
	lt.waiting = lt.batch[:n:n]
	lt.batch = append([]*pendingCall(nil), lt.batch[n:]...)

	res := &Result{FinishReason: FinishToolCalls, Content: strings.Join(lt.texts, "\n\n")}
	lt.texts = nil
	if u := lt.lastUsage; u != nil {
		res.PromptTokens = u.InputTokens + u.CacheReadTokens + u.CacheCreationTokens
		res.CompletionTokens = u.OutputTokens
	}
	ids := make([]string, len(lt.waiting))
	for i, pc := range lt.waiting {
		ids[i] = pc.call.ID
		res.ToolCalls = append(res.ToolCalls, pc.call)
	}
	lt.parkedIDs = ids
	lt.svc.park(ids, lt)
	timeout := lt.svc.ToolWaitTimeout
	if lt.req.LegacyFunctions || lt.req.StatelessTools {
		lt.stoppedForCalls = true
		lt.stops <- stopPoint{res: res}
		go lt.abort(newError(KindCanceled, "function calls returned to the caller"))
		return
	}
	lt.parkTimer = time.AfterFunc(timeout, func() {
		lt.log.Warn("no function results received in time, stopping the turn", "timeout", timeout.String())
		lt.abort(newError(KindTimeout, "no function results received within %s", timeout))
	})
	lt.stops <- stopPoint{res: res}
}

// abort stops the process; reason, if set, becomes the turn's error.
func (lt *liveTurn) abort(reason *Error) {
	lt.mu.Lock()
	if reason != nil && lt.reason == nil {
		lt.reason = reason
	}
	lt.mu.Unlock()
	lt.cancel()
}

func (lt *liveTurn) canceled(what string) *Error {
	if lt.svc.HardStop.Err() != nil {
		return newError(KindShutdown, "server shutting down")
	}
	lt.mu.Lock()
	reason := lt.reason
	lt.mu.Unlock()
	if reason != nil {
		return reason
	}
	return newError(KindCanceled, "request canceled %s", what)
}

// run executes the process (with the resume fallback), records the outcome,
// releases resources and delivers the final stop.
func (lt *liveTurn) run(release func(), entry session.Entry, resumed bool, in input, hash string) {
	s, req := lt.svc, lt.req
	key := req.InternalKey()
	resumeID := ""
	if resumed {
		resumeID = entry.CLISessionID
	}
	out, runErr := lt.exec(in, resumeID)
	if resumed && resumeMissing(out, runErr) {
		lt.log.Warn("stored session no longer exists, retrying once as a new session", "claude_session_id", entry.CLISessionID)
		lt.storeErr(s.Store.Delete(key))
		resumed = false
		entry = session.Entry{Agent: req.Agent.Name, Cwd: req.Cwd, CreatedAt: time.Now().UTC(), InstructionsHash: hash}
		if full, err := s.prepareInput(lt.ctx, req, false); err == nil {
			out, runErr = lt.exec(full, "")
		}
	}

	lt.rec.Resumed = resumed
	lt.rec.Model = out.Model
	lt.rec.CLISessionID = out.SessionID
	lt.mu.Lock()
	stopped, lastUsage := lt.stoppedForCalls, lt.lastUsage
	lt.mu.Unlock()
	switch {
	case stopped:
		// The CLI session ends with calls that never got their results:
		// it must not be resumed. The next request starts from history.
		lt.storeErr(s.Store.Delete(key))
		if u := lastUsage; u != nil && out.Result == nil {
			lt.rec.InputTokens = u.InputTokens
			lt.rec.OutputTokens = u.OutputTokens
			lt.rec.CacheReadTokens = u.CacheReadTokens
			lt.rec.CacheCreationTokens = u.CacheCreationTokens
		}
	case out.SessionID != "":
		entry.CLISessionID = out.SessionID
		entry.LastUsedAt = time.Now().UTC()
		entry.Turns++
		lt.storeErr(s.Store.Put(key, entry))
	}
	if s.AuthMode == config.AuthSubscription && out.APIKeySource != "" && out.APIKeySource != "none" {
		lt.log.Error("subscription mode but the CLI reports an API key as credential source; check the environment of the server",
			"api_key_source", out.APIKeySource)
	}
	if res := out.Result; res != nil {
		lt.rec.Turns = res.NumTurns
		lt.rec.DurationMs = res.DurationMs
		lt.rec.CostUSD = res.CostUSD
		lt.rec.InputTokens = res.Usage.InputTokens
		lt.rec.OutputTokens = res.Usage.OutputTokens
		lt.rec.CacheReadTokens = res.Usage.CacheReadTokens
		lt.rec.CacheCreationTokens = res.Usage.CacheCreationTokens
	}
	if lt.rec.DurationMs == 0 {
		lt.rec.DurationMs = out.Duration.Milliseconds()
	}
	res, err := lt.classify(out, runErr)

	// Release before answering, so a follow-up request never finds the
	// session or the slot still taken.
	lt.mu.Lock()
	lt.finished = true
	if lt.batchTimer != nil {
		lt.batchTimer.Stop()
	}
	if lt.parkTimer != nil {
		lt.parkTimer.Stop()
	}
	s.unpark(lt.parkedIDs)
	lt.mu.Unlock()
	lt.cancel()
	if lt.unregister != nil {
		lt.unregister()
	}
	if lt.toolsDir != "" {
		os.RemoveAll(lt.toolsDir)
	}
	release()

	lt.mu.Lock()
	lt.stops <- stopPoint{res: res, err: err, final: true}
	close(lt.stops)
	lt.mu.Unlock()
}

func (lt *liveTurn) stoppedAfterCalls() bool {
	lt.mu.Lock()
	defer lt.mu.Unlock()
	return lt.stoppedForCalls
}

func (r *Request) omitInstructions() bool {
	return r.SystemMode == config.SystemAppend || r.SystemMode == config.SystemReplace
}

// applySystemMode moves the request's system and developer messages into
// the CLI system prompt when SystemMode asks for it. They are combined
// with the agent's own system prompt, which always comes first.
func applySystemMode(req *Request) {
	if !req.omitInstructions() {
		return
	}
	instructions := prompt.Instructions(req.Messages)
	if req.SystemMode == config.SystemReplace {
		req.Spec.SystemPromptMode = config.PromptModeReplace
	}
	if instructions == "" {
		return
	}
	if req.Spec.SystemPrompt == "" {
		req.Spec.SystemPrompt = instructions
	} else {
		req.Spec.SystemPrompt += "\n\n" + instructions
	}
}

// classify maps the process outcome to a Result or an Error.
func (lt *liveTurn) classify(out *runner.Outcome, runErr error) (*Result, *Error) {
	switch {
	case runErr == nil:
		res := out.Result
		content := res.Result
		var finish, outcome string
		switch {
		case res.Subtype == runner.SubtypeStructuredOutputFail:
			return nil, lt.finish(stats.OutcomeError, newError(KindUpstream, "the model could not produce output matching the JSON schema"))
		case res.Subtype == runner.SubtypeSuccess && !res.IsError && lt.req.Spec.JSONSchema != "":
			structured, err := compactJSON(res.StructuredOutput)
			if err != nil {
				return nil, lt.finish(stats.OutcomeError, newError(KindUpstream, "the turn ended without structured output"))
			}
			content = structured
			finish, outcome = FinishStop, stats.OutcomeSuccess
		case res.Subtype == runner.SubtypeSuccess && !res.IsError:
			finish, outcome = FinishStop, stats.OutcomeSuccess
		case res.Subtype == runner.SubtypeMaxTurns:
			finish, outcome = FinishLength, stats.OutcomeMaxTurns
		default:
			msg := fmt.Sprintf("CLI turn failed (subtype %q)", res.Subtype)
			if res.Result != "" {
				msg += ": " + redact.Tail(res.Result, maxErrorDetail)
			}
			return nil, lt.finish(stats.OutcomeError, newError(KindUpstream, "%s", msg))
		}
		lt.finish(outcome, nil)
		promptTokens := res.Usage.InputTokens + res.Usage.CacheCreationTokens + res.Usage.CacheReadTokens
		return &Result{
			Content:          content,
			FinishReason:     finish,
			PromptTokens:     promptTokens,
			CompletionTokens: res.Usage.OutputTokens,
			Turns:            res.NumTurns,
			CostUSD:          res.CostUSD,
			DurationMs:       lt.rec.DurationMs,
		}, nil
	case errors.Is(runErr, runner.ErrTimeout):
		return nil, lt.finish(stats.OutcomeTimeout,
			newError(KindTimeout, "turn exceeded the agent timeout of %s", lt.req.Agent.Timeout))
	case errors.Is(runErr, runner.ErrCanceled) && lt.stoppedAfterCalls():
		lt.finish(stats.OutcomeToolCalls, nil)
		return nil, newError(KindCanceled, "process stopped after returning function calls")
	case errors.Is(runErr, runner.ErrCanceled):
		e := lt.canceled("while running")
		outcome := stats.OutcomeCanceled
		if e.Kind == KindTimeout {
			outcome = stats.OutcomeTimeout
		}
		return nil, lt.finish(outcome, e)
	default:
		msg := runErr.Error()
		if tail := strings.TrimSpace(redact.Tail(out.Stderr, maxErrorDetail)); tail != "" {
			msg += ": " + tail
		}
		return nil, lt.finish(stats.OutcomeError, newError(KindUpstream, "%s", msg))
	}
}

// input is what is written to the CLI's stdin for one process.
type input struct {
	text   string
	images []runner.ImageBlock
}

// prepareInput builds the prompt and loads its images.
func (s *service) prepareInput(ctx context.Context, req *Request, resumed bool) (input, *Error) {
	p, err := prompt.BuildPrompt(req.Messages, prompt.Options{Resumed: resumed, OmitInstructions: req.omitInstructions()})
	if err != nil {
		return input{}, newError(KindInvalid, "%v", err)
	}
	in := input{text: p.Text}
	if len(p.Images) == 0 {
		return in, nil
	}
	if s.Images == nil {
		return input{}, newError(KindInvalid, "images are not enabled on this server")
	}
	if s.MaxImages > 0 && len(p.Images) > s.MaxImages {
		return input{}, newError(KindInvalid, "too many images: %d, the limit is %d", len(p.Images), s.MaxImages)
	}
	for i, url := range p.Images {
		img, err := s.Images.Resolve(ctx, url)
		if err != nil {
			if ctx.Err() != nil {
				return input{}, newError(KindCanceled, "request canceled while loading images")
			}
			return input{}, newError(KindInvalid, "image %d: %v", i+1, err)
		}
		in.images = append(in.images, runner.ImageBlock{MediaType: img.MediaType, Data: img.Data})
	}
	if n := len(runner.UserMessage(in.text, in.images)); n > prompt.MaxBytes {
		return input{}, newError(KindInvalid, "request too large: %d bytes with images, the limit is %d", n, prompt.MaxBytes)
	}
	return in, nil
}

func (lt *liveTurn) exec(in input, resumeID string) (*runner.Outcome, error) {
	s := lt.svc
	spec := lt.req.Spec
	spec.ResumeID = resumeID
	text := in.text
	stdin := in.text
	if len(in.images) > 0 {
		spec.StructuredInput = true
		stdin = runner.UserMessage(in.text, in.images)
	}
	env := runner.Env(s.BaseEnv, s.AuthMode)
	if spec.ClientTools != "" {
		env = runner.WithClientToolsTimeout(env, lt.req.Agent.Timeout)
	}
	env = runner.WithMaxOutputTokens(env, lt.req.MaxOutputTokens)
	req := runner.Request{
		Binary:  s.Binary,
		Args:    runner.Args(spec),
		Env:     env,
		Dir:     lt.req.Cwd,
		Stdin:   stdin,
		Timeout: lt.req.Agent.Timeout,
		OnEvent: lt.onEvent,
	}
	lt.log.Debug("starting CLI", "resume", resumeID != "", "images", len(in.images), "prompt", redact.Truncate(text, maxLoggedText))
	return s.Executor.Run(lt.ctx, req)
}

func (lt *liveTurn) onEvent(ev runner.Event) {
	switch ev.Kind {
	case runner.KindInit:
		lt.log.Debug("CLI session started", "claude_session_id", ev.SessionID, "model", ev.Model, "api_key_source", ev.APIKeySource)
	case runner.KindText:
		lt.log.Debug("assistant text", "text", redact.Truncate(ev.Text, maxLoggedText))
		lt.mu.Lock()
		lt.texts = append(lt.texts, ev.Text)
		progress := lt.progress
		lt.mu.Unlock()
		if progress != nil {
			progress(ev.Text)
		}
	case runner.KindUsage:
		lt.mu.Lock()
		lt.lastUsage = ev.Usage
		lt.mu.Unlock()
	case runner.KindToolUse:
		lt.log.Debug("tool use", "tool", ev.ToolName)
	case runner.KindMalformed:
		lt.log.Debug("ignored non-JSON output line")
	case runner.KindIgnored:
		lt.log.Debug("ignored event", "type", ev.Type)
	}
}

func (lt *liveTurn) storeErr(err error) {
	if err != nil {
		lt.log.Warn("session store write failed", "error", err)
	}
}

// finish records statistics and the end-of-turn log line, and passes e
// through.
func (lt *liveTurn) finish(outcome string, e *Error) *Error {
	rec := lt.rec
	rec.Outcome = outcome
	if err := lt.svc.Recorder.Record(rec); err != nil {
		lt.log.Warn("usage log write failed", "error", err)
	}
	attrs := []any{
		"model", rec.Model, "claude_session_id", rec.CLISessionID, "resumed", rec.Resumed,
		"turns", rec.Turns, "duration_ms", rec.DurationMs, "queue_ms", rec.QueueMs, "cost_usd", rec.CostUSD,
		"input_tokens", rec.InputTokens, "output_tokens", rec.OutputTokens,
		"cache_read_tokens", rec.CacheReadTokens, "cache_creation_tokens", rec.CacheCreationTokens,
		"outcome", outcome,
	}
	if e != nil {
		lt.log.Warn("turn finished", append(attrs, "error", e.Message)...)
	} else {
		lt.log.Info("turn finished", attrs...)
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

// compactJSON returns raw as compact JSON; empty or null is an error.
func compactJSON(raw json.RawMessage) (string, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return "", errors.New("no structured output")
	}
	var b bytes.Buffer
	if err := json.Compact(&b, raw); err != nil {
		return "", err
	}
	return b.String(), nil
}

func randomHex(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b)
}
