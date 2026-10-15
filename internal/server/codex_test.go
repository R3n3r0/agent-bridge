package server

import (
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/r3n3r0/agent-bridge/internal/config"
)

const codexAgents = `"coder": {"cwd": "work", "model": "gpt-x", "system_prompt": "agent prompt", "add_dirs": ["work"]},
	"chat": {"tools_enabled": false}`

func newCodexHarness(t *testing.T, env ...string) *harness {
	t.Helper()
	return newHarness(t, harnessOpts{backend: config.BackendCodex, agents: codexAgents,
		env: append([]string{"CODEX_API_KEY=sk-should-not-leak"}, env...)})
}

// configValue returns the value of the "-c key=value" argument for key.
func configValue(args []string, key string) (string, bool) {
	for i := 0; i+1 < len(args); i++ {
		if args[i] == "-c" {
			if k, v, ok := strings.Cut(args[i+1], "="); ok && k == key {
				return v, true
			}
		}
	}
	return "", false
}

func okCompletion(t *testing.T, resp *http.Response) chatCompletion {
	t.Helper()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("status %d: %s", resp.StatusCode, b)
	}
	return decode[chatCompletion](t, resp)
}

func TestCodexChatOnlyAndResume(t *testing.T) {
	h := newCodexHarness(t)
	headers := map[string]string{"X-Session-Id": "conv"}
	c := okCompletion(t, h.post(userMsg("bridge/default", "hello"), headers))
	if c.Choices[0].Message.text() != "Fake answer." || c.Choices[0].FinishReason != "stop" {
		t.Fatalf("choice %+v", c.Choices[0])
	}
	if c.Usage.PromptTokens != 13 || c.Usage.CompletionTokens != 5 {
		t.Fatalf("usage %+v", c.Usage)
	}
	first := h.starts()[0]
	if first.Args[0] != "exec" || first.Args[1] != "--json" || first.Args[len(first.Args)-1] != "-" {
		t.Fatalf("args %q", first.Args)
	}
	if _, ok := argValue(first.Args, "-m"); ok {
		t.Fatal("the default alias must use the model configured in the CLI")
	}
	if v, _ := configValue(first.Args, "sandbox_mode"); v != `"read-only"` {
		t.Fatalf("sandbox %q", v)
	}
	if v, _ := configValue(first.Args, "features.shell_tool"); v != "false" {
		t.Fatal("chat-only must disable the shell tool")
	}
	for _, kv := range first.Env {
		if strings.HasPrefix(kv, "CODEX_API_KEY=") || strings.HasPrefix(kv, "AGENT_BRIDGE_") {
			t.Fatalf("leaked %s", kv)
		}
	}

	// The fake reports thread totals on resume: the bridge returns the delta.
	c = okCompletion(t, h.post(map[string]any{"model": "bridge/default", "messages": []map[string]any{
		msg("user", "hello"), msg("assistant", "Fake answer."), msg("user", "again")}}, headers))
	if c.Usage.PromptTokens != 13 || c.Usage.CompletionTokens != 5 {
		t.Fatalf("resumed usage %+v, want the turn's usage", c.Usage)
	}
	second := h.starts()[1]
	if second.Args[1] != "resume" || second.Args[len(second.Args)-2] == "" || second.Stdin != "again" {
		t.Fatalf("resume args %q stdin %q", second.Args, second.Stdin)
	}
}

func TestCodexAgentWithTools(t *testing.T) {
	h := newCodexHarness(t)
	body := userMsg("coder", "list files")
	body["reasoning_effort"] = "max"
	okCompletion(t, h.post(body, nil))
	args := h.starts()[0].Args
	checks := map[string]string{
		"sandbox_mode":                           `"workspace-write"`,
		"developer_instructions":                 `"agent prompt"`,
		"model_reasoning_effort":                 `"xhigh"`,
		"approval_policy":                        `"never"`,
		"sandbox_workspace_write.writable_roots": `["` + filepath.Join(h.dir, "work") + `"]`,
	}
	for k, want := range checks {
		if v, _ := configValue(args, k); v != want {
			t.Errorf("%s = %s, want %s", k, v, want)
		}
	}
	if v, _ := argValue(args, "-m"); v != "gpt-x" {
		t.Fatalf("model %q", v)
	}
	if !strings.Contains(h.logs.String(), "tool=shell") {
		t.Fatal("tool use not logged")
	}
}

