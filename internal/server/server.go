// Package server implements the OpenAI-compatible HTTP API and the service
// endpoints.
package server

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"log/slog"
	"net/http"
	"os/exec"
	"regexp"
	"strings"
	"time"

	"github.com/r3n3r0/agent-bridge/internal/config"
	"github.com/r3n3r0/agent-bridge/internal/stats"
	"github.com/r3n3r0/agent-bridge/internal/turn"
)

const (
	maxBodyBytes             = 10 << 20
	defaultKeepaliveInterval = 15 * time.Second
	headerRequestID          = "X-Request-Id"
	headerSessionID          = "X-Session-Id"
	headerSessionReset       = "X-Session-Reset"
	headerAgentCwd           = "X-Agent-Cwd"
	headerAgentTools         = "X-Agent-Allowed-Tools"
	headerAgentPrompt        = "X-Agent-System-Prompt"
	headerToolMode           = "X-Agent-Bridge-Tool-Mode"
	headerSystemMode         = "X-Agent-Bridge-System-Mode"
	headerTurns              = "X-Agent-Bridge-Turns"
	headerCost               = "X-Agent-Bridge-Cost-USD"
	headerDuration           = "X-Agent-Bridge-Duration-Ms"
)

var requestIDRe = regexp.MustCompile(`^[a-zA-Z0-9._:-]{1,128}$`)

// Options wires the server dependencies. The server only knows interfaces:
// turn execution and statistics can be replaced independently.
type Options struct {
	Config *config.Config
	Turns  turn.Service
	Stats  stats.Reporter
	Logger *slog.Logger
	// KeepaliveInterval defaults to 15 seconds.
	KeepaliveInterval time.Duration
	// Ready reports whether the server can execute turns; nil means the
	// CLI binary must be found in PATH.
	Ready func() error
}

// Server serves the bridge API.
type Server struct {
	cfg       *config.Config
	turns     turn.Service
	stats     stats.Reporter
	log       *slog.Logger
	keepalive time.Duration
	ready     func() error
	keyHash   [32]byte
}

// New creates a server.
func New(o Options) *Server {
	s := &Server{
		cfg:       o.Config,
		turns:     o.Turns,
		stats:     o.Stats,
		log:       o.Logger,
		keepalive: o.KeepaliveInterval,
		ready:     o.Ready,
	}
	if s.log == nil {
		s.log = slog.Default()
	}
	if s.keepalive <= 0 {
		s.keepalive = defaultKeepaliveInterval
	}
	if s.ready == nil {
		s.ready = func() error {
			_, err := exec.LookPath(s.cfg.Binary)
			return err
		}
	}
	if s.cfg.APIKey != "" {
		s.keyHash = sha256.Sum256([]byte(s.cfg.APIKey))
	}
	return s
}

// Handler returns the HTTP handler with every route and middleware.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = w.Write([]byte("ok\n"))
	})
	mux.HandleFunc("GET /readyz", s.handleReady)
	mux.Handle("POST /v1/chat/completions", s.auth(http.HandlerFunc(s.handleChat)))
	mux.Handle("GET /v1/models", s.auth(http.HandlerFunc(s.handleModels)))
	mux.Handle("GET /v1/models/{id...}", s.auth(http.HandlerFunc(s.handleModel)))
	mux.Handle("GET /stats", s.auth(http.HandlerFunc(s.handleStats)))
	if s.cfg.Metrics {
		mux.Handle("GET /metrics", s.auth(http.HandlerFunc(s.handleMetrics)))
	}
	return s.requestID(s.cors(mux))
}

type ctxKey int

const requestIDKey ctxKey = 0

func requestIDFrom(ctx context.Context) string {
	id, _ := ctx.Value(requestIDKey).(string)
	return id
}

