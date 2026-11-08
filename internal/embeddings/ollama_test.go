package embeddings

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeOllama imitates the parts of the Ollama API the manager uses.
type fakeOllama struct {
	mu      sync.Mutex
	models  map[string][]string // name -> capabilities
	shows   int
	release chan struct{} // closes to let a pull finish
}

func newFake(t *testing.T) (*fakeOllama, *OllamaManager) {
	t.Helper()
	f := &fakeOllama{models: map[string][]string{
		"nomic-embed-text:latest": {"embedding"},
		"llama3.2:latest":         {"completion"},
	}, release: make(chan struct{})}
	ts := httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(ts.Close)
	return f, NewOllamaManager(OllamaOptions{URL: ts.URL + "/"})
}

func (f *fakeOllama) serve(w http.ResponseWriter, r *http.Request) {
	var in map[string]any
	_ = json.NewDecoder(r.Body).Decode(&in)
	model, _ := in["model"].(string)
	f.mu.Lock()
	defer f.mu.Unlock()
	switch r.URL.Path {
	case "/api/version":
		fmt.Fprint(w, `{"version":"0.12.0"}`)
	case "/api/tags":
		var list []map[string]any
		for name := range f.models {
			list = append(list, map[string]any{"name": name, "digest": "d-" + name, "size": 274302450,
				"modified_at": time.Now(), "details": map[string]string{"family": "nomic-bert", "parameter_size": "137M"}})
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"models": list})
	case "/api/show":
		f.shows++
		_ = json.NewEncoder(w).Encode(map[string]any{"capabilities": f.models[model]})
	case "/api/delete":
		if _, ok := f.models[model]; !ok {
			w.WriteHeader(http.StatusNotFound)
			fmt.Fprintf(w, `{"error":"model '%s' not found"}`, model)
			return
		}
		delete(f.models, model)
	case "/api/embed":
		if caps := f.models[model]; len(caps) == 0 || caps[0] != "embedding" {
			w.WriteHeader(http.StatusBadRequest)
			fmt.Fprintf(w, `{"error":"%q does not support embeddings"}`, model)
			return
		}
		fmt.Fprint(w, `{"embeddings":[[0.1,0.2,0.3,0.4]]}`)
	case "/api/pull":
		if model == "missing" {
			fmt.Fprintln(w, `{"error":"pull model manifest: file does not exist"}`)
			return
		}
		fl := w.(http.Flusher)
		fmt.Fprintln(w, `{"status":"pulling manifest"}`)
		fmt.Fprintln(w, `{"status":"pulling 970aa74c","digest":"sha256:970a","total":1000,"completed":400}`)
		fl.Flush()
		f.mu.Unlock()
		select {
		case <-f.release:
		case <-r.Context().Done():
		}
		f.mu.Lock()
		f.models[model] = []string{"embedding"}
		fmt.Fprintln(w, `{"status":"success"}`)
	default:
		http.NotFound(w, r)
	}
}

func waitPull(t *testing.T, m *OllamaManager, model, state string) Pull {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		for _, p := range m.Status(context.Background()).Pulls {
			if p.Model == model && p.State == state {
				return p
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("%s never reached %s", model, state)
	return Pull{}
}

func TestStatusAndCapabilities(t *testing.T) {
	f, m := newFake(t)
	st := m.Status(context.Background())
	if !st.Enabled || !st.Reachable || st.Version != "0.12.0" || len(st.Models) != 2 || !strings.HasSuffix(st.ClientURL, "/v1") || strings.Contains(st.ClientURL, "//v1") {
		t.Fatalf("status %+v", st)
	}
	for _, md := range st.Models {
		want := md.Name == "nomic-embed-text:latest"
		if md.Embedding == nil || *md.Embedding != want || md.Family != "nomic-bert" {
			t.Errorf("model %+v", md)
		}
	}
	m.Status(context.Background())
	if f.shows != 2 {
		t.Errorf("capabilities not cached: %d show calls", f.shows)
	}
}

func TestUnreachable(t *testing.T) {
	m := NewOllamaManager(OllamaOptions{URL: "http://127.0.0.1:1", ClientURL: "http://host:11434/v1"})
	st := m.Status(context.Background())
	if st.Reachable || !strings.Contains(st.Error, "not reachable") || st.ClientURL != "http://host:11434/v1" {
		t.Fatalf("status %+v", st)
	}
	if _, err := m.Test(context.Background(), "x", "hi"); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("test err %v", err)
	}
}

func TestPullLifecycle(t *testing.T) {
	f, m := newFake(t)
	p, err := m.Pull("bge-m3")
	if err != nil || p.State != PullRunning {
		t.Fatalf("pull %+v %v", p, err)
	}
	if again, _ := m.Pull("bge-m3"); !again.StartedAt.Equal(p.StartedAt) {
		t.Fatal("second pull did not return the running one")
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		ps := m.Status(context.Background()).Pulls
		if len(ps) == 1 && ps[0].Completed == 400 && ps[0].Total == 1000 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("no progress: %+v", ps)
		}
		time.Sleep(20 * time.Millisecond)
	}
	close(f.release)
	waitPull(t, m, "bge-m3", PullSucceeded)
	if err := m.CancelPull("bge-m3"); !errors.Is(err, ErrNoPull) {
		t.Fatalf("cancel after end: %v", err)
	}
}

func TestPullCancelAndFailure(t *testing.T) {
	_, m := newFake(t)
	if _, err := m.Pull("all-minilm"); err != nil {
		t.Fatal(err)
	}
	if err := m.CancelPull("all-minilm"); err != nil {
		t.Fatal(err)
	}
	waitPull(t, m, "all-minilm", PullCanceled)
	if _, err := m.Pull("missing"); err != nil {
		t.Fatal(err)
	}
	if p := waitPull(t, m, "missing", PullFailed); !strings.Contains(p.Error, "does not exist") {
		t.Fatalf("failure %+v", p)
	}
	if _, err := m.Pull("bad name;rm"); !errors.Is(err, ErrBadModel) {
		t.Fatalf("bad name: %v", err)
	}
}

func TestDeleteAndTest(t *testing.T) {
	_, m := newFake(t)
	r, err := m.Test(context.Background(), "nomic-embed-text:latest", "ciao")
	if err != nil || r.Dimensions != 4 {
		t.Fatalf("test %+v %v", r, err)
	}
	if _, err := m.Test(context.Background(), "llama3.2:latest", "ciao"); !errors.Is(err, ErrRejected) || !strings.Contains(err.Error(), "does not support embeddings") {
		t.Fatalf("chat model: %v", err)
	}
	if err := m.Delete(context.Background(), "llama3.2:latest"); err != nil {
		t.Fatal(err)
	}
	if err := m.Delete(context.Background(), "llama3.2:latest"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("second delete: %v", err)
	}
	if err := m.Delete(context.Background(), "../x"); !errors.Is(err, ErrBadModel) {
		t.Fatalf("bad delete: %v", err)
	}
}

func TestValidModel(t *testing.T) {
	for name, want := range map[string]bool{
		"nomic-embed-text": true, "bge-m3:567m": true, "hf.co/user/repo:Q4_K_M": true,
		"": false, "-x": false, "a b": false, "x:": false, "x;y": false,
	} {
		if ValidModel(name) != want {
			t.Errorf("%q: want %v", name, want)
		}
	}
}
