package runner

import (
	"container/list"
	"context"
	"errors"
	"sync"
	"time"
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

// FIFOLimiter is a counting semaphore with a bounded FIFO wait queue.
type FIFOLimiter struct {
	mu       sync.Mutex
	max      int
	maxQueue int
	timeout  time.Duration
	active   int
	waiters  *list.List // of chan struct{}
}

var _ Limiter = (*FIFOLimiter)(nil)

// NewFIFOLimiter allows max concurrent holders and up to maxQueue waiters,
// each waiting at most timeout (0 = no limit).
func NewFIFOLimiter(max, maxQueue int, timeout time.Duration) *FIFOLimiter {
	if max < 1 {
		max = 1
	}
	return &FIFOLimiter{max: max, maxQueue: maxQueue, timeout: timeout, waiters: list.New()}
}

// Full implements Limiter.
func (l *FIFOLimiter) Full() bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.active >= l.max && l.waiters.Len() >= l.maxQueue
}

// Active implements Limiter.
func (l *FIFOLimiter) Active() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.active
}

// Queued implements Limiter.
func (l *FIFOLimiter) Queued() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.waiters.Len()
}

// Acquire implements Limiter; waiters are served in arrival order.
func (l *FIFOLimiter) Acquire(ctx context.Context) (func(), error) {
	l.mu.Lock()
	if l.active < l.max && l.waiters.Len() == 0 {
		l.active++
		l.mu.Unlock()
		return l.releaser(), nil
	}
	if l.waiters.Len() >= l.maxQueue {
		l.mu.Unlock()
		return nil, ErrQueueFull
	}
	ch := make(chan struct{})
	elem := l.waiters.PushBack(ch)
	l.mu.Unlock()

	var timeout <-chan time.Time
	if l.timeout > 0 {
		t := time.NewTimer(l.timeout)
		defer t.Stop()
		timeout = t.C
	}
	var err error
	select {
	case <-ch:
		return l.releaser(), nil
	case <-ctx.Done():
		err = ctx.Err()
	case <-timeout:
		err = ErrQueueTimeout
	}

	l.mu.Lock()
	defer l.mu.Unlock()
	select {
	case <-ch:
		// The slot was handed over while giving up: pass it on.
		l.handOffLocked()
	default:
		l.waiters.Remove(elem)
	}
	return nil, err
}

func (l *FIFOLimiter) releaser() func() {
	var once sync.Once
	return func() {
		once.Do(func() {
			l.mu.Lock()
			defer l.mu.Unlock()
			l.handOffLocked()
		})
	}
}

// handOffLocked gives the caller's slot to the first waiter, or frees it.
func (l *FIFOLimiter) handOffLocked() {
	if front := l.waiters.Front(); front != nil {
		l.waiters.Remove(front)
		close(front.Value.(chan struct{}))
		return
	}
	l.active--
}
