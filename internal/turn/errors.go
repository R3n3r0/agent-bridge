package turn

import (
	"fmt"

	"github.com/r3n3r0/agent-bridge/internal/redact"
)

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
