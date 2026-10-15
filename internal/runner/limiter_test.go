package runner

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestLimiterQueueFull(t *testing.T) {
	l := NewFIFOLimiter(1, 1, 0)
	r1, err := l.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if l.Full() {
		t.Fatal("full with an empty queue")
	}
	got := make(chan error, 1)
	go func() {
		r, err := l.Acquire(context.Background())
		if err == nil {
			r()
		}
		got <- err
	}()
	waitFor(t, func() bool { return l.Queued() == 1 })
	if !l.Full() {
		t.Fatal("not full")
	}
	if _, err := l.Acquire(context.Background()); !errors.Is(err, ErrQueueFull) {
		t.Fatalf("got %v", err)
	}
	r1()
	r1() // idempotent
	if err := <-got; err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return l.Active() == 0 })
}

func TestLimiterFIFO(t *testing.T) {
	l := NewFIFOLimiter(1, 10, 0)
	release, _ := l.Acquire(context.Background())
	order := make(chan int, 5)
	for i := 0; i < 5; i++ {
		i := i
		go func() {
			r, err := l.Acquire(context.Background())
			if err != nil {
				t.Error(err)
				return
			}
			order <- i
			r()
		}()
		waitFor(t, func() bool { return l.Queued() == i+1 })
	}
	release()
	for i := 0; i < 5; i++ {
		if got := <-order; got != i {
			t.Fatalf("position %d served %d", i, got)
		}
	}
}

func TestLimiterTimeoutAndCancel(t *testing.T) {
	l := NewFIFOLimiter(1, 5, 20*time.Millisecond)
	release, _ := l.Acquire(context.Background())
	if _, err := l.Acquire(context.Background()); !errors.Is(err, ErrQueueTimeout) {
		t.Fatalf("got %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := l.Acquire(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("got %v", err)
	}
	if l.Queued() != 0 {
		t.Fatal("waiters not removed")
	}
	release()
	if l.Active() != 0 {
		t.Fatalf("active %d", l.Active())
	}
	if NewFIFOLimiter(0, 0, 0).max != 1 {
		t.Fatal("minimum concurrency")
	}
}

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatal("condition not met")
		}
		time.Sleep(time.Millisecond)
	}
}
