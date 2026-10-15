package login

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/r3n3r0/agent-bridge/internal/config"
	"github.com/r3n3r0/agent-bridge/internal/testutil"
)

var fakeBin string

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "login-test-")
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

func manager(t *testing.T, env ...string) *ProcessManager {
	t.Helper()
	cfg := &config.Config{DefaultCLIBinary: fakeBin, CodexBinary: fakeBin, KimiBinary: filepath.Join(t.TempDir(), "missing")}
	base := append(os.Environ(), "FAKE_SLEEP_MS=10000", "FAKE_RECORD=", "AGENT_BRIDGE_API_KEY=secret", "CODEX_API_KEY=k")
	return NewProcessManager(Options{CLIs: CLIs(cfg), BaseEnv: append(base, env...), NoTerminal: true})
}

func waitState(t *testing.T, m *ProcessManager, cli, state string) FlowView {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		for _, s := range m.Statuses(context.Background()) {
			if s.CLI == cli && s.Flow != nil && s.Flow.State == state {
				return *s.Flow
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("%s never reached %s", cli, state)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func TestStatuses(t *testing.T) {
	for _, logged := range []string{"0", "1"} {
		m := manager(t, "FAKE_LOGGED_IN="+logged)
		st := m.Statuses(context.Background())
		if len(st) != 3 || st[0].CLI != "claude" || st[1].CLI != "codex" || st[2].CLI != "kimi" {
			t.Fatalf("statuses %+v", st)
		}
		want := logged == "1"
		if st[0].LoggedIn == nil || *st[0].LoggedIn != want || st[0].Detail != "fake" {
			t.Fatalf("claude %+v", st[0])
		}
		if st[1].LoggedIn == nil || *st[1].LoggedIn != want {
			t.Fatalf("codex %+v", st[1])
		}
		if !want && st[1].Detail != "Not logged in" {
			t.Fatalf("codex detail %q", st[1].Detail)
		}
		if st[2].Installed || st[2].LoggedIn != nil || st[2].Detail != "not installed" {
			t.Fatalf("kimi %+v", st[2])
		}
	}
}

func TestPastedCodeFlow(t *testing.T) {
	m := manager(t)
	v, err := m.Start("claude")
	if err != nil || v.State != StateRunning || !v.AcceptsInput {
		t.Fatalf("start %+v %v", v, err)
	}
	if again, _ := m.Start("claude"); !again.StartedAt.Equal(v.StartedAt) {
		t.Fatal("a running flow must be returned, not restarted")
	}
	deadline := time.Now().Add(5 * time.Second)
	for !strings.Contains(m.flows["claude"].view().Output, "Paste code") {
		if time.Now().After(deadline) {
			t.Fatal("no prompt")
		}
		time.Sleep(20 * time.Millisecond)
	}
	v = m.flows["claude"].view()
	if strings.Contains(v.Output, "\x1b") || len(v.URLs) != 1 || v.URLs[0] != "https://example.test/oauth?x=1" {
		t.Fatalf("view %+v", v)
	}
	if err := m.Input("claude", "two\nlines"); !errors.Is(err, ErrBadInput) {
		t.Fatalf("err %v", err)
	}
	if err := m.Input("claude", "good"); err != nil {
		t.Fatal(err)
	}
	v = waitState(t, m, "claude", StateSucceeded)
	if !strings.Contains(v.Output, "Login successful") || v.AcceptsInput {
		t.Fatalf("final %+v", v)
	}
	if err := m.Input("claude", "late"); !errors.Is(err, ErrNoFlow) {
		t.Fatalf("err %v", err)
	}

	m.Start("claude")
	m.Input("claude", "bad")
	waitState(t, m, "claude", StateFailed)
}

func TestDeviceFlowAndCancel(t *testing.T) {
	m := manager(t)
	if _, err := m.Start("codex"); err != nil {
		t.Fatal(err)
	}
	time.Sleep(300 * time.Millisecond)
	v := m.flows["codex"].view()
	if v.AcceptsInput || len(v.URLs) != 1 || !strings.Contains(v.Output, "ABCD-1234") {
		t.Fatalf("view %+v", v)
	}
	start := time.Now()
	if err := m.Cancel("codex"); err != nil {
		t.Fatal(err)
	}
	waitState(t, m, "codex", StateCanceled)
	if time.Since(start) > 3*time.Second {
		t.Fatal("cancel must stop the process at once")
	}
	if err := m.Cancel("codex"); !errors.Is(err, ErrNoFlow) {
		t.Fatalf("err %v", err)
	}
	if _, err := m.Start("nope"); !errors.Is(err, ErrUnknownCLI) {
		t.Fatalf("err %v", err)
	}
	if _, err := m.Start("kimi"); err == nil {
		t.Fatal("a missing binary must fail")
	}
	if err := m.Input("kimi", "x"); !errors.Is(err, ErrNoFlow) {
		t.Fatalf("err %v", err)
	}
}

func TestEnvironment(t *testing.T) {
	m := manager(t)
	env := strings.Join(m.env(m.clis[1]), "\n")
	if strings.Contains(env, "AGENT_BRIDGE_API_KEY") || strings.Contains(env, "CODEX_API_KEY") || !strings.Contains(env, "TERM=dumb") {
		t.Fatalf("env %s", env)
	}
}

func TestParsers(t *testing.T) {
	if v, d := parseJSONStatus("garbage\n", nil); v != nil || d != "garbage" {
		t.Fatalf("%v %q", v, d)
	}
	if v, _ := parseProviderList("boom", errors.New("x")); v != nil {
		t.Fatal("provider list error")
	}
	if got := lastLine("\x1b[31mwarn\x1b[0m\r\n\nfinal " + strings.Repeat("x", 300)); !strings.HasPrefix(got, "final ") || len(got) != 200 {
		t.Fatalf("lastLine %q", got)
	}
	if got := findURLs("see https://a.test/x. and (https://a.test/x) or http://b.test/y,"); len(got) != 2 || got[1] != "http://b.test/y" {
		t.Fatalf("urls %q", got)
	}
	if w := scriptWrapper(); w != nil {
		if got := w([]string{"/bin/cli", "it's"}); got[2] != `'/bin/cli' 'it'\''s'` {
			t.Fatalf("wrapped %q", got)
		}
	}
}
