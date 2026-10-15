package config

import (
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func env(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

func flags(t *testing.T, args ...string) *Flags {
	t.Helper()
	fs := flag.NewFlagSet("test", flag.ContinueOnError)
	f := RegisterFlags(fs)
	if err := fs.Parse(args); err != nil {
		t.Fatal(err)
	}
	f.MarkSet(fs)
	return f
}

func writeConfig(t *testing.T, dir, body string) string {
	t.Helper()
	p := filepath.Join(dir, "agents.json")
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestDefaults(t *testing.T) {
	cfg, err := Load(flags(t), env(nil))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Listen != DefaultListen || cfg.Binary != "claude" || cfg.MaxConcurrent != 2 || cfg.MaxQueue != 32 ||
		cfg.QueueTimeoutSeconds != 300 || cfg.ShutdownGraceSeconds != 30 || cfg.StreamProgress != StreamComments ||
		!cfg.BuiltinModels || cfg.Defaults.TimeoutSeconds != 1800 || cfg.Defaults.MaxTurns != 0 ||
		cfg.Sessions.Store != SessionStoreMemory || cfg.Sessions.TTLHours != 168 || cfg.Sessions.MaxEntries != 10000 ||
		cfg.Sessions.OnBusy != OnBusyWait || cfg.AllowRemote || cfg.Metrics || cfg.AllowBypassPermissions {
		t.Fatalf("unexpected defaults: %+v", cfg)
	}
	if !cfg.BuiltinAnyModel || cfg.SystemMessages != SystemAsPrompt || cfg.ClientTools.Mode != ToolModeStateful {
		t.Fatalf("defaults: %+v", cfg)
	}
	if !cfg.ClientTools.Enabled || cfg.ClientTools.WaitTimeoutSeconds != 600 || cfg.ClientTools.BatchWindowMs != 300 {
		t.Fatalf("client_tools defaults: %+v", cfg.ClientTools)
	}
	if cfg.LogFormat != "text" || cfg.LogLevel != "info" {
		t.Fatalf("log defaults: %s %s", cfg.LogFormat, cfg.LogLevel)
	}
	if len(cfg.Agents) != 3 {
		t.Fatalf("want 3 built-in models, got %d", len(cfg.Agents))
	}
	for name, model := range BuiltinModels {
		a := cfg.Agents[name]
		if a == nil || !a.Builtin || a.ToolsEnabled || a.Model != model || a.Cwd != "" || a.SystemPrompt != "" {
			t.Fatalf("bad built-in %s: %+v", name, a)
		}
	}
	cfg.SetScratchDir("/tmp/x")
	if cfg.Agents["claude-code/haiku"].Cwd != "/tmp/x" {
		t.Fatal("scratch dir not assigned")
	}
	if !strings.HasSuffix(cfg.Sessions.Path, DefaultSessionsFile) {
		t.Fatalf("sessions path %s", cfg.Sessions.Path)
	}
}

func TestPrecedence(t *testing.T) {
	dir := t.TempDir()
	p := writeConfig(t, dir, `{"listen":"127.0.0.1:1000","max_concurrent":3,"claude_binary":"from-file"}`)

	cfg, err := Load(flags(t, "-config", p), env(nil))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Listen != "127.0.0.1:1000" || cfg.MaxConcurrent != 3 || cfg.Binary != "from-file" {
		t.Fatalf("file not applied: %+v", cfg)
	}

	e := env(map[string]string{EnvConfig: p, EnvListen: "127.0.0.1:2000", EnvLogFormat: "json", EnvLogLevel: "debug"})
	cfg, err = Load(flags(t), e)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Listen != "127.0.0.1:2000" || cfg.LogFormat != "json" || cfg.LogLevel != "debug" || cfg.Path != p {
		t.Fatalf("env not applied over file: %+v", cfg)
	}

	cfg, err = Load(flags(t, "-listen", "127.0.0.1:3000", "-max-concurrent", "5", "-claude-bin", "flagbin",
		"-log-format", "text", "-log-level", "warn", "-allow-remote"), e)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Listen != "127.0.0.1:3000" || cfg.MaxConcurrent != 5 || cfg.Binary != "flagbin" ||
		cfg.LogFormat != "text" || cfg.LogLevel != "warn" || !cfg.AllowRemote {
		t.Fatalf("flags not applied over env: %+v", cfg)
	}
}

func TestAgentResolution(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "repo"), 0o755); err != nil {
		t.Fatal(err)
	}
	p := writeConfig(t, dir, `{
		"defaults": {"timeout_seconds": 60, "max_turns": 7},
		"agents": {
			"coder": {"cwd": "repo", "allowed_tools": ["Read","Edit"], "add_dirs": ["extra"], "mcp_config": "mcp.json",
				"system_prompt": "x", "system_prompt_mode": "replace", "permission_mode": "acceptEdits"},
			"chat": {"tools_enabled": false, "model": "haiku", "max_turns": 1, "timeout_seconds": 5}
		}
	}`)
	cfg, err := Load(flags(t, "-config", p), env(nil))
	if err != nil {
		t.Fatal(err)
	}
	c := cfg.Agents["coder"]
	if c.Cwd != filepath.Join(dir, "repo") || c.AddDirs[0] != filepath.Join(dir, "extra") || c.MCPConfig != filepath.Join(dir, "mcp.json") {
		t.Fatalf("relative paths not resolved: %+v", c)
	}
	if c.Model != "sonnet" || !c.ToolsEnabled || c.MaxTurns != 7 || c.Timeout != 60*time.Second ||
		c.SystemPromptMode != "replace" || c.PermissionMode != "acceptEdits" {
		t.Fatalf("coder: %+v", c)
	}
	h := cfg.Agents["chat"]
	if h.ToolsEnabled || h.Model != "haiku" || h.MaxTurns != 1 || h.Timeout != 5*time.Second || h.Cwd != "" ||
		h.SystemPromptMode != "append" || h.PermissionMode != "default" {
		t.Fatalf("chat: %+v", h)
	}
	names := cfg.AgentNames()
	if names[0] != "chat" || names[1] != "coder" || len(names) != 5 {
		t.Fatalf("names: %v", names)
	}
}

func TestValidationErrors(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "afile")
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name, body, want string
	}{
		{"bad name", `{"agents":{"bad name!":{"tools_enabled":false}}}`, `agent "bad name!": name`},
		{"long name", `{"agents":{"` + strings.Repeat("a", 65) + `":{"tools_enabled":false}}}`, "name"},
		{"builtin conflict", `{"agents":{"claude-code/opus":{"tools_enabled":false}}}`, "conflicts with a built-in"},
		{"missing cwd", `{"agents":{"a":{}}}`, `agent "a": cwd: is required`},
		{"cwd not found", `{"agents":{"a":{"cwd":"nope"}}}`, `agent "a": cwd`},
		{"cwd is file", `{"agents":{"a":{"cwd":"afile"}}}`, "is not a directory"},
		{"prompt mode", `{"agents":{"a":{"tools_enabled":false,"system_prompt_mode":"x"}}}`, "system_prompt_mode"},
		{"permission mode", `{"agents":{"a":{"tools_enabled":false,"permission_mode":"yolo"}}}`, "permission_mode"},
		{"bypass", `{"agents":{"a":{"tools_enabled":false,"permission_mode":"bypassPermissions"}}}`, "allow_bypass_permissions"},
		{"negative turns", `{"agents":{"a":{"tools_enabled":false,"max_turns":-1}}}`, "max_turns"},
		{"negative timeout", `{"agents":{"a":{"tools_enabled":false,"timeout_seconds":-1}}}`, "timeout_seconds"},
		{"negative queue", `{"max_queue":-1}`, "max_queue"},
		{"system messages", `{"system_messages":"inline"}`, "system_messages"},
		{"tool mode", `{"client_tools":{"mode":"sometimes"}}`, "client_tools.mode"},
		{"negative tool wait", `{"client_tools":{"wait_timeout_seconds":-1}}`, "client_tools.wait_timeout_seconds"},
		{"zero concurrency", `{"max_concurrent":0}`, "max_concurrent"},
		{"stream progress", `{"stream_progress":"all"}`, "stream_progress"},
		{"session store", `{"sessions":{"store":"redis"}}`, "sessions.store"},
		{"on busy", `{"sessions":{"on_busy":"drop"}}`, "sessions.on_busy"},
		{"cors wildcard", `{"cors_origins":["*"]}`, "cors_origins"},
		{"root missing", `{"overrides":{"allowed_roots":["missing"]}}`, "overrides.allowed_roots"},
		{"root is file", `{"overrides":{"allowed_roots":["afile"]}}`, "overrides.allowed_roots"},
		{"unknown field", `{"listn":"x"}`, "unknown field"},
		{"bad json", `{`, "config"},
		{"empty listen", `{"listen":""}`, "listen"},
		{"empty binary", `{"claude_binary":""}`, "claude_binary"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			p := writeConfig(t, dir, c.body)
			_, err := Load(flags(t, "-config", p), env(nil))
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("got %v, want error containing %q", err, c.want)
			}
		})
	}
	if _, err := Load(flags(t, "-log-level", "trace"), env(nil)); err == nil {
		t.Fatal("bad log level accepted")
	}
	if _, err := Load(flags(t, "-log-format", "xml"), env(nil)); err == nil {
		t.Fatal("bad log format accepted")
	}
	if _, err := Load(flags(t, "-config", filepath.Join(dir, "missing.json")), env(nil)); err == nil {
		t.Fatal("missing file accepted")
	}
}

