package runner

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/r3n3r0/agent-bridge/internal/config"
)

var base = []string{"-p", "--output-format", "stream-json", "--verbose"}

func TestArgsMinimal(t *testing.T) {
	got := Args(Spec{ToolsEnabled: true})
	if !reflect.DeepEqual(got, base) {
		t.Fatalf("got %q", got)
	}
}

func TestArgsEveryField(t *testing.T) {
	s := Spec{
		Model:            "opus",
		SystemPrompt:     "be careful",
		SystemPromptMode: config.PromptModeAppend,
		AllowedTools:     []string{"Read", "Bash(git *)"},
		DisallowedTools:  []string{"WebFetch", "Write"},
		ToolsEnabled:     true,
		PermissionMode:   "acceptEdits",
		MaxTurns:         4,
		AddDirs:          []string{"/a", "/b"},
		MCPConfig:        "/m.json",
		ResumeID:         "sess-1",
	}
	s.Effort = "high"
	s.JSONSchema = `{"type":"object"}`
	want := append(append([]string{}, base...),
		"--model", "opus",
		"--effort", "high",
		"--append-system-prompt", "be careful",
		"--allowedTools", "Read,Bash(git *)",
		"--disallowedTools", "WebFetch,Write",
		"--permission-mode", "acceptEdits",
		"--max-turns", "4",
		"--add-dir", "/a", "--add-dir", "/b",
		"--mcp-config", "/m.json",
		"--json-schema", `{"type":"object"}`,
		"--resume", "sess-1",
	)
	if got := Args(s); !reflect.DeepEqual(got, want) {
		t.Fatalf("got  %q\nwant %q", got, want)
	}
}

func TestArgsReplacePrompt(t *testing.T) {
	got := strings.Join(Args(Spec{ToolsEnabled: true, SystemPrompt: "x", SystemPromptMode: config.PromptModeReplace}), " ")
	if !strings.Contains(got, "--system-prompt x") || strings.Contains(got, "--append-system-prompt") {
		t.Fatalf("got %q", got)
	}
}

func TestArgsChatOnly(t *testing.T) {
	s := Spec{
		Model:        "haiku",
		ToolsEnabled: false,
		AllowedTools: []string{"Read"},
		AddDirs:      []string{"/a"},
		MCPConfig:    "/m.json",
	}
	got := Args(s)
	idx := -1
	for i, a := range got {
		if a == "--tools" {
			idx = i
		}
		if a == "--allowedTools" || a == "--add-dir" || a == "--mcp-config" {
			t.Fatalf("chat-only args contain %s: %q", a, got)
		}
	}
	if idx < 0 || idx+1 >= len(got) || got[idx+1] != "" {
		t.Fatalf(`--tools "" missing: %q`, got)
	}
}

func TestArgsNeverContainPrompt(t *testing.T) {
	for _, a := range Args(Spec{ToolsEnabled: true, Model: "m"}) {
		if !strings.HasPrefix(a, "-") && a != "stream-json" && a != "m" {
			t.Fatalf("unexpected positional argument %q", a)
		}
	}
}

func TestSpecFromAgentCopies(t *testing.T) {
	a := &config.Agent{Model: "m", AllowedTools: []string{"Read"}, ToolsEnabled: true, MaxTurns: 3}
	s := SpecFromAgent(a)
	s.AllowedTools[0] = "Changed"
	if a.AllowedTools[0] != "Read" || s.Model != "m" || s.MaxTurns != 3 || !s.ToolsEnabled {
		t.Fatal("SpecFromAgent must copy slices and fields")
	}
}

