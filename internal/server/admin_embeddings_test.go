package server

import (
	"context"
	"fmt"
	"io"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/r3n3r0/agent-bridge/internal/config"
	"github.com/r3n3r0/agent-bridge/internal/embeddings"
)

// fakeEmbeddings records the calls of the embedding models API.
type fakeEmbeddings struct {
	calls []string
	err   error
}

func (f *fakeEmbeddings) Status(context.Context) embeddings.Status {
	return embeddings.Status{Enabled: true, Reachable: true, ClientURL: "http://127.0.0.1:11434/v1",
		Models: []embeddings.Model{{Name: "bge-m3:latest"}}, Pulls: []embeddings.Pull{}}
}

func (f *fakeEmbeddings) Pull(model string) (embeddings.Pull, error) {
	f.calls = append(f.calls, "pull "+model)
	return embeddings.Pull{Model: model, State: embeddings.PullRunning}, f.err
}

func (f *fakeEmbeddings) CancelPull(model string) error {
	f.calls = append(f.calls, "cancel "+model)
	return f.err
}

func (f *fakeEmbeddings) Delete(_ context.Context, model string) error {
	f.calls = append(f.calls, "delete "+model)
	return f.err
}

func (f *fakeEmbeddings) Test(_ context.Context, model, text string) (embeddings.TestResult, error) {
	f.calls = append(f.calls, "test "+model+" "+text)
	return embeddings.TestResult{Model: model, Dimensions: 1024}, f.err
}

func embeddingServer(t *testing.T, m embeddings.Manager) *httptest.Server {
	t.Helper()
	cfg := &config.Config{APIKey: "k", Backend: config.BackendCodex}
	ts := httptest.NewServer(New(Options{Config: cfg, Logins: &fakeLogins{}, Embeddings: m, Ready: func() error { return nil }}).Handler())
	t.Cleanup(ts.Close)
	return ts
}

func TestAdminEmbeddingsAPI(t *testing.T) {
	f := &fakeEmbeddings{}
	ts := embeddingServer(t, f)
	auth := []string{"Authorization", "Bearer k", headerAdmin, "1"}

	if code, _ := adminDo(t, ts, "GET", "/admin/api/embeddings", ""); code != 401 {
		t.Fatalf("status without key: %d", code)
	}
	code, body := adminDo(t, ts, "GET", "/admin/api/embeddings", "", auth...)
	if code != 200 || !strings.Contains(body, `"enabled":true`) || !strings.Contains(body, "bge-m3:latest") {
		t.Fatalf("status %d %s", code, body)
	}
	if code, _ := adminDo(t, ts, "POST", "/admin/api/embeddings/pulls", `{"model":"bge-m3"}`, "Authorization", "Bearer k"); code != 403 {
		t.Fatalf("missing admin header: %d", code)
	}
	steps := []struct {
		method, path, body string
		want               int
	}{
		{"POST", "/admin/api/embeddings/pulls", `{"model":"bge-m3"}`, 200},
		{"POST", "/admin/api/embeddings/pulls", `nope`, 400},
		{"DELETE", "/admin/api/embeddings/pulls?model=bge-m3", "", 204},
		{"DELETE", "/admin/api/embeddings/models?model=hf.co%2Fx%2Fy%3Aq4", "", 204},
		{"POST", "/admin/api/embeddings/test", `{"model":"bge-m3","text":"ciao"}`, 200},
		{"POST", "/admin/api/embeddings/test", `{"model":"bge-m3"}`, 200},
	}
	for _, s := range steps {
		if code, body := adminDo(t, ts, s.method, s.path, s.body, auth...); code != s.want {
			t.Errorf("%s %s: %d %s, want %d", s.method, s.path, code, body, s.want)
		}
	}
	want := "pull bge-m3|cancel bge-m3|delete hf.co/x/y:q4|test bge-m3 ciao|test bge-m3 agent-bridge embedding test"
	if got := strings.Join(f.calls, "|"); got != want {
		t.Fatalf("calls %q", got)
	}
	for err, code := range map[error]int{embeddings.ErrBadModel: 400, embeddings.ErrRejected: 400, embeddings.ErrNotFound: 404,
		embeddings.ErrNoPull: 409, embeddings.ErrUnavailable: 502, io.EOF: 500} {
		f.err = fmt.Errorf("wrapped: %w", err)
		if got, _ := adminDo(t, ts, "DELETE", "/admin/api/embeddings/models?model=x", "", auth...); got != code {
			t.Errorf("%v -> %d, want %d", err, got, code)
		}
	}
}

func TestAdminEmbeddingsDisabled(t *testing.T) {
	ts := embeddingServer(t, nil)
	auth := []string{"Authorization", "Bearer k", headerAdmin, "1"}
	code, body := adminDo(t, ts, "GET", "/admin/api/embeddings", "", auth...)
	if code != 200 || !strings.Contains(body, `"enabled":false`) || !strings.Contains(body, `"models":[]`) {
		t.Fatalf("status %d %s", code, body)
	}
	if code, _ := adminDo(t, ts, "POST", "/admin/api/embeddings/pulls", `{"model":"x"}`, auth...); code != 404 {
		t.Fatalf("pull when disabled: %d", code)
	}
}
