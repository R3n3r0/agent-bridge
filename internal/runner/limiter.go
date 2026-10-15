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

// Limiter is a counting semaphore with a bounded FIFO wait queue.
type Limiter struct {
	mu       sync.Mutex
	max      int
	maxQueue int
	timeout  time.Duration
	active   int
	waiters  *list.List // of chan struct{}
}

// NewLimiter allows max concurrent holders and up to maxQueue waiters, each
// waiting at most timeout (0 = no limit).
func NewLimiter(max, maxQueue int, timeout time.Duration) *Limiter {
	if max < 1 {
		max = 1
	}
	return &Limiter{max: max, maxQueue: maxQueue, timeout: timeout, waiters: list.New()}
}

// Full reports whether a new Acquire would be rejected immediately.
func (l *Limiter) Full() bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.active >= l.max && l.waiters.Len() >= l.maxQueue
}

// Active returns the number of slots in use.
func (l *Limiter) Active() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.active
}

// Queued returns the number of waiters.
func (l *Limiter) Queued() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.waiters.Len()
}

// Acquire takes a slot, waiting in FIFO order if none is free. The returned
// function releases the slot and must be called exactly once.
func (l *Limiter) Acquire(ctx context.Context) (func(), error) {
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

func (l *Limiter) releaser() func() {
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
func (l *Limiter) handOffLocked() {
	if front := l.waiters.Front(); front != nil {
		l.waiters.Remove(front)
		close(front.Value.(chan struct{}))
		return
	}
	l.active--
}
