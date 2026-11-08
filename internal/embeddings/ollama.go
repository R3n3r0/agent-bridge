package embeddings

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"
)

// requestTimeout bounds every Ollama call except downloads and tests.
const requestTimeout = 5 * time.Second

// OllamaOptions configures an OllamaManager.
type OllamaOptions struct {
	// URL is Ollama's address as the bridge reaches it, e.g.
	// http://ollama:11434.
	URL string
	// ClientURL is the base_url clients use; it defaults to URL + "/v1".
	ClientURL string
	// HTTPClient defaults to a client without timeout (downloads are long;
	// each call sets its own deadline).
	HTTPClient *http.Client
	Logger     *slog.Logger
}

// OllamaManager is a Manager backed by the Ollama HTTP API.
type OllamaManager struct {
	url       string
	clientURL string
	http      *http.Client
	log       *slog.Logger

	mu    sync.Mutex
	pulls map[string]*pullJob
	// caps caches the embedding capability by model digest.
	caps map[string]*bool
}

// NewOllamaManager returns a manager for the Ollama server at o.URL.
func NewOllamaManager(o OllamaOptions) *OllamaManager {
	m := &OllamaManager{
		url:       strings.TrimRight(o.URL, "/"),
		clientURL: o.ClientURL,
		http:      o.HTTPClient,
		log:       o.Logger,
		pulls:     map[string]*pullJob{},
		caps:      map[string]*bool{},
	}
	if m.clientURL == "" {
		m.clientURL = m.url + "/v1"
	}
	if m.http == nil {
		m.http = &http.Client{}
	}
	if m.log == nil {
		m.log = slog.Default()
	}
	return m
}

// Status implements Manager.
func (m *OllamaManager) Status(ctx context.Context) Status {
	st := Status{Enabled: true, ClientURL: m.clientURL, Models: []Model{}, Pulls: m.pullViews()}
	var v struct {
		Version string `json:"version"`
	}
	if err := m.call(ctx, requestTimeout, http.MethodGet, "/api/version", nil, &v); err != nil {
		st.Error = err.Error()
		return st
	}
	st.Reachable, st.Version = true, v.Version
	var tags struct {
		Models []struct {
			Name       string    `json:"name"`
			Size       int64     `json:"size"`
			Digest     string    `json:"digest"`
			ModifiedAt time.Time `json:"modified_at"`
			Details    struct {
				Family        string `json:"family"`
				ParameterSize string `json:"parameter_size"`
			} `json:"details"`
		} `json:"models"`
	}
	if err := m.call(ctx, requestTimeout, http.MethodGet, "/api/tags", nil, &tags); err != nil {
		st.Error = err.Error()
		return st
	}
	for _, t := range tags.Models {
		st.Models = append(st.Models, Model{
			Name: t.Name, Size: t.Size, ModifiedAt: t.ModifiedAt,
			Family: t.Details.Family, ParameterSize: t.Details.ParameterSize,
			Embedding: m.embedding(ctx, t.Name, t.Digest),
		})
	}
	return st
}

// embedding reports whether model produces embeddings, from the
// capabilities of /api/show (cached by digest).
func (m *OllamaManager) embedding(ctx context.Context, model, digest string) *bool {
	m.mu.Lock()
	v, ok := m.caps[digest]
	m.mu.Unlock()
	if ok {
		return v
	}
	var show struct {
		Capabilities []string `json:"capabilities"`
	}
	if err := m.call(ctx, requestTimeout, http.MethodPost, "/api/show", map[string]string{"model": model}, &show); err != nil {
		return nil // not cached: retried on the next status
	}
	if show.Capabilities != nil {
		yes := false
		for _, c := range show.Capabilities {
			yes = yes || c == "embedding"
		}
		v = &yes
	}
	m.mu.Lock()
	m.caps[digest] = v
	m.mu.Unlock()
	return v
}

// Delete implements Manager.
func (m *OllamaManager) Delete(ctx context.Context, model string) error {
	if !ValidModel(model) {
		return ErrBadModel
	}
	err := m.call(ctx, requestTimeout, http.MethodDelete, "/api/delete", map[string]string{"model": model}, nil)
	if err == nil {
		m.log.Info("embedding model deleted", "model", model)
	}
	return err
}

// Test implements Manager.
func (m *OllamaManager) Test(ctx context.Context, model, text string) (TestResult, error) {
	if !ValidModel(model) {
		return TestResult{}, ErrBadModel
	}
	start := time.Now()
	var out struct {
		Embeddings [][]float64 `json:"embeddings"`
	}
	// The first call loads the model into memory: allow for it.
	if err := m.call(ctx, 2*time.Minute, http.MethodPost, "/api/embed", map[string]string{"model": model, "input": text}, &out); err != nil {
		return TestResult{}, err
	}
	if len(out.Embeddings) == 0 {
		return TestResult{}, fmt.Errorf("%w: no embedding returned", ErrRejected)
	}
	return TestResult{Model: model, Dimensions: len(out.Embeddings[0]), Millis: time.Since(start).Milliseconds()}, nil
}

// call sends a JSON request to Ollama and decodes the JSON answer into
// out (when not nil). Connection failures are ErrUnavailable, 404 is
// ErrNotFound, other errors are ErrRejected with Ollama's message.
func (m *OllamaManager) call(ctx context.Context, timeout time.Duration, method, path string, in, out any) error {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	resp, err := m.send(ctx, method, path, in)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if out == nil {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<20))
		return nil
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 64<<20)).Decode(out); err != nil {
		return fmt.Errorf("%w: invalid answer: %v", ErrRejected, err)
	}
	return nil
}

// send performs a request and turns error statuses into errors; on
// success the caller closes the body.
func (m *OllamaManager) send(ctx context.Context, method, path string, in any) (*http.Response, error) {
	var body io.Reader
	if in != nil {
		b, err := json.Marshal(in)
		if err != nil {
			return nil, err
		}
		body = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, m.url+path, body)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := m.http.Do(req)
	if err != nil {
		if errors.Is(ctx.Err(), context.Canceled) {
			return nil, ctx.Err()
		}
		return nil, fmt.Errorf("%w at %s: %v", ErrUnavailable, m.url, err)
	}
	if resp.StatusCode < 300 {
		return resp, nil
	}
	defer resp.Body.Close()
	msg := ollamaError(resp.Body)
	if resp.StatusCode == http.StatusNotFound {
		return nil, fmt.Errorf("%w: %s", ErrNotFound, msg)
	}
	return nil, fmt.Errorf("%w: %s", ErrRejected, msg)
}

// ollamaError extracts {"error": "..."} from an error answer.
func ollamaError(r io.Reader) string {
	b, _ := io.ReadAll(io.LimitReader(r, 8<<10))
	var e struct {
		Error string `json:"error"`
	}
	if json.Unmarshal(b, &e) == nil && e.Error != "" {
		return e.Error
	}
	if s := strings.TrimSpace(string(b)); s != "" {
		return s
	}
	return "no details"
}
