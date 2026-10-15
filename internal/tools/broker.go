package tools

import (
	"context"
)

// Handler executes the calls of one turn.
type Handler interface {
	// Call blocks until the result is available, ctx is done or the turn
	// ends (ErrClosed).
	Call(ctx context.Context, name string, arguments []byte) (Result, error)
}

// Registration identifies a registered handler.
type Registration struct {
	Endpoint string
	Token    string
	// Unregister removes the handler; later calls fail with ErrClosed.
	Unregister func()
}

// Broker routes calls from MCP subprocesses to turn handlers.
type Broker interface {
	Register(h Handler) (Registration, error)
}
