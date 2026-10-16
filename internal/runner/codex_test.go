package runner

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/r3n3r0/agent-bridge/internal/config"
)

func codexConfig(args []string) map[string]string {
	out := map[string]string{}
	for i := 0; i+1 < len(args); i++ {
		if args[i] == "-c" {
			k, v, _ := strings.Cut(args[i+1], "=")
			out[k] = v
		}
	}
	return out
}

func prepare(t *testing.T, turn Turn) *Invocation {
	t.Helper()
	inv, err := CodexBackend{}.Prepare(turn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(inv.Cleanup)
	return inv
}

func TestCodexChatOnly(t *testing.T) {
	inv := prepare(t, Turn{
		Spec:     Spec{Model: "gpt-x", Effort: "minimal", SystemPrompt: "be \"brief\"\nplease"},
		Prompt:   "hi",
		BaseEnv:  []string{"PATH=/bin", "CODEX_API_KEY=k", "OPENAI_API_KEY=o", "AGENT_BRIDGE_API_KEY=x"},
		AuthMode: config.AuthSubscription,
	})
	a := inv.Args
	if strings.Join(a[:3], " ") != "exec --json --skip-git-repo-check" || a[len(a)-1] != "-" || inv.Stdin != "hi" {
		t.Fatalf("args %q", a)
	}
	c := codexConfig(a)
	want := map[string]string{
		"sandbox_mode":           `"read-only"`,
		"features.shell_tool":    "false",
		"web_search":             `"disabled"`,
		"model_reasoning_effort": `"minimal"`,
		"developer_instructions": `"be \"brief\"\nplease"`,
	}
	for k, v := range want {
		if c[k] != v {
			t.Errorf("%s = %s, want %s", k, c[k], v)
		}
	}
	if strings.Join(inv.Env, " ") != "PATH=/bin" {
		t.Fatalf("env %q", inv.Env)
	}
}

func TestCodexToolsResumeAndFiles(t *testing.T) {
	dir := t.TempDir()
	mcp := filepath.Join(dir, "mcp.json")
	os.WriteFile(mcp, []byte(`{"mcpServers":{"web":{"url":"https://x","headers":{"Authorization":"Bearer t"}},
		"fs":{"command":"fs-server","args":["--root","/a"],"env":{"K":"v"}}}}`), 0o600)
	clientTools := filepath.Join(dir, "functions.json")
	os.WriteFile(clientTools, []byte(`{"mcpServers":{"functions":{"type":"stdio","command":"/bridge","args":["mcp-tools","-spec","/s"]}}}`), 0o600)
	inv := prepare(t, Turn{
		Spec: Spec{ToolsEnabled: true, Sandbox: "danger-full-access", AddDirs: []string{"/a", "/b"}, MCPConfig: mcp,
			ClientTools: clientTools, SystemPrompt: "only me", SystemPromptMode: config.PromptModeReplace,
			JSONSchema: `{"type":"object","properties":{}}`, ResumeID: "thread-1"},
		Images:      []ImageBlock{{MediaType: "image/jpeg", Data: []byte("j")}, {MediaType: "image/webp", Data: []byte("w")}},
		ToolTimeout: 90 * time.Second,
	})
	a := inv.Args
	if a[1] != "resume" || a[len(a)-2] != "thread-1" || a[len(a)-1] != "-" {
		t.Fatalf("args %q", a)
	}
	c := codexConfig(a)
	want := map[string]string{
		"sandbox_mode":                                      `"danger-full-access"`,
		"sandbox_workspace_write.writable_roots":            `["/a", "/b"]`,
		"mcp_servers.fs.command":                            `"fs-server"`,
		"mcp_servers.fs.args":                               `["--root", "/a"]`,
		"mcp_servers.fs.env":                                `{"K" = "v"}`,
		"mcp_servers.web.url":                               `"https://x"`,
		"mcp_servers.web.http_headers":                      `{"Authorization" = "Bearer t"}`,
		"mcp_servers.functions.command":                     `"/bridge"`,
		"mcp_servers.functions.tool_timeout_sec":            "90",
		"mcp_servers.functions.default_tools_approval_mode": `"approve"`,
	}
	for k, v := range want {
		if c[k] != v {
			t.Errorf("%s = %s, want %s", k, c[k], v)
		}
	}
	if _, ok := c["mcp_servers.fs.tool_timeout_sec"]; ok {
		t.Error("agent servers keep the CLI timeout")
	}
	var files []string
	for i, x := range a {
		if p, ok := strings.CutPrefix(x, "--image="); ok {
			files = append(files, p)
		}
		if x == "--output-schema" {
			files = append(files, a[i+1])
		}
	}
	var instr string
	_ = jsonUnquote(c["model_instructions_file"], &instr)
	files = append(files, instr)
	if len(files) != 4 || filepath.Ext(files[0]) != ".json" || filepath.Ext(files[1]) != ".jpg" || filepath.Ext(files[2]) != ".webp" {
		t.Fatalf("files %q", files)
	}
	if data, _ := os.ReadFile(instr); string(data) != "only me" {
		t.Fatalf("instructions %q", data)
	}
	inv.Cleanup()
	for _, f := range files {
		if _, err := os.Stat(f); !os.IsNotExist(err) {
			t.Fatalf("%s not removed", f)
		}
	}
}

func TestCodexPrepareErrors(t *testing.T) {
	if _, err := (CodexBackend{}).Prepare(Turn{Spec: Spec{SystemPrompt: strings.Repeat("x", maxInlineInstructions+1)}}); err == nil {
		t.Fatal("expected an error for oversized instructions")
	}
	dir := t.TempDir()
	for _, body := range []string{`{`, `{"mcpServers":{"bad name":{"command":"x"}}}`, `{"mcpServers":{"x":{}}}`} {
		p := filepath.Join(dir, "m.json")
		os.WriteFile(p, []byte(body), 0o600)
		if _, err := (CodexBackend{}).Prepare(Turn{Spec: Spec{ToolsEnabled: true, MCPConfig: p}}); err == nil {
			t.Fatalf("expected an error for %s", body)
		}
	}
	if _, err := (CodexBackend{}).Prepare(Turn{Spec: Spec{ToolsEnabled: true, MCPConfig: filepath.Join(dir, "none")}}); err == nil {
		t.Fatal("expected an error for a missing file")
	}
}

func TestCodexJSONObjectIsAnInstruction(t *testing.T) {
	inv := prepare(t, Turn{Spec: Spec{JSONSchema: jsonObjectSchema}})
	c := codexConfig(inv.Args)
	if !strings.Contains(c["developer_instructions"], "single JSON object") || strings.Contains(strings.Join(inv.Args, " "), "--output-schema") {
		t.Fatalf("args %q", inv.Args)
	}
}

func TestCodexBackendBasics(t *testing.T) {
	b, err := NewBackend(config.BackendCodex)
	if err != nil || b.Name() != config.BackendCodex || b.MinVersion() != CodexMinVersion || !b.CumulativeUsage() {
		t.Fatalf("backend %v %v", b, err)
	}
	if _, err := NewBackend("other"); err == nil || err.Error() != "unknown backend other" {
		t.Fatalf("err %v", err)
	}
	if d, _ := NewBackend(""); d.Name() != config.BackendDefault || d.CumulativeUsage() {
		t.Fatal("default backend")
	}
	missing := &Outcome{Stderr: "Error: thread/resume failed: no rollout found for thread id x"}
	if !b.ResumeMissing(missing) || b.ResumeMissing(&Outcome{Stderr: "other"}) ||
		b.ResumeMissing(&Outcome{Stderr: missing.Stderr, Result: &ResultEvent{Subtype: SubtypeSuccess}}) {
		t.Fatal("ResumeMissing")
	}
	for mt, ext := range map[string]string{"image/png": ".png", "image/gif": ".gif", "image/jpeg": ".jpg"} {
		if imageExt(mt) != ext {
			t.Errorf("%s -> %s", mt, imageExt(mt))
		}
	}
}
