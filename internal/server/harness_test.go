package server

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/r3n3r0/agent-bridge/internal/config"
	"github.com/r3n3r0/agent-bridge/internal/runner"
	"github.com/r3n3r0/agent-bridge/internal/session"
	"github.com/r3n3r0/agent-bridge/internal/stats"
	"github.com/r3n3r0/agent-bridge/internal/testutil"
)

var fakeBin string

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "server-test-")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fakeBin, err = testutil.BuildFakeCLI(dir)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}

type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

type harness struct {
	t       *testing.T
	cfg     *config.Config
	srv     *Server
	ts      *httptest.Server
	reg     *session.Registry
	stats   *stats.Stats
	limiter *runner.Limiter
	logs    *syncBuffer
	record  string
	dir     string
	hard    context.CancelFunc
}

type harnessOpts struct {
	configJSON string                  // extra top-level keys, merged into the base config
	mutate     func(*config.Config)    // applied after loading
	env        []string                // extra child environment (FAKE_*)
	keepalive  time.Duration           // SSE keepalive interval
	apiKey     string                  // client API key
	authMode   config.AuthMode         // default subscription
	sessions   *session.Options        // registry options
	extra      func(dir string) string // returns extra agents JSON (without braces), given the temp dir
}

func newHarness(t *testing.T, o harnessOpts) *harness {
	t.Helper()
	dir := t.TempDir()
	work := filepath.Join(dir, "work")
	if err := os.MkdirAll(work, 0o755); err != nil {
		t.Fatal(err)
	}
	agents := `"coder": {"cwd": "work", "allowed_tools": ["Read", "Edit", "Bash"], "system_prompt": "agent prompt"},
		"chat": {"tools_enabled": false, "model": "haiku"}`
	if o.extra != nil {
		agents += ", " + o.extra(dir)
	}
	body := `{"agents": {` + agents + `}`
	if o.configJSON != "" {
		body += ", " + o.configJSON
	}
	body += "}"
	cfgPath := filepath.Join(dir, "agents.json")
	if err := os.WriteFile(cfgPath, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	fs := flag.NewFlagSet("t", flag.ContinueOnError)
	fl := config.RegisterFlags(fs)
	_ = fs.Parse([]string{"-config", cfgPath, "-claude-bin", fakeBin})
	fl.MarkSet(fs)
	mode := o.authMode
	if mode == "" {
		mode = config.AuthSubscription
	}
	cfg, err := config.Load(fl, func(k string) string {
		switch k {
		case config.EnvAuthMode:
			return string(mode)
		case config.EnvAPIKey:
			return o.apiKey
		}
		return ""
	})
	if err != nil {
		t.Fatal(err)
	}
	scratch := filepath.Join(dir, "scratch")
	_ = os.Mkdir(scratch, 0o755)
	cfg.SetScratchDir(scratch)
	if o.mutate != nil {
		o.mutate(cfg)
	}

	regOpts := session.Options{}
	if o.sessions != nil {
		regOpts = *o.sessions
	}
	reg, err := session.New(regOpts)
	if err != nil {
		t.Fatal(err)
	}
	limiter := runner.NewLimiter(cfg.MaxConcurrent, cfg.MaxQueue, time.Duration(cfg.QueueTimeoutSeconds)*time.Second)
	st, err := stats.New(string(cfg.AuthMode), limiter, cfg.UsageLog)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })

	record := filepath.Join(dir, "record.jsonl")
	env := append(os.Environ(), "FAKE_RECORD="+record, "AGENT_BRIDGE_SHOULD_NOT_LEAK=1", "ANTHROPIC_API_KEY=sk-ant-should-not-leak")
	env = append(env, o.env...)
	logs := &syncBuffer{}
	log := slog.New(slog.NewTextHandler(logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
	hardCtx, hardCancel := context.WithCancel(context.Background())
	t.Cleanup(hardCancel)

	srv := New(Options{
		Config:            cfg,
		Registry:          reg,
		Limiter:           limiter,
		Stats:             st,
		Logger:            log,
		BaseEnv:           env,
		KeepaliveInterval: o.keepalive,
		HardStop:          hardCtx,
	})
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	return &harness{t: t, cfg: cfg, srv: srv, ts: ts, reg: reg, stats: st, limiter: limiter, logs: logs, record: record, dir: dir, hard: hardCancel}
}

func (h *harness) post(body any, headers map[string]string) *http.Response {
	h.t.Helper()
	var r io.Reader
	switch b := body.(type) {
	case string:
		r = strings.NewReader(b)
	default:
		data, _ := json.Marshal(b)
		r = bytes.NewReader(data)
	}
	req, _ := http.NewRequest(http.MethodPost, h.ts.URL+"/v1/chat/completions", r)
	req.Header.Set("Content-Type", "application/json")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		h.t.Fatal(err)
	}
	return resp
}

func (h *harness) get(path string, headers map[string]string) *http.Response {
	h.t.Helper()
	req, _ := http.NewRequest(http.MethodGet, h.ts.URL+path, nil)
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		h.t.Fatal(err)
	}
	return resp
}

type fakeRecord struct {
	Phase string    `json:"phase"`
	PID   int       `json:"pid"`
	Time  time.Time `json:"time"`
	Args  []string  `json:"args"`
	Env   []string  `json:"env"`
	Cwd   string    `json:"cwd"`
	Stdin string    `json:"stdin"`
}

func (h *harness) records() []fakeRecord {
	h.t.Helper()
	f, err := os.Open(h.record)
	if err != nil {
		return nil
	}
	defer f.Close()
	var out []fakeRecord
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 64<<20)
	for sc.Scan() {
		var r fakeRecord
		if err := json.Unmarshal(sc.Bytes(), &r); err != nil {
			h.t.Fatal(err)
		}
		out = append(out, r)
	}
	return out
}

func (h *harness) starts() []fakeRecord {
	var out []fakeRecord
	for _, r := range h.records() {
		if r.Phase == "start" {
			out = append(out, r)
		}
	}
	return out
}

func decode[T any](t *testing.T, resp *http.Response) T {
	t.Helper()
	defer resp.Body.Close()
	var v T
	data, _ := io.ReadAll(resp.Body)
	if err := json.Unmarshal(data, &v); err != nil {
		t.Fatalf("decode %s: %v", data, err)
	}
	return v
}

func expectError(t *testing.T, resp *http.Response, status int, code string) errorObject {
	t.Helper()
	if resp.StatusCode != status {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("status %d, want %d: %s", resp.StatusCode, status, body)
	}
	e := decode[errorBody](t, resp)
	if e.Error.Code != code || e.Error.Type != code {
		t.Fatalf("error %+v, want code %s", e.Error, code)
	}
	return e.Error
}

func userMsg(model, text string) map[string]any {
	return map[string]any{"model": model, "messages": []map[string]any{{"role": "user", "content": text}}}
}

func argValue(args []string, name string) (string, bool) {
	for i := 0; i+1 < len(args); i++ {
		if args[i] == name {
			return args[i+1], true
		}
	}
	return "", false
}

func sessionEntry(id, agent, cwd string) session.Entry {
	now := time.Now().UTC()
	return session.Entry{CLISessionID: id, Agent: agent, Cwd: cwd, CreatedAt: now, LastUsedAt: now}
}
