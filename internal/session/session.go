// Package session keeps the registry that maps (agent, client key) pairs to
// CLI session identifiers, and serialises turns on the same session.
//
// The registry stores identifiers and metadata only, never conversation
// content or credentials.
package session

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"sync"
	"time"
)

// ErrBusy is returned by TryLock when the session is in use.
var ErrBusy = errors.New("session is busy")

var keyRe = regexp.MustCompile(`^[a-zA-Z0-9._:-]{1,128}$`)

// ValidKey reports whether k is an acceptable client session key.
func ValidKey(k string) bool { return keyRe.MatchString(k) }

// NewKey returns a random server-generated session key.
func NewKey() string { return "sess_" + randomHex(12) }

func randomHex(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b)
}

// InternalKey combines agent and client key.
func InternalKey(agent, key string) string { return agent + "|" + key }

// Entry is one registry record.
type Entry struct {
	CLISessionID string    `json:"claude_session_id"`
	Agent        string    `json:"agent"`
	Cwd          string    `json:"cwd"`
	CreatedAt    time.Time `json:"created_at"`
	LastUsedAt   time.Time `json:"last_used_at"`
	Turns        int       `json:"turns"`
	// InstructionsHash fingerprints the first turn's system messages, so a
	// later change can be reported. It never contains the text itself.
	InstructionsHash string `json:"instructions_hash,omitempty"`
}

// Options configures a Registry.
type Options struct {
	// Path enables file persistence when not empty.
	Path       string
	TTL        time.Duration
	MaxEntries int
	// Now overrides the clock, for tests.
	Now func() time.Time
}

// Registry is safe for concurrent use.
type Registry struct {
	opts Options

	mu      sync.Mutex
	entries map[string]Entry

	lockMu sync.Mutex
	locks  map[string]*keyLock
}

type keyLock struct {
	ch   chan struct{}
	refs int
}

// New creates a registry. With a file path, existing entries are loaded; a
// missing file is not an error.
func New(opts Options) (*Registry, error) {
	if opts.Now == nil {
		opts.Now = time.Now
	}
	r := &Registry{opts: opts, entries: map[string]Entry{}, locks: map[string]*keyLock{}}
	if opts.Path != "" {
		data, err := os.ReadFile(opts.Path)
		switch {
		case errors.Is(err, os.ErrNotExist):
		case err != nil:
			return nil, fmt.Errorf("sessions: %w", err)
		case len(data) > 0:
			if err := json.Unmarshal(data, &r.entries); err != nil {
				return nil, fmt.Errorf("sessions: %s: %w", opts.Path, err)
			}
		}
		r.mu.Lock()
		r.pruneLocked()
		r.mu.Unlock()
	}
	return r, nil
}

func (r *Registry) expired(e Entry) bool {
	return r.opts.TTL > 0 && r.opts.Now().Sub(e.LastUsedAt) > r.opts.TTL
}

// Get returns the entry for key, if present and not expired. Expired entries
// are removed.
func (r *Registry) Get(key string) (Entry, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	e, ok := r.entries[key]
	if !ok {
		return Entry{}, false
	}
	if r.expired(e) {
		delete(r.entries, key)
		r.saveLocked()
		return Entry{}, false
	}
	return e, true
}

// Put stores e under key, evicting expired and least recently used entries
// as needed.
func (r *Registry) Put(key string, e Entry) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.entries[key] = e
	r.pruneLocked()
	return r.saveLocked()
}

// Delete removes key.
func (r *Registry) Delete(key string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.entries[key]; !ok {
		return nil
	}
	delete(r.entries, key)
	return r.saveLocked()
}

// Len returns the number of stored entries.
func (r *Registry) Len() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.entries)
}

func (r *Registry) pruneLocked() {
	for k, e := range r.entries {
		if r.expired(e) {
			delete(r.entries, k)
		}
	}
	if r.opts.MaxEntries <= 0 || len(r.entries) <= r.opts.MaxEntries {
		return
	}
	keys := make([]string, 0, len(r.entries))
	for k := range r.entries {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		return r.entries[keys[i]].LastUsedAt.Before(r.entries[keys[j]].LastUsedAt)
	})
	for _, k := range keys[:len(keys)-r.opts.MaxEntries] {
		delete(r.entries, k)
	}
}

// saveLocked writes the registry atomically: temporary file in the same
// directory, then rename.
func (r *Registry) saveLocked() error {
	if r.opts.Path == "" {
		return nil
	}
	data, err := json.MarshalIndent(r.entries, "", "  ")
	if err != nil {
		return err
	}
	dir := filepath.Dir(r.opts.Path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("sessions: %w", err)
	}
	tmp, err := os.CreateTemp(dir, ".sessions-*.tmp")
	if err != nil {
		return fmt.Errorf("sessions: %w", err)
	}
	name := tmp.Name()
	_, werr := tmp.Write(data)
	if werr == nil {
		werr = tmp.Sync()
	}
	cerr := tmp.Close()
	if werr == nil {
		werr = cerr
	}
	if werr == nil {
		werr = os.Rename(name, r.opts.Path)
	}
	if werr != nil {
		os.Remove(name)
		return fmt.Errorf("sessions: %w", werr)
	}
	return nil
}

func (r *Registry) acquireRef(key string) *keyLock {
	r.lockMu.Lock()
	defer r.lockMu.Unlock()
	l, ok := r.locks[key]
	if !ok {
		l = &keyLock{ch: make(chan struct{}, 1)}
		r.locks[key] = l
	}
	l.refs++
	return l
}

func (r *Registry) releaseRef(key string, l *keyLock) {
	r.lockMu.Lock()
	defer r.lockMu.Unlock()
	l.refs--
	if l.refs == 0 {
		delete(r.locks, key)
	}
}

// Lock waits until the session key is free or ctx is done. The returned
// function releases the lock.
func (r *Registry) Lock(ctx context.Context, key string) (func(), error) {
	l := r.acquireRef(key)
	select {
	case l.ch <- struct{}{}:
		return r.unlocker(key, l), nil
	case <-ctx.Done():
		r.releaseRef(key, l)
		return nil, ctx.Err()
	}
}

// TryLock acquires the session lock without waiting, or returns ErrBusy.
func (r *Registry) TryLock(key string) (func(), error) {
	l := r.acquireRef(key)
	select {
	case l.ch <- struct{}{}:
		return r.unlocker(key, l), nil
	default:
		r.releaseRef(key, l)
		return nil, ErrBusy
	}
}

func (r *Registry) unlocker(key string, l *keyLock) func() {
	var once sync.Once
	return func() {
		once.Do(func() {
			<-l.ch
			r.releaseRef(key, l)
		})
	}
}