func TestBypassAllowedAndBuiltinsDisabled(t *testing.T) {
	dir := t.TempDir()
	p := writeConfig(t, dir, `{"allow_bypass_permissions":true,"builtin_models":false,
		"agents":{"claude-code/opus":{"tools_enabled":false,"permission_mode":"bypassPermissions"}}}`)
	cfg, err := Load(flags(t, "-config", p), env(nil))
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Agents) != 1 || cfg.Agents["claude-code/opus"].Builtin {
		t.Fatalf("agents: %+v", cfg.Agents)
	}
}

func TestAllowedRootsCanonical(t *testing.T) {
	dir := t.TempDir()
	realDir := filepath.Join(dir, "realDir")
	if err := os.Mkdir(realDir, 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "link")
	if err := os.Symlink(realDir, link); err != nil {
		t.Skip("symlinks not supported:", err)
	}
	p := writeConfig(t, dir, `{"overrides":{"allowed_roots":["link"]}}`)
	cfg, err := Load(flags(t, "-config", p), env(nil))
	if err != nil {
		t.Fatal(err)
	}
	want, _ := filepath.EvalSymlinks(realDir)
	if cfg.Overrides.AllowedRoots[0] != want {
		t.Fatalf("root %s, want %s", cfg.Overrides.AllowedRoots[0], want)
	}
}