func (s *Server) requestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := r.Header.Get(headerRequestID)
		if !requestIDRe.MatchString(id) {
			id = "req_" + randomHex(12)
		}
		w.Header().Set(headerRequestID, id)
		ctx := context.WithValue(r.Context(), requestIDKey, id)
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		start := time.Now()
		next.ServeHTTP(rec, r.WithContext(ctx))
		s.log.Debug("http request", "request_id", id, "method", r.Method, "path", r.URL.Path,
			"status", rec.status, "duration_ms", time.Since(start).Milliseconds())
	})
}

func (s *Server) cors(next http.Handler) http.Handler {
	allowed := map[string]bool{}
	for _, o := range s.cfg.CORSOrigins {
		allowed[o] = true
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		if origin == "" || !allowed[origin] {
			next.ServeHTTP(w, r)
			return
		}
		h := w.Header()
		h.Set("Access-Control-Allow-Origin", origin)
		h.Add("Vary", "Origin")
		h.Set("Access-Control-Expose-Headers", strings.Join([]string{
			headerRequestID, headerSessionID, headerTurns, headerCost, headerDuration,
		}, ", "))
		if r.Method == http.MethodOptions && r.Header.Get("Access-Control-Request-Method") != "" {
			h.Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
			h.Set("Access-Control-Allow-Headers", strings.Join([]string{
				"Authorization", "Content-Type", headerRequestID, headerSessionID, headerSessionReset,
				headerAgentCwd, headerAgentTools, headerAgentPrompt, headerToolMode, headerSystemMode,
			}, ", "))
			h.Set("Access-Control-Max-Age", "600")
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) auth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.cfg.APIKey == "" {
			next.ServeHTTP(w, r)
			return
		}
		token, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
		got := sha256.Sum256([]byte(strings.TrimSpace(token)))
		if !ok || subtle.ConstantTimeCompare(got[:], s.keyHash[:]) != 1 {
			w.Header().Set("WWW-Authenticate", `Bearer realm="agent-bridge"`)
			writeError(w, newError(http.StatusUnauthorized, errAuthentication, "missing or invalid bearer token"))
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) handleReady(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	if err := s.ready(); err != nil {
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte("not ready: CLI binary not executable\n"))
		return
	}
	_, _ = w.Write([]byte("ready\n"))
}

func (s *Server) modelObject(a *config.Agent) modelObject {
	return modelObject{ID: a.Name, Object: "model", Created: 0, OwnedBy: "agent-bridge", Description: a.Description}
}

func (s *Server) handleModels(w http.ResponseWriter, r *http.Request) {
	list := modelList{Object: "list", Data: []modelObject{}}
	for _, name := range s.cfg.AgentNames() {
		list.Data = append(list.Data, s.modelObject(s.cfg.Agents[name]))
	}
	writeJSON(w, http.StatusOK, list)
}

func (s *Server) handleModel(w http.ResponseWriter, r *http.Request) {
	a, ok := s.cfg.Agent(r.PathValue("id"))
	if !ok {
		writeError(w, newError(http.StatusNotFound, errModelNotFound, "model "+r.PathValue("id")+" does not exist"))
		return
	}
	writeJSON(w, http.StatusOK, s.modelObject(a))
}

func (s *Server) handleStats(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.stats.Snapshot(r.URL.Query().Get("reset") == "true"))
}

func (s *Server) handleMetrics(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
	s.stats.WriteMetrics(w)
}

// statusRecorder captures the status code and keeps http.Flusher working.
type statusRecorder struct {
	http.ResponseWriter
	status      int
	wroteHeader bool
}

func (r *statusRecorder) WriteHeader(code int) {
	if !r.wroteHeader {
		r.status = code
		r.wroteHeader = true
	}
	r.ResponseWriter.WriteHeader(code)
}

func (r *statusRecorder) Write(b []byte) (int, error) {
	r.wroteHeader = true
	return r.ResponseWriter.Write(b)
}

func (r *statusRecorder) Flush() {
	if f, ok := r.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

func (r *statusRecorder) Unwrap() http.ResponseWriter { return r.ResponseWriter }
