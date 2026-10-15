package session

import (
	"context"
	"sync"
)

// KeyLocker is an in-process Locker with one mutex per key. Unused keys are
// removed, so memory does not grow with the number of sessions seen.
type KeyLocker struct {
	mu    sync.Mutex
	locks map[string]*keyLock
}

var _ Locker = (*KeyLocker)(nil)

type keyLock struct {
	ch   chan struct{}
	refs int
}

// NewKeyLocker creates a locker.
func NewKeyLocker() *KeyLocker { return &KeyLocker{locks: map[string]*keyLock{}} }

// Lock implements Locker.
func (l *KeyLocker) Lock(ctx context.Context, key string) (func(), error) {
	k := l.ref(key)
	select {
	case k.ch <- struct{}{}:
		return l.unlocker(key, k), nil
	case <-ctx.Done():
		l.unref(key, k)
		return nil, ctx.Err()
	}
}

// TryLock implements Locker.
func (l *KeyLocker) TryLock(key string) (func(), error) {
	k := l.ref(key)
	select {
	case k.ch <- struct{}{}:
		return l.unlocker(key, k), nil
	default:
		l.unref(key, k)
		return nil, ErrBusy
	}
}

func (l *KeyLocker) ref(key string) *keyLock {
	l.mu.Lock()
	defer l.mu.Unlock()
	k, ok := l.locks[key]
	if !ok {
		k = &keyLock{ch: make(chan struct{}, 1)}
		l.locks[key] = k
	}
	k.refs++
	return k
}

func (l *KeyLocker) unref(key string, k *keyLock) {
	l.mu.Lock()
	defer l.mu.Unlock()
	k.refs--
	if k.refs == 0 {
		delete(l.locks, key)
	}
}

func (l *KeyLocker) unlocker(key string, k *keyLock) func() {
	var once sync.Once
	return func() {
		once.Do(func() {
			<-k.ch
			l.unref(key, k)
		})
	}
}

func (l *KeyLocker) size() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.locks)
}
