package redact

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"
)

func TestStringProviderKeys(t *testing.T) {
	Reset()
	in := "error: key sk-ant-api03-AbC_d-123 rejected; other sk-ant-oat01-xyz"
	got := String(in)
	if strings.Contains(got, "sk-ant-") {
		t.Fatalf("key not redacted: %q", got)
	}
	if strings.Count(got, Placeholder) != 2 {
		t.Fatalf("want 2 placeholders, got %q", got)
	}
}

func TestStringRegisteredSecret(t *testing.T) {
	Reset()
	defer Reset()
	AddSecret("bridge-secret-123")
	AddSecret("")
	AddSecret("bridge-secret-123")
	got := String("Authorization: Bearer bridge-secret-123")
	if got != "Authorization: Bearer "+Placeholder {
		t.Fatalf("got %q", got)
	}
	if String("") != "" {
		t.Fatal("empty string changed")
	}
}

func TestHandlerRedactsLogs(t *testing.T) {
	Reset()
	defer Reset()
	AddSecret("proxy-key-xyz")
	var buf bytes.Buffer
	log := slog.New(NewHandler(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	log = log.With("static", "sk-ant-api03-static")
	log.WithGroup("g").Info("message with sk-ant-api03-msg",
		"attr", "proxy-key-xyz",
		"err", errors.New("failed with sk-ant-api03-err"),
		slog.Group("nested", "v", "sk-ant-nested"),
		"n", 42)
	out := buf.String()
	if got := String("Incorrect API key provided: sk-proj-Abc123_xyz-9 and sk-test-123456"); strings.Contains(got, "sk-") {
		t.Fatalf("OpenAI keys not redacted: %s", got)
	}
	for _, leak := range []string{"sk-ant-", "proxy-key-xyz"} {
		if strings.Contains(out, leak) {
			t.Fatalf("log leaks %q: %s", leak, out)
		}
	}
	if !strings.Contains(out, "n=42") {
		t.Fatalf("non-string attribute lost: %s", out)
	}
	if !log.Handler().Enabled(context.Background(), slog.LevelDebug) {
		t.Fatal("Enabled not forwarded")
	}
}

func TestTruncateAndTail(t *testing.T) {
	if got := Truncate("àbcdef", 3); got != "àbc…" {
		t.Fatalf("Truncate = %q", got)
	}
	if got := Truncate("abc", 5); got != "abc" {
		t.Fatalf("Truncate = %q", got)
	}
	if got := Truncate("abc", 0); got != "" {
		t.Fatalf("Truncate = %q", got)
	}
	if got := Tail("hello world", 5); got != "world" {
		t.Fatalf("Tail = %q", got)
	}
	if got := Tail("short", 10); got != "short" {
		t.Fatalf("Tail = %q", got)
	}
	// Cutting inside a multi-byte rune must not produce invalid UTF-8.
	if got := Tail("xày", 2); got != "y" {
		t.Fatalf("Tail = %q", got)
	}
}
