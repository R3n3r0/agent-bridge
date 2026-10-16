package session

// MemoryStore keeps entries in memory only; they are lost on restart.
type MemoryStore struct {
	t *table
}

var _ Store = (*MemoryStore)(nil)

// NewMemoryStore creates an empty in-memory store.
func NewMemoryStore(p Policy) *MemoryStore { return &MemoryStore{t: newTable(p)} }

// Get implements Store.
func (s *MemoryStore) Get(key string) (Entry, bool) { return s.t.get(key) }

// Put implements Store.
func (s *MemoryStore) Put(key string, e Entry) error { return s.t.put(key, e) }

// Delete implements Store.
func (s *MemoryStore) Delete(key string) error { return s.t.delete(key) }

// Len implements Store.
func (s *MemoryStore) Len() int { return s.t.len() }
