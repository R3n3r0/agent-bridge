package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/r3n3r0/agent-bridge/internal/testutil"
)

func TestVersionAndHelp(t *testing.T) {
	var out, errb bytes.Buffer
	if code := run([]string{"version"}, &out, &errb); code != 0 || !strings.HasPrefix(out.String(), "agent-bridge dev") {
		t.Fatalf("version: %d %q", code, out.String())
	}
	out.Reset()
	if code := run([]string{"help"}, &out, &errb); code != 0 || !strings.Contains(out.String(), "check-config") {
		t.Fatalf("help: %d", code)
	}
	if code := run([]string{"bogus"}, &out, &errb); code != 2 {
		t.Fatalf("unknown command: %d", code)
	}
}

func TestCheckConfig(t *testing.T) {
	dir := t.TempDir()
	good := filepath.Join(dir, "good.json")
	_ = os.WriteFile(good, []byte(`{"agents":{"reviewer":{"cwd":".","allowed_tools":["Read"],"system_prompt":"x","add_dirs":["."],"mcp_config":"m.json"},
		"chat":{"tools_enabled":false}}}`), 0o600)
	var out, errb bytes.Buffer
	if code := run([]string{"check-config", "-config", good}, &out, &errb); code != 0 {
		t.Fatalf("code %d: %s", code, errb.String())
	}
	for _, want := range []string{"reviewer (agent)", "allowed: Read", "chat (agent)", "disabled (chat only)", "claude-code/opus (built-in)"} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("missing %q in %s", want, out.String())
		}
	}
	bad := filepath.Join(dir, "bad.json")
	_ = os.WriteFile(bad, []byte(`{"agents":{"x":{}}}`), 0o600)
	errb.Reset()
	if code := run([]string{"check-config", "-config", bad}, &out, &errb); code != 1 || !strings.Contains(errb.String(), `agent "x": cwd`) {
		t.Fatalf("code %d: %s", code, errb.String())
	}
	if code := run([]string{"check-config", "-nope"}, &out, &errb); code != 2 {
		t.Fatalf("bad flag: %d", code)
	}
	if code := run([]string{"check-config", "extra"}, &out, &errb); code != 2 {
		t.Fatalf("extra arg: %d", code)
	}
}

func TestServeRefusesUnsafeStart(t *testing.T) {
	cases := []struct {
		name string
		args []string
		env  map[string]string
		want string
	}{
		{"no auth mode", []string{"-listen", "127.0.0.1:0"}, nil, "AGENT_BRIDGE_AUTH_MODE is required"},
		{"subscription with key", []string{"-listen", "127.0.0.1:0"},
			map[string]string{"AGENT_BRIDGE_AUTH_MODE": "subscription", "ANTHROPIC_API_KEY": "sk-ant-x"}, "ANTHROPIC_API_KEY is set"},
		{"non loopback", []string{"-listen", "0.0.0.0:0"},
			map[string]string{"AGENT_BRIDGE_AUTH_MODE": "subscription"}, "allow_remote"},
		{"remote without key", []string{"-listen", "0.0.0.0:0", "-allow-remote"},
			map[string]string{"AGENT_BRIDGE_AUTH_MODE": "subscription"}, "AGENT_BRIDGE_API_KEY must be set"},
		{"bad config", []string{"-config", "/nonexistent/agents.json"}, nil, "configuration is not valid"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			for _, k := range []string{"AGENT_BRIDGE_AUTH_MODE", "ANTHROPIC_API_KEY", "ANTHROPIC_AUTH_TOKEN", "AGENT_BRIDGE_API_KEY", "AGENT_BRIDGE_CONFIG", "AGENT_BRIDGE_LISTEN"} {
				t.Setenv(k, "")
			}
			for k, v := range c.env {
				t.Setenv(k, v)
			}
			var out, errb bytes.Buffer
			if code := run(append([]string{"serve"}, c.args...), &out, &errb); code != 1 {
				t.Fatalf("code %d, output %s", code, errb.String())
			}
			if !strings.Contains(errb.String(), c.want) {
				t.Fatalf("missing %q in %s", c.want, errb.String())
			}
			if strings.Contains(errb.String(), "sk-ant-x") {
				t.Fatal("secret in output")
			}
		})
	}
}

func TestDoctor(t *testing.T) {
	bin, err := testutil.BuildFakeCLI(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("AGENT_BRIDGE_AUTH_MODE", "subscription")
	t.Setenv("ANTHROPIC_API_KEY", "")
	t.Setenv("ANTHROPIC_AUTH_TOKEN", "")
	t.Setenv("AGENT_BRIDGE_CONFIG", "")
	t.Setenv("AGENT_BRIDGE_LISTEN", "")
	t.Setenv("FAKE_SCENARIO", "success")
	t.Setenv("FAKE_RECORD", "")
	var out, errb bytes.Buffer
	if code := run([]string{"doctor", "-live", "-claude-bin", bin}, &out, &errb); code != 0 {
		t.Fatalf("code %d:\n%s%s", code, out.String(), errb.String())
	}
	for _, want := range []string{"[ok]   CLI version 2.1.200", "[ok]   authentication mode: subscription", "[ok]   live check: model haiku"} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("missing %q in\n%s", want, out.String())
		}
	}

	out.Reset()
	t.Setenv("FAKE_KEY_SOURCE", "ANTHROPIC_API_KEY")
	if code := run([]string{"doctor", "-live", "-claude-bin", bin}, &out, &errb); code != 1 {
		t.Fatalf("api key source accepted:\n%s", out.String())
	}
	t.Setenv("FAKE_KEY_SOURCE", "")

	out.Reset()
	if code := run([]string{"doctor", "-claude-bin", filepath.Join(t.TempDir(), "missing")}, &out, &errb); code != 1 ||
		!strings.Contains(out.String(), "[fail] CLI binary") {
		t.Fatalf("missing binary: %d\n%s", code, out.String())
	}

	out.Reset()
	t.Setenv("AGENT_BRIDGE_AUTH_MODE", "")
	if code := run([]string{"doctor", "-live", "-claude-bin", bin}, &out, &errb); code != 1 ||
		!strings.Contains(out.String(), "live check skipped") {
		t.Fatalf("missing auth mode: %d\n%s", code, out.String())
	}
}

func TestCompareVersions(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		{"2.1.128", "2.1.128", 0},
		{"2.1.129", "2.1.128", 1},
		{"2.0.300", "2.1.0", -1},
		{"10.0.0", "9.9.9", 1},
	}
	for _, c := range cases {
		if got := compareVersions(c.a, c.b); got != c.want {
			t.Errorf("compare(%s,%s)=%d", c.a, c.b, got)
		}
	}
}
