package session

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type clock struct{ t time.Time }

func (c *clock) now() time.Time { return c.t }

func TestKeys(t *testing.T) {
	if !ValidKey("abc.DEF_1:2-3") || ValidKey("") || ValidKey("has space") || ValidKey(string(make([]byte, 129))) {
		t.Fatal("ValidKey")
	}
	k := NewKey()
	if len(k) != len("sess_")+24 || !ValidKey(k) || k == NewKey() {
		t.Fatalf("NewKey %q", k)
	}
	if InternalKey("a", "k") != "a|k" {
		t.Fatal("InternalKey")
	}
}

func TestMemoryGetPutDelete(t *testing.T) {
	r, err := NewStore(Options{})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := r.(*MemoryStore); !ok {
		t.Fatalf("NewStore without path returned %T", r)
	}
	if _, ok := r.Get("x"); ok {
		t.Fatal("unexpected entry")
	}
	e := Entry{CLISessionID: "s1", Agent: "a", LastUsedAt: time.Now()}
	if err := r.Put("x", e); err != nil {
		t.Fatal(err)
	}
	got, ok := r.Get("x")
	if !ok || got.CLISessionID != "s1" {
		t.Fatalf("got %+v", got)
	}
	if err := r.Delete("x"); err != nil {
		t.Fatal(err)
	}
	if err := r.Delete("x"); err != nil {
		t.Fatal(err)
	}
	if r.Len() != 0 {
		t.Fatal("not deleted")
	}
}

func TestTTL(t *testing.T) {
	c := &clock{t: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}
	r := NewMemoryStore(Policy{TTL: time.Hour, Now: c.now})
	_ = r.Put("old", Entry{LastUsedAt: c.t})
	c.t = c.t.Add(30 * time.Minute)
	if _, ok := r.Get("old"); !ok {
		t.Fatal("expired too early")
	}
	c.t = c.t.Add(31 * time.Minute)
	if _, ok := r.Get("old"); ok {
		t.Fatal("expired entry returned")
	}
	if r.Len() != 0 {
		t.Fatal("expired entry not removed")
	}
}

func TestLRU(t *testing.T) {
	base := time.Now()
	r := NewMemoryStore(Policy{MaxEntries: 2})
	_ = r.Put("a", Entry{LastUsedAt: base})
	_ = r.Put("b", Entry{LastUsedAt: base.Add(2 * time.Second)})
	_ = r.Put("a", Entry{LastUsedAt: base.Add(3 * time.Second)}) // a used again
	_ = r.Put("c", Entry{LastUsedAt: base.Add(4 * time.Second)})
	if _, ok := r.Get("b"); ok {
		t.Fatal("least recently used entry kept")
	}
	if _, ok := r.Get("a"); !ok {
		t.Fatal("recent entry evicted")
	}
	if r.Len() != 2 {
		t.Fatalf("len %d", r.Len())
	}
}

func TestFilePersistence(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sub", "sessions.json")
	r, err := NewStore(Options{Path: path})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := r.(*FileStore); !ok {
		t.Fatalf("NewStore with path returned %T", r)
	}
	now := time.Now().UTC().Truncate(time.Second)
	e := Entry{CLISessionID: "abc", Agent: "coder", Cwd: "/w", CreatedAt: now, LastUsedAt: now, Turns: 3}
	if err := r.Put("coder|k", e); err != nil {
		t.Fatal(err)
	}
	// Atomic write leaves no temporary files behind.
	files, _ := os.ReadDir(filepath.Dir(path))
	if len(files) != 1 || files[0].Name() != "sessions.json" {
		t.Fatalf("unexpected files: %v", files)
	}
	r2, err := NewFileStore(path, Policy{})
	if err != nil {
		t.Fatal(err)
	}
	got, ok := r2.Get("coder|k")
	if !ok || got != e {
		t.Fatalf("reloaded %+v, want %+v", got, e)
	}
	_ = r2.Delete("coder|k")
	r3, _ := NewFileStore(path, Policy{})
	if r3.Len() != 0 {
		t.Fatal("delete not persisted")
	}
}

func TestFileLoadPrunesAndErrors(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "s.json")
	old := `{"a|1":{"cli_session_id":"x","last_used_at":"2000-01-01T00:00:00Z"}}`
	if err := os.WriteFile(path, []byte(old), 0o600); err != nil {
		t.Fatal(err)
	}
	r, err := NewFileStore(path, Policy{TTL: time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	if r.Len() != 0 {
		t.Fatal("expired entry loaded")
	}
	if err := os.WriteFile(path, []byte("{broken"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := NewFileStore(path, Policy{}); err == nil {
		t.Fatal("corrupt file accepted")
	}
	if err := os.WriteFile(path, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := NewFileStore(path, Policy{}); err != nil {
		t.Fatalf("empty file: %v", err)
	}
	if _, err := NewFileStore(dir, Policy{}); err == nil {
		t.Fatal("directory accepted as file")
	}
}

func TestSaveError(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sessions.json")
	r, err := NewFileStore(path, Policy{})
	if err != nil {
		t.Fatal(err)
	}
	// A non-empty directory in place of the file makes the rename fail.
	if err := os.MkdirAll(filepath.Join(path, "x"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := r.Put("k", Entry{LastUsedAt: time.Now()}); err == nil {
		t.Fatal("rename onto a directory succeeded")
	}
	files, _ := os.ReadDir(filepath.Dir(path))
	if len(files) != 1 {
		t.Fatalf("temporary file left behind: %v", files)
	}
}

func TestLockSerialises(t *testing.T) {
	r := NewKeyLocker()
	var running, maxRunning int32
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			unlock, err := r.Lock(context.Background(), "k")
			if err != nil {
				t.Error(err)
				return
			}
			n := atomic.AddInt32(&running, 1)
			for {
				m := atomic.LoadInt32(&maxRunning)
				if n <= m || atomic.CompareAndSwapInt32(&maxRunning, m, n) {
					break
				}
			}
			time.Sleep(2 * time.Millisecond)
			atomic.AddInt32(&running, -1)
			unlock()
			unlock() // idempotent
		}()
	}
	wg.Wait()
	if maxRunning != 1 {
		t.Fatalf("max concurrent holders %d", maxRunning)
	}
	if r.size() != 0 {
		t.Fatal("lock map not cleaned up")
	}
}

func TestLockCancelAndTryLock(t *testing.T) {
	var r Locker = NewKeyLocker()
	unlock, err := r.TryLock("k")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.TryLock("k"); !errors.Is(err, ErrBusy) {
		t.Fatalf("got %v", err)
	}
	if u, err := r.TryLock("other"); err != nil {
		t.Fatal(err)
	} else {
		u()
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if _, err := r.Lock(ctx, "k"); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("got %v", err)
	}
	unlock()
	u2, err := r.Lock(context.Background(), "k")
	if err != nil {
		t.Fatal(err)
	}
	u2()
}
