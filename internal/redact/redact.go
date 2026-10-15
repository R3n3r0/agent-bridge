// Package redact removes secrets from strings that end up in logs, error
// messages and HTTP responses.
package redact

import (
	"context"
	"log/slog"
	"regexp"
	"strings"
	"sync"
)

// Placeholder replaces every secret found.
const Placeholder = "[REDACTED]"

// providerKey matches upstream provider keys (prefix "sk-ant-").
var providerKey = regexp.MustCompile(`sk-ant-[A-Za-z0-9_\-]*`)

var (
	mu      sync.RWMutex
	secrets []string
)

// AddSecret registers an exact value that must never appear in output, for
// example the bridge's own client API key. Empty values are ignored.
func AddSecret(s string) {
	if s == "" {
		return
	}
	mu.Lock()
	defer mu.Unlock()
	for _, v := range secrets {
		if v == s {
			return
		}
	}
	secrets = append(secrets, s)
}

// Reset forgets every registered secret. Intended for tests.
func Reset() {
	mu.Lock()
	secrets = nil
	mu.Unlock()
}

// String returns s with provider keys and registered secrets replaced.
func String(s string) string {
	if s == "" {
		return s
	}
	s = providerKey.ReplaceAllString(s, Placeholder)
	mu.RLock()
	defer mu.RUnlock()
	for _, v := range secrets {
		s = strings.ReplaceAll(s, v, Placeholder)
	}
	return s
}

// Truncate shortens s to at most n runes, appending an ellipsis when cut.
func Truncate(s string, n int) string {
	if n <= 0 {
		return ""
	}
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}

// Tail returns the last n bytes of s, aligned to a valid UTF-8 boundary.
func Tail(s string, n int) string {
	if len(s) <= n {
		return s
	}
	s = s[len(s)-n:]
	for i := 0; i < len(s) && i < 4; i++ {
		if s[i]&0xC0 != 0x80 {
			return s[i:]
		}
	}
	return s
}

// Handler wraps a slog.Handler and redacts the message and every string
// attribute before passing the record on.
type Handler struct {
	next slog.Handler
}

// NewHandler returns a redacting handler around next.
func NewHandler(next slog.Handler) *Handler { return &Handler{next: next} }

// Enabled implements slog.Handler.
func (h *Handler) Enabled(ctx context.Context, l slog.Level) bool { return h.next.Enabled(ctx, l) }

// Handle implements slog.Handler.
func (h *Handler) Handle(ctx context.Context, r slog.Record) error {
	nr := slog.NewRecord(r.Time, r.Level, String(r.Message), r.PC)
	r.Attrs(func(a slog.Attr) bool {
		nr.AddAttrs(redactAttr(a))
		return true
	})
	return h.next.Handle(ctx, nr)
}

// WithAttrs implements slog.Handler.
func (h *Handler) WithAttrs(attrs []slog.Attr) slog.Handler {
	out := make([]slog.Attr, len(attrs))
	for i, a := range attrs {
		out[i] = redactAttr(a)
	}
	return &Handler{next: h.next.WithAttrs(out)}
}

// WithGroup implements slog.Handler.
func (h *Handler) WithGroup(name string) slog.Handler {
	return &Handler{next: h.next.WithGroup(name)}
}

func redactAttr(a slog.Attr) slog.Attr {
	v := a.Value.Resolve()
	switch v.Kind() {
	case slog.KindString:
		return slog.String(a.Key, String(v.String()))
	case slog.KindGroup:
		g := v.Group()
		out := make([]any, len(g))
		for i, x := range g {
			out[i] = redactAttr(x)
		}
		return slog.Group(a.Key, out...)
	case slog.KindAny:
		if err, ok := v.Any().(error); ok {
			return slog.String(a.Key, String(err.Error()))
		}
		return a
	default:
		return a
	}
}
