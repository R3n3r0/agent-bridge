package runner

import (
	"context"
	"errors"
	"time"
)

// Process management constants.
const (
	// killGrace is the time between SIGTERM and SIGKILL, and the exec
	// WaitDelay used so a stuck pipe never blocks a turn forever.
	killGrace = 5 * time.Second
	// MaxLineBytes is the largest stdout line accepted.
	MaxLineBytes = 64 << 20
	// stderrKeep is how much stderr is retained for error messages.
	stderrKeep = 64 << 10
)

// Errors returned by Run when no result event was received.
var (
	ErrTimeout  = errors.New("turn timed out")
	ErrCanceled = errors.New("turn canceled")
	ErrNoResult = errors.New("process ended without a result event")
)

// Executor runs one turn of the CLI. CLIExecutor is the implementation that
// starts a real process; tests and alternative back ends provide their own.
type Executor interface {
	// Run executes req until the process exits. The returned error is nil
	// whenever a result event arrived; otherwise it is ErrTimeout,
	// ErrCanceled, ErrNoResult or a start error. The Outcome is never nil.
	Run(ctx context.Context, req Request) (*Outcome, error)
}

// Request describes one CLI process.
type Request struct {
	Binary  string
	Args    []string
	Env     []string
	Dir     string
	Stdin   string
	Timeout time.Duration
	// OnEvent, if set, receives every parsed event in order.
	OnEvent func(Event)
}

// Outcome collects what was observed while the process ran.
type Outcome struct {
	// SessionID is the last session id seen in init or result events.
	SessionID    string
	Model        string
	APIKeySource string
	Result       *ResultEvent
	Stderr       string
	ExitErr      error
	Duration     time.Duration
}
