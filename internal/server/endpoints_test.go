package server

import (
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/r3n3r0/agent-bridge/internal/config"
	"github.com/r3n3r0/agent-bridge/internal/stats"
)

func TestModels(t *testing.T) {
	h := newHarness(t, harnessOpts{})
	list := decode[modelList](t, h.get("/v1/models", nil))
	var ids []string
	for _, m := range list.Data {
		ids = append(ids, m.ID)
		if m.Object != "model" || m.OwnedBy != "agent-bridge" {
			t.Fatalf("model %+v", m)
		}
	}
	want := "chat,coder,claude-code/haiku,claude-code/opus,claude-code/sonnet"
	if list.Object != "list" || strings.Join(ids, ",") != want {
		t.Fatalf("ids %v", ids)
	}
	m := decode[modelObject](t, h.get("/v1/models/claude-code/opus", nil))
	if m.ID != "claude-code/opus" || m.Description == "" {
		t.Fatalf("model %+v", m)
	}
	expectError(t, h.get("/v1/models/missing", nil), 404, errModelNotFound)
}

func TestAuth(t *testing.T) {
	h := newHarness(t, harnessOpts{apiKey: "s3cret", configJSON: `"metrics": true`})
	for _, path := range []string{"/v1/models", "/v1/models/chat", "/stats", "/metrics"} {
		expectError(t, h.get(path, nil), 401, errAuthentication)
		expectError(t, h.get(path, map[string]string{"Authorization": "Bearer wrong"}), 401, errAuthentication)
		expectError(t, h.get(path, map[string]string{"Authorization": "s3cret"}), 401, errAuthentication)
		resp := h.get(path, map[string]string{"Authorization": "Bearer s3cret"})
		if resp.StatusCode != 200 {
			t.Fatalf("%s: status %d", path, resp.StatusCode)
		}
		resp.Body.Close()
	}
	expectError(t, h.post(userMsg("chat", "q"), nil), 401, errAuthentication)
	resp := h.post(userMsg("chat", "q"), map[string]string{"Authorization": "Bearer s3cret"})
	if resp.StatusCode != 200 {
		t.Fatalf("status %d", resp.StatusCode)
	}
	resp.Body.Close()
	// Health is always open.
	resp = h.get("/healthz", nil)
	if resp.StatusCode != 200 {
		t.Fatalf("healthz %d", resp.StatusCode)
	}
	resp.Body.Close()
}

func TestHealthReady(t *testing.T) {
	h := newHarness(t, harnessOpts{})
	resp := h.get("/healthz", nil)
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 || string(body) != "ok\n" {
		t.Fatalf("healthz %d %q", resp.StatusCode, body)
	}
	resp = h.get("/readyz", nil)
	resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("readyz %d", resp.StatusCode)
	}
	h2 := newHarness(t, harnessOpts{mutate: func(c *config.Config) { c.Binary = filepath.Join(t.TempDir(), "missing") }})
	resp = h2.get("/readyz", nil)
	resp.Body.Close()
	if resp.StatusCode != 503 {
		t.Fatalf("readyz with missing binary %d", resp.StatusCode)
	}
	resp = h2.get("/metrics", nil)
	resp.Body.Close()
	if resp.StatusCode != 404 {
		t.Fatalf("metrics disabled but status %d", resp.StatusCode)
	}
}