func TestCodexFunctionCalling(t *testing.T) {
	h := newCodexHarness(t, "FAKE_SCENARIO=tools")
	c := okCompletion(t, h.post(toolsBody("bridge/default", false, msg("user", "What's the weather in Rome?")), nil))
	m := c.Choices[0].Message
	if c.Choices[0].FinishReason != "tool_calls" || len(m.ToolCalls) != 1 || m.ToolCalls[0].Function.Name != "get_weather" {
		t.Fatalf("first response %+v", c.Choices[0])
	}
	c = okCompletion(t, h.post(followUp("bridge/default", false, c, "sunny"), nil))
	if c.Choices[0].Message.text() != "results: sunny" {
		t.Fatalf("final %+v", c.Choices[0])
	}
	args := h.starts()[0].Args
	if v, _ := configValue(args, "mcp_servers.functions.default_tools_approval_mode"); v != `"approve"` {
		t.Fatalf("client functions must not need approval: %q", args)
	}
	if v, _ := configValue(args, "mcp_servers.functions.tool_timeout_sec"); v == "" {
		t.Fatal("missing tool timeout")
	}
}

func TestCodexImagesAndSchema(t *testing.T) {
	h := newCodexHarness(t)
	body := map[string]any{"model": "bridge/default", "messages": []map[string]any{{"role": "user", "content": []map[string]any{
		{"type": "text", "text": "what is this?"},
		{"type": "image_url", "image_url": map[string]any{"url": pngDataURL()}},
	}}}}
	c := okCompletion(t, h.post(body, nil))
	if got := c.Choices[0].Message.text(); got != "Fake answer. [images=.png]" {
		t.Fatalf("content %q", got)
	}
	var image string
	for _, a := range h.starts()[0].Args {
		if p, ok := strings.CutPrefix(a, "--image="); ok {
			image = p
		}
	}
	if _, err := os.Stat(image); image == "" || !os.IsNotExist(err) {
		t.Fatalf("image file %q must be removed after the turn", image)
	}

	body = userMsg("bridge/default", "answer")
	body["response_format"] = map[string]any{"type": "json_schema", "json_schema": map[string]any{"name": "a",
		"schema": map[string]any{"type": "object", "properties": map[string]any{"answer": map[string]any{"type": "string"}}}}}
	if got := okCompletion(t, h.post(body, nil)).Choices[0].Message.text(); got != `{"answer":"Fake answer."}` {
		t.Fatalf("structured %q", got)
	}
	if _, ok := argValue(h.starts()[1].Args, "--output-schema"); !ok {
		t.Fatal("missing --output-schema")
	}

	body["response_format"] = map[string]any{"type": "json_object"}
	if got := okCompletion(t, h.post(body, nil)).Choices[0].Message.text(); got != `{"answer":"Fake answer."}` {
		t.Fatalf("json_object %q", got)
	}
	if v, _ := configValue(h.starts()[2].Args, "developer_instructions"); !strings.Contains(v, "single JSON object") {
		t.Fatalf("json_object must be an instruction: %q", h.starts()[2].Args)
	}
}

func TestCodexErrors(t *testing.T) {
	h := newCodexHarness(t, "FAKE_SCENARIO=exec_error")
	e := expectError(t, h.post(userMsg("bridge/default", "x"), nil), http.StatusBadGateway, errUpstream)
	if !strings.Contains(e.Message, "upstream failure") {
		t.Fatalf("message %q", e.Message)
	}

	h = newCodexHarness(t, "FAKE_SCENARIO=resume_missing")
	headers := map[string]string{"X-Session-Id": "gone"}
	okCompletion(t, h.post(userMsg("bridge/default", "one"), headers))
	okCompletion(t, h.post(map[string]any{"model": "bridge/default", "messages": []map[string]any{
		msg("user", "one"), msg("assistant", "Fake answer."), msg("user", "two")}}, headers))
	starts := h.starts()
	if len(starts) != 3 || starts[1].Args[1] != "resume" || starts[2].Args[1] == "resume" {
		t.Fatalf("expected a failed resume and a new session, got %d processes", len(starts))
	}
	if !strings.Contains(starts[2].Stdin, "one") {
		t.Fatalf("the new session must receive the full history: %q", starts[2].Stdin)
	}
}

func TestCodexStream(t *testing.T) {
	h := newCodexHarness(t)
	body := userMsg("bridge/default", "hi")
	body["stream"] = true
	resp := h.post(body, nil)
	data, _ := io.ReadAll(resp.Body)
	text := string(data)
	if !strings.Contains(text, `"content":"Fake answer."`) || !strings.HasSuffix(strings.TrimSpace(text), "data: [DONE]") {
		t.Fatalf("stream %s", text)
	}
}
