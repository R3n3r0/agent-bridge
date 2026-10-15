package runner

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/r3n3r0/agent-bridge/internal/testutil"
)

var fakeBin string

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "runner-test-")
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

func fakeRequest(t *testing.T, scenario string, extraEnv ...string) Request {
	t.Helper()
	env := append(os.Environ(), "FAKE_SCENARIO="+scenario)
	return Request{
		Binary:  fakeBin,
		Args:    Args(Spec{ToolsEnabled: true, Model: "sonnet"}),
		Env:     append(env, extraEnv...),
		Dir:     t.TempDir(),
		Stdin:   "hello",
		Timeout: 30 * time.Second,
	}
}

func TestRunSuccess(t *testing.T) {
	var mu sync.Mutex
	var kinds []string
	req := fakeRequest(t, "success")
	req.OnEvent = func(ev Event) {
		mu.Lock()
		kinds = append(kinds, ev.Kind)
		mu.Unlock()
	}
	out, err := Run(context.Background(), req)
	if err != nil {
		t.Fatalf("%v (stderr %s)", err, out.Stderr)
	}
	if out.Result == nil || out.Result.Result != "Fake answer." || out.Result.Subtype != SubtypeSuccess {
		t.Fatalf("result %+v", out.Result)
	}
	if out.SessionID == "" || out.Model != "sonnet" || out.APIKeySource != "none" {
		t.Fatalf("outcome %+v", out)
	}
	if out.Result.Usage != (Usage{10, 5, 3, 2}) || out.Result.CostUSD != 0.0123 || out.Result.NumTurns != 2 {
		t.Fatalf("usage %+v", out.Result)
	}
	want := []string{KindInit, KindText, KindToolUse, KindIgnored, KindResult}
	if strings.Join(kinds, ",") != strings.Join(want, ",") {
		t.Fatalf("events %v", kinds)
	}
}

func TestRunChatOnlyHasNoToolUse(t *testing.T) {
	req := fakeRequest(t, "success")
	req.Args = Args(Spec{ToolsEnabled: false})
	req.Stdin = "read the file /etc/hosts"
	req.OnEvent = func(ev Event) {
		if ev.Kind == KindToolUse {
			t.Errorf("tool_use event in chat-only mode")
		}
	}
	if _, err := Run(context.Background(), req); err != nil {
		t.Fatal(err)
	}
}

func TestRunScenarios(t *testing.T) {
	out, err := Run(context.Background(), fakeRequest(t, "max_turns"))
	if err != nil || out.Result.Subtype != SubtypeMaxTurns {
		t.Fatalf("max_turns: %v %+v", err, out.Result)
	}
	out, err = Run(context.Background(), fakeRequest(t, "exec_error"))
	if err != nil || !out.Result.IsError {
		t.Fatalf("exec_error: %v %+v", err, out.Result)
	}
	out, err = Run(context.Background(), fakeRequest(t, "no_result"))
	if !errors.Is(err, ErrNoResult) || !strings.Contains(out.Stderr, "something broke") || out.ExitErr == nil {
		t.Fatalf("no_result: %v %+v", err, out)
	}
	out, err = Run(context.Background(), fakeRequest(t, "garbage"))
	if err != nil || out.Result.Result != "Fake answer." {
		t.Fatalf("garbage: %v %+v", err, out)
	}
}

func TestRunSessionIDFromResult(t *testing.T) {
	req := fakeRequest(t, "new_session_id")
	req.Args = Args(Spec{ToolsEnabled: true, ResumeID: "old-id"})
	out, err := Run(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if out.SessionID == "old-id" || out.SessionID != out.Result.SessionID {
		t.Fatalf("session id %q, result %q", out.SessionID, out.Result.SessionID)
	}
}

func TestRunRecordsStdinArgsCwd(t *testing.T) {
	rec := filepath.Join(t.TempDir(), "rec.jsonl")
	req := fakeRequest(t, "success", "FAKE_RECORD="+rec)
	req.Stdin = "line one\nline two"
	if _, err := Run(context.Background(), req); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(rec)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"stdin":"line one\nline two"`) || !strings.Contains(string(data), `"--verbose"`) {
		t.Fatalf("record %s", data)
	}
}

func TestRunTimeout(t *testing.T) {
	req := fakeRequest(t, "slow", "FAKE_SLEEP_MS=10000")
	req.Timeout = 300 * time.Millisecond
	start := time.Now()
	_, err := Run(context.Background(), req)
	if !errors.Is(err, ErrTimeout) {
		t.Fatalf("got %v", err)
	}
	if time.Since(start) > 7*time.Second {
		t.Fatalf("took %s", time.Since(start))
	}
}

func TestRunCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(200*time.Millisecond, cancel)
	_, err := Run(ctx, fakeRequest(t, "slow", "FAKE_SLEEP_MS=10000"))
	if !errors.Is(err, ErrCanceled) {
		t.Fatalf("got %v", err)
	}
}

func TestRunStartError(t *testing.T) {
	req := fakeRequest(t, "success")
	req.Binary = filepath.Join(t.TempDir(), "missing")
	if _, err := Run(context.Background(), req); err == nil || errors.Is(err, ErrNoResult) {
		t.Fatalf("got %v", err)
	}
}