func TestStatsAndUsageLog(t *testing.T) {
	usagePath := filepath.Join(t.TempDir(), "usage.jsonl")
	h := newHarness(t, harnessOpts{configJSON: `"usage_log": "` + filepath.ToSlash(usagePath) + `", "metrics": true`})
	for i := 0; i < 2; i++ {
		resp := h.post(userMsg("coder", "secret prompt text"), map[string]string{headerRequestID: "req-fixed"})
		resp.Body.Close()
	}
	snap := decode[stats.Snapshot](t, h.get("/stats", nil))
	a := snap.Agents["coder"]
	if snap.AuthMode != "subscription" || a == nil || a.Requests != 2 || a.InputTokens != 20 || a.OutputTokens != 10 ||
		a.CacheReadTokens != 6 || a.CacheCreationTokens != 4 || a.CostUSD < 0.0245 || a.AvgDurationMs != 42 {
		t.Fatalf("stats %+v %+v", snap, a)
	}

	resp := h.get("/metrics", nil)
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	for _, want := range []string{
		`agent_bridge_requests_total{agent="coder",outcome="success"} 2`,
		`agent_bridge_tokens_total{agent="coder",kind="input"} 20`,
		`agent_bridge_cost_usd_total{agent="coder"}`,
		"agent_bridge_turn_duration_seconds_count 2",
		"agent_bridge_active 0",
		"agent_bridge_queued 0",
	} {
		if !strings.Contains(string(body), want) {
			t.Fatalf("metrics missing %q:\n%s", want, body)
		}
	}

	snap = decode[stats.Snapshot](t, h.get("/stats?reset=true", nil))
	if snap.Agents["coder"].Requests != 2 {
		t.Fatal("reset must return counters first")
	}
	snap = decode[stats.Snapshot](t, h.get("/stats", nil))
	if len(snap.Agents) != 0 {
		t.Fatalf("not reset: %+v", snap.Agents)
	}

	data, err := os.ReadFile(usagePath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "secret prompt text") || strings.Contains(string(data), "Fake answer") {
		t.Fatal("usage log contains conversation text")
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) != 2 {
		t.Fatalf("lines %d", len(lines))
	}
	var turn stats.Turn
	if err := json.Unmarshal([]byte(lines[0]), &turn); err != nil {
		t.Fatal(err)
	}
	if turn.TS == "" || turn.AuthMode != "subscription" || turn.RequestID != "req-fixed" || turn.Agent != "coder" ||
		turn.Outcome != "success" || turn.CLISessionID == "" || turn.Model != "sonnet" || turn.Turns != 2 {
		t.Fatalf("usage record %+v", turn)
	}
}

func TestRequestIDHeader(t *testing.T) {
	h := newHarness(t, harnessOpts{})
	resp := h.get("/healthz", map[string]string{headerRequestID: "abc-123"})
	resp.Body.Close()
	if resp.Header.Get(headerRequestID) != "abc-123" {
		t.Fatalf("got %q", resp.Header.Get(headerRequestID))
	}
	resp = h.get("/healthz", map[string]string{headerRequestID: "bad id with spaces"})
	resp.Body.Close()
	if !strings.HasPrefix(resp.Header.Get(headerRequestID), "req_") {
		t.Fatalf("got %q", resp.Header.Get(headerRequestID))
	}
}

func TestCORS(t *testing.T) {
	h := newHarness(t, harnessOpts{configJSON: `"cors_origins": ["https://app.example"]`})
	req, _ := http.NewRequest(http.MethodOptions, h.ts.URL+"/v1/chat/completions", nil)
	req.Header.Set("Origin", "https://app.example")
	req.Header.Set("Access-Control-Request-Method", "POST")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 204 || resp.Header.Get("Access-Control-Allow-Origin") != "https://app.example" ||
		!strings.Contains(resp.Header.Get("Access-Control-Allow-Headers"), "Authorization") {
		t.Fatalf("preflight %d %v", resp.StatusCode, resp.Header)
	}
	resp = h.get("/v1/models", map[string]string{"Origin": "https://evil.example"})
	resp.Body.Close()
	if resp.Header.Get("Access-Control-Allow-Origin") != "" {
		t.Fatal("CORS allowed an unlisted origin")
	}
	resp = h.get("/v1/models", map[string]string{"Origin": "https://app.example"})
	resp.Body.Close()
	if resp.Header.Get("Access-Control-Allow-Origin") != "https://app.example" {
		t.Fatal("CORS header missing")
	}
	// Disabled by default.
	h2 := newHarness(t, harnessOpts{})
	resp = h2.get("/v1/models", map[string]string{"Origin": "https://app.example"})
	resp.Body.Close()
	if resp.Header.Get("Access-Control-Allow-Origin") != "" {
		t.Fatal("CORS enabled by default")
	}
}

func TestHardStopCancelsTurns(t *testing.T) {
	h := newHarness(t, harnessOpts{env: []string{"FAKE_SCENARIO=slow", "FAKE_SLEEP_MS=60000"}})
	done := make(chan *http.Response, 1)
	go func() { done <- h.post(userMsg("coder", "q"), nil) }()
	waitFor(t, func() bool { return len(h.starts()) == 1 })
	h.hard()
	resp := <-done
	e := expectError(t, resp, 502, errUpstream)
	if !strings.Contains(e.Message, "shutting down") {
		t.Fatalf("message %q", e.Message)
	}
}
