package runner

import (
	"context"
	"errors"
)

// Errors returned by Limiter.
var (
	ErrQueueFull    = errors.New("queue is full")
	ErrQueueTimeout = errors.New("timed out waiting in queue")
)

// Limiter bounds the number of turns running at once.
type Limiter interface {
	// Acquire takes a slot, waiting if none is free. It fails with
	// ErrQueueFull, ErrQueueTimeout or the context error. The returned
	// function releases the slot and may be called more than once.
	Acquire(ctx context.Context) (func(), error)
	// Full reports whether a new Acquire would be rejected immediately.
	Full() bool
	// Active returns the number of slots in use.
	Active() int
	// Queued returns the number of waiters.
	Queued() int
}
