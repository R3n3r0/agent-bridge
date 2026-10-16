package runner

import (
	"time"

	"github.com/r3n3r0/agent-bridge/internal/config"
)

// Backend is a CLI dialect: how one turn is started and how its output is
// read. StreamJSONBackend drives the default CLI, CodexBackend the codex
// CLI. Everything else in the bridge is independent of the CLI in use.
type Backend interface {
	// Name is the value of the "backend" setting.
	Name() string
	// Prepare returns the process invocation of one turn. The caller must
	// call Invocation.Cleanup once the process has exited.
	Prepare(t Turn) (*Invocation, error)
	// NewParser returns the event parser of one process.
	NewParser() Parser
	// ResumeMissing reports whether a resumed process failed because its
	// stored session no longer exists.
	ResumeMissing(out *Outcome) bool
	// MinVersion is the oldest CLI version the backend is tested against.
	MinVersion() string
	// CumulativeUsage reports whether the result usage of a resumed
	// session is the session total rather than the usage of the turn.
	CumulativeUsage() bool
}

// Parser turns the stdout lines of one process into events. A parser may
// keep state between lines; it is used by a single goroutine.
type Parser interface {
	Parse(line []byte) []Event
}

// Turn is everything a Backend needs to build an invocation.
type Turn struct {
	Spec   Spec
	Prompt string
	Images []ImageBlock
	// BaseEnv is the environment the child environment is derived from.
	BaseEnv  []string
	AuthMode config.AuthMode
	// MaxOutputTokens limits each model request (0 = default); backends
	// without an equivalent ignore it.
	MaxOutputTokens int
	// ToolTimeout is how long a client function call may wait for the
	// client (0 = no limit).
	ToolTimeout time.Duration
}

// Invocation is a ready-to-run process: arguments, environment and stdin.
type Invocation struct {
	Args  []string
	Env   []string
	Stdin string
	// Cleanup removes the temporary files of the invocation; never nil.
	Cleanup func()
}

// NewBackend returns the backend named by the "backend" setting.
func NewBackend(name string) (Backend, error) {
	switch name {
	case config.BackendDefault, "":
		return StreamJSONBackend{}, nil
	case config.BackendCodex:
		return CodexBackend{}, nil
	}
	return nil, &UnknownBackendError{Name: name}
}

// UnknownBackendError is returned by NewBackend for an unknown name.
type UnknownBackendError struct{ Name string }

func (e *UnknownBackendError) Error() string { return "unknown backend " + e.Name }
