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
	"context"

	"github.com/r3n3r0/agent-bridge/internal/config"
	"github.com/r3n3r0/agent-bridge/internal/prompt"
	"github.com/r3n3r0/agent-bridge/internal/runner"
	"github.com/r3n3r0/agent-bridge/internal/session"
	"github.com/r3n3r0/agent-bridge/internal/tools"
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
