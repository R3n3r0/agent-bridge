// Package session maps (agent, client key) pairs to CLI session identifiers
// and serialises turns on the same session.
//
// Two abstractions are exposed:
//
//   - Store persists entries. MemoryStore keeps them in memory, FileStore
//     also writes them atomically to a JSON file.
//   - Locker gives exclusive access to a key. KeyLocker is the in-process
//     implementation.
//
// Stores keep identifiers and metadata only, never conversation content or
// credentials.
package session

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"regexp"
	"time"
)

// Entry is one stored session.
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

// Store persists session entries. Implementations must be safe for
// concurrent use and must drop entries that expired according to their
// policy.
type Store interface {
	// Get returns the entry for key, if present and not expired.
	Get(key string) (Entry, bool)
	// Put stores e under key, evicting entries beyond the capacity.
	Put(key string, e Entry) error
	// Delete removes key; deleting a missing key is not an error.
	Delete(key string) error
	// Len returns the number of stored entries.
	Len() int
}

// Locker serialises work on the same key.
type Locker interface {
	// Lock waits until key is free or ctx is done. The returned function
	// releases the lock and may be called more than once.
	Lock(ctx context.Context, key string) (func(), error)
	// TryLock acquires key without waiting, or returns ErrBusy.
	TryLock(key string) (func(), error)
}

// ErrBusy is returned by TryLock when the key is in use.
var ErrBusy = errors.New("session is busy")

// Policy controls expiry and capacity of a Store.
type Policy struct {
	// TTL removes entries unused for longer; 0 disables expiry.
	TTL time.Duration
	// MaxEntries evicts the least recently used entries beyond the limit;
	// 0 disables the limit.
	MaxEntries int
	// Now overrides the clock, for tests.
	Now func() time.Time
}

// Options selects and configures a Store implementation.
type Options struct {
	Policy
	// Path selects FileStore when not empty, MemoryStore otherwise.
	Path string
}

// NewStore returns a FileStore when opts.Path is set, a MemoryStore
// otherwise.
func NewStore(opts Options) (Store, error) {
	if opts.Path != "" {
		return NewFileStore(opts.Path, opts.Policy)
	}
	return NewMemoryStore(opts.Policy), nil
}

var keyRe = regexp.MustCompile(`^[a-zA-Z0-9._:-]{1,128}$`)

// ValidKey reports whether k is an acceptable client session key.
func ValidKey(k string) bool { return keyRe.MatchString(k) }

// NewKey returns a random server-generated session key.
func NewKey() string {
	b := make([]byte, 12)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return "sess_" + hex.EncodeToString(b)
}

// InternalKey combines agent and client key.
func InternalKey(agent, key string) string { return agent + "|" + key }
