package tools

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
)

// Handler executes the calls of one turn.
type Handler interface {
	// Call blocks until the result is available, ctx is done or the turn
	// ends (ErrClosed).
	Call(ctx context.Context, name string, arguments []byte) (Result, error)
}

// Registration identifies a registered handler.
type Registration struct {
	Endpoint string
	Token    string
	// Unregister removes the handler; later calls fail with ErrClosed.
	Unregister func()
}

// Broker routes calls from MCP subprocesses to turn handlers.
type Broker interface {
	Register(h Handler) (Registration, error)
}

// HTTPBroker is a Broker listening on a random loopback port. Each handler
// gets a random token that callers present as a bearer token.
type HTTPBroker struct {
	ln  net.Listener
	srv *http.Server

	mu       sync.Mutex
	handlers map[[32]byte]Handler
}

var _ Broker = (*HTTPBroker)(nil)

// NewHTTPBroker starts a broker on 127.0.0.1 with a random port.
func NewHTTPBroker() (*HTTPBroker, error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, fmt.Errorf("tools broker: %w", err)
	}
	b := &HTTPBroker{ln: ln, handlers: map[[32]byte]Handler{}}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /call", b.handleCall)
	b.srv = &http.Server{Handler: mux}
	go func() { _ = b.srv.Serve(ln) }()
	return b, nil
}

// Endpoint returns the base URL of the broker.
func (b *HTTPBroker) Endpoint() string { return "http://" + b.ln.Addr().String() }

// Close stops the broker.
func (b *HTTPBroker) Close() error { return b.srv.Close() }

// Register implements Broker.
func (b *HTTPBroker) Register(h Handler) (Registration, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return Registration{}, err
	}
	token := hex.EncodeToString(raw)
	key := sha256.Sum256([]byte(token))
	b.mu.Lock()
	b.handlers[key] = h
	b.mu.Unlock()
	var once sync.Once
	return Registration{
		Endpoint: b.Endpoint(),
		Token:    token,
		Unregister: func() {
			once.Do(func() {
				b.mu.Lock()
				delete(b.handlers, key)
				b.mu.Unlock()
			})
		},
	}, nil
}

func (b *HTTPBroker) lookup(token string) Handler {
	got := sha256.Sum256([]byte(token))
	b.mu.Lock()
	defer b.mu.Unlock()
	for k, h := range b.handlers {
		if subtle.ConstantTimeCompare(k[:], got[:]) == 1 {
			return h
		}
	}
	return nil
}

type callRequest struct {
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments"`
}

func (b *HTTPBroker) handleCall(w http.ResponseWriter, r *http.Request) {
	token, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	h := b.lookup(token)
	if !ok || h == nil {
		http.Error(w, ErrClosed.Error(), http.StatusGone)
		return
	}
	var req callRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, 16<<20)).Decode(&req); err != nil {
		http.Error(w, "invalid call", http.StatusBadRequest)
		return
	}
	res, err := h.Call(r.Context(), req.Name, req.Arguments)
	if err != nil {
		http.Error(w, err.Error(), http.StatusGone)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(res)
}

// Forward sends a call to the broker described by spec and waits for the
// result. It is used by the MCP subprocess.
func Forward(ctx context.Context, client *http.Client, spec Spec, name string, arguments json.RawMessage) (Result, error) {
	body, err := json.Marshal(callRequest{Name: name, Arguments: arguments})
	if err != nil {
		return Result{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, spec.Endpoint+"/call", bytes.NewReader(body))
	if err != nil {
		return Result{}, err
	}
	req.Header.Set("Authorization", "Bearer "+spec.Token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return Result{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return Result{}, errors.New(strings.TrimSpace(string(msg)))
	}
	var res Result
	if err := json.NewDecoder(resp.Body).Decode(&res); err != nil {
		return Result{}, err
	}
	return res, nil
}
