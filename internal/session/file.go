package session

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// FileStore keeps entries in memory and rewrites a JSON file after every
// change. Writes are atomic: temporary file in the same directory, then
// rename.
type FileStore struct {
	t    *table
	path string
}

var _ Store = (*FileStore)(nil)

// NewFileStore loads path, if it exists, and returns a store that persists
// to it. Expired entries are dropped on load.
func NewFileStore(path string, p Policy) (*FileStore, error) {
	s := &FileStore{t: newTable(p), path: path}
	data, err := os.ReadFile(path)
	switch {
	case errors.Is(err, os.ErrNotExist):
	case err != nil:
		return nil, fmt.Errorf("sessions: %w", err)
	case len(data) > 0:
		var entries map[string]Entry
		if err := json.Unmarshal(data, &entries); err != nil {
			return nil, fmt.Errorf("sessions: %s: %w", path, err)
		}
		s.t.load(entries)
	}
	s.t.onChange = s.save
	return s, nil
}

// Get implements Store.
func (s *FileStore) Get(key string) (Entry, bool) { return s.t.get(key) }

// Put implements Store.
func (s *FileStore) Put(key string, e Entry) error { return s.t.put(key, e) }

// Delete implements Store.
func (s *FileStore) Delete(key string) error { return s.t.delete(key) }

// Len implements Store.
func (s *FileStore) Len() int { return s.t.len() }

func (s *FileStore) save(entries map[string]Entry) error {
	data, err := json.MarshalIndent(entries, "", "  ")
	if err != nil {
		return err
	}
	dir := filepath.Dir(s.path)
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
	if cerr := tmp.Close(); werr == nil {
		werr = cerr
	}
	if werr == nil {
		werr = os.Rename(name, s.path)
	}
	if werr != nil {
		os.Remove(name)
		return fmt.Errorf("sessions: %w", werr)
	}
	return nil
}