func TestValidateAuth(t *testing.T) {
	cases := []struct {
		name string
		env  map[string]string
		ok   bool
	}{
		{"missing", map[string]string{}, false},
		{"invalid", map[string]string{EnvAuthMode: "token"}, false},
		{"subscription clean", map[string]string{EnvAuthMode: "subscription"}, true},
		{"subscription with key", map[string]string{EnvAuthMode: "subscription", EnvUpstreamKey: "k"}, false},
		{"subscription with token", map[string]string{EnvAuthMode: "subscription", EnvUpstreamTok: "t"}, false},
		{"api with key", map[string]string{EnvAuthMode: "api", EnvUpstreamKey: "k"}, true},
		{"api without key", map[string]string{EnvAuthMode: "api"}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			cfg, err := Load(flags(t), env(c.env))
			if err != nil {
				t.Fatal(err)
			}
			err = cfg.ValidateAuth(env(c.env))
			if (err == nil) != c.ok {
				t.Fatalf("ok=%v, err=%v", c.ok, err)
			}
		})
	}
}

func TestValidateNetwork(t *testing.T) {
	cases := []struct {
		listen string
		remote bool
		key    string
		ok     bool
	}{
		{"127.0.0.1:8787", false, "", true},
		{"[::1]:8787", false, "", true},
		{"localhost:8787", false, "", true},
		{"0.0.0.0:8787", false, "", false},
		{":8787", false, "", false},
		{"0.0.0.0:8787", true, "", false},
		{"0.0.0.0:8787", true, "secret", true},
		{"192.168.1.5:8787", false, "secret", false},
		{"not-an-address", false, "", false},
	}
	for _, c := range cases {
		cfg := &Config{Listen: c.listen, AllowRemote: c.remote, APIKey: c.key}
		if err := cfg.ValidateNetwork(); (err == nil) != c.ok {
			t.Errorf("%+v: err=%v", c, err)
		}
	}
}

func TestDirWithoutFile(t *testing.T) {
	cfg := &Config{}
	wd, _ := os.Getwd()
	if cfg.Dir() != wd {
		t.Fatalf("Dir() = %s", cfg.Dir())
	}
	if cfg.abs("/x") != "/x" && !filepath.IsAbs(cfg.abs("/x")) {
		t.Fatal("abs changed an absolute path")
	}
}

func TestAgentLookupAnyModel(t *testing.T) {
	cfg, err := Load(flags(t), env(nil))
	if err != nil {
		t.Fatal(err)
	}
	cfg.SetScratchDir("/scratch")
	a, ok := cfg.Agent("claude-code/opus")
	if !ok || a.Model != "opus" {
		t.Fatalf("builtin %+v", a)
	}
	a, ok = cfg.Agent("claude-code/claude-model-x-1[1m]")
	if !ok || a.Model != "claude-model-x-1[1m]" || a.ToolsEnabled || !a.Builtin || a.Cwd != "/scratch" {
		t.Fatalf("any model %+v", a)
	}
	for _, bad := range []string{"other/opus", "claude-code/", "claude-code/-x", "claude-code/a b", "claude-code/a/b"} {
		if _, ok := cfg.Agent(bad); ok {
			t.Errorf("%q accepted", bad)
		}
	}
	cfg.BuiltinAnyModel = false
	if _, ok := cfg.Agent("claude-code/model-x"); ok {
		t.Fatal("any model disabled but accepted")
	}
	if _, ok := cfg.Agent("claude-code/haiku"); !ok {
		t.Fatal("builtins must stay available")
	}
}