func TestEnv(t *testing.T) {
	in := []string{
		"PATH=/bin", "HOME=/h",
		"AGENT_BRIDGE_API_KEY=secret", "AGENT_BRIDGE_AUTH_MODE=api", "AGENT_BRIDGE_X=1",
		"ANTHROPIC_API_KEY=k", "ANTHROPIC_AUTH_TOKEN=t", "ANTHROPIC_MODEL=m",
	}
	sub := Env(in, config.AuthSubscription)
	want := []string{"PATH=/bin", "HOME=/h", "ANTHROPIC_MODEL=m"}
	if !reflect.DeepEqual(sub, want) {
		t.Fatalf("subscription env %q", sub)
	}
	api := Env(in, config.AuthAPI)
	want = []string{"PATH=/bin", "HOME=/h", "ANTHROPIC_API_KEY=k", "ANTHROPIC_AUTH_TOKEN=t", "ANTHROPIC_MODEL=m"}
	if !reflect.DeepEqual(api, want) {
		t.Fatalf("api env %q", api)
	}
}

func TestArgsClientTools(t *testing.T) {
	got := Args(Spec{ToolsEnabled: false, ClientTools: "/t/mcp.json", MCPConfig: "/agent.json"})
	want := append(append([]string{}, base...), "--tools", "", "--allowedTools", "mcp__functions", "--mcp-config", "/t/mcp.json")
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("chat-only: %q", got)
	}
	got = Args(Spec{ToolsEnabled: true, AllowedTools: []string{"Read"}, MCPConfig: "/agent.json", ClientTools: "/t/mcp.json", ResumeID: "s"})
	want = append(append([]string{}, base...), "--allowedTools", "Read,mcp__functions",
		"--mcp-config", "/agent.json", "--mcp-config", "/t/mcp.json", "--resume", "s")
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("tools: %q", got)
	}
}

func TestWithClientToolsTimeout(t *testing.T) {
	env := WithClientToolsTimeout([]string{"A=1"}, 2*time.Second)
	if env[len(env)-1] != "MCP_TOOL_TIMEOUT=2000" {
		t.Fatalf("env %q", env)
	}
	if got := WithClientToolsTimeout([]string{"MCP_TOOL_TIMEOUT=5"}, time.Second); len(got) != 1 {
		t.Fatalf("existing value overridden: %q", got)
	}
	if got := WithClientToolsTimeout(nil, 0); got[0] != "MCP_TOOL_TIMEOUT=86400000" {
		t.Fatalf("default %q", got)
	}
}

func TestStructuredInput(t *testing.T) {
	got := Args(Spec{ToolsEnabled: true, StructuredInput: true})
	want := append(append([]string{}, base...), "--input-format", "stream-json")
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("args %q", got)
	}
	line := UserMessage("look at [image 1]", []ImageBlock{{MediaType: "image/png", Data: []byte{1, 2, 3}}})
	if !strings.HasSuffix(line, "\n") || strings.Count(line, "\n") != 1 {
		t.Fatalf("not a single line: %q", line)
	}
	var msg struct {
		Type    string `json:"type"`
		Message struct {
			Role    string `json:"role"`
			Content []struct {
				Type   string `json:"type"`
				Text   string `json:"text"`
				Source struct {
					Type      string `json:"type"`
					MediaType string `json:"media_type"`
					Data      string `json:"data"`
				} `json:"source"`
			} `json:"content"`
		} `json:"message"`
	}
	if err := json.Unmarshal([]byte(line), &msg); err != nil {
		t.Fatal(err)
	}
	c := msg.Message.Content
	if msg.Type != "user" || msg.Message.Role != "user" || len(c) != 3 || c[0].Text != "look at [image 1]" ||
		c[1].Text != "[image 1]" || c[2].Type != "image" || c[2].Source.Type != "base64" ||
		c[2].Source.MediaType != "image/png" || c[2].Source.Data != "AQID" {
		t.Fatalf("message %+v", msg)
	}
}

func TestWithMaxOutputTokens(t *testing.T) {
	env := []string{"A=1", "CLAUDE_CODE_MAX_OUTPUT_TOKENS=5"}
	got := WithMaxOutputTokens(env, 100)
	if !reflect.DeepEqual(got, []string{"A=1", "CLAUDE_CODE_MAX_OUTPUT_TOKENS=100"}) {
		t.Fatalf("got %q", got)
	}
	if got := WithMaxOutputTokens(env, 0); !reflect.DeepEqual(got, env) {
		t.Fatalf("zero changed env: %q", got)
	}
}
