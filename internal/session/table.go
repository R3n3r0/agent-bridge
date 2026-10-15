package session

import (
	"sort"
	"sync"
	"time"
)

// table is the in-memory core shared by the Store implementations: entries,
// expiry and eviction. onChange, when set, runs after every mutation while
// the lock is held, so persisted snapshots are written in order.
type table struct {
	policy   Policy
	onChange func(map[string]Entry) error

	mu      sync.Mutex
	entries map[string]Entry
}

func newTable(p Policy) *table {
	if p.Now == nil {
		p.Now = time.Now
	}
	return &table{policy: p, entries: map[string]Entry{}}
}

func (t *table) expired(e Entry) bool {
	return t.policy.TTL > 0 && t.policy.Now().Sub(e.LastUsedAt) > t.policy.TTL
}

func (t *table) get(key string) (Entry, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	e, ok := t.entries[key]
	if !ok {
		return Entry{}, false
	}
	if t.expired(e) {
		delete(t.entries, key)
		_ = t.changedLocked()
		return Entry{}, false
	}
	return e, true
}

func (t *table) put(key string, e Entry) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.entries[key] = e
	t.pruneLocked()
	return t.changedLocked()
}

func (t *table) delete(key string) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if _, ok := t.entries[key]; !ok {
		return nil
	}
	delete(t.entries, key)
	return t.changedLocked()
}

func (t *table) len() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return len(t.entries)
}

// load replaces the entries and prunes them, without triggering onChange.
func (t *table) load(entries map[string]Entry) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if entries == nil {
		entries = map[string]Entry{}
	}
	t.entries = entries
	t.pruneLocked()
}

func (t *table) changedLocked() error {
	if t.onChange == nil {
		return nil
	}
	return t.onChange(t.entries)
}

func (t *table) pruneLocked() {
	for k, e := range t.entries {
		if t.expired(e) {
			delete(t.entries, k)
		}
	}
	max := t.policy.MaxEntries
	if max <= 0 || len(t.entries) <= max {
		return
	}
	keys := make([]string, 0, len(t.entries))
	for k := range t.entries {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		return t.entries[keys[i]].LastUsedAt.Before(t.entries[keys[j]].LastUsedAt)
	})
	for _, k := range keys[:len(keys)-max] {
		delete(t.entries, k)
	}
}
