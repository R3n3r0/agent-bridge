package server

import (
	"net/http"
	"strings"
	"testing"

	"github.com/r3n3r0/agent-bridge/internal/config"
)

const kimiAgents = `"ops": {"cwd": "work", "allowed_tools": ["Bash"], "system_prompt": "agent prompt", "model": "kimi-k2"},
	"auto": {"cwd": "work", "permission_mode": "auto", "disallowed_tools": ["Bash"]},
	"chat": {"tools_enabled": false}`

func newKimiHarness(t *testing.T, env ...string) *harness {
	t.Helper()
	return newHarness(t, harnessOpts{backend: config.BackendKimi, agents: kimiAgents,
		env: append([]string{"KIMI_MODEL_API_KEY=sk-should-not-leak", "KIMI_MODEL_NAME=x"}, env...)})
}

// acpRequests returns the JSON-RPC requests the fake agent received in the
// i-th process.
func (h *harness) acpRequests(i int) string {
	var ends []fakeRecord
	for _, r := range h.records() {
		if r.Phase == "end" {
			ends = append(ends, r)
		}
	}
	if i >= len(ends) {
		h.t.Fatalf("no process %d", i)
	}
	return ends[i].Stdin
}

func TestKimiChatResumeAndPermissions(t *testing.T) {
	h := newKimiHarness(t)
	headers := map[string]string{"X-Session-Id": "conv"}
	c := okCompletion(t, h.post(userMsg("bridge/default", "hello"), headers))
	if c.Choices[0].Message.text() != "Fake answer." || c.Choices[0].FinishReason != "stop" {
		t.Fatalf("choice %+v", c.Choices[0])
	}
	first := h.starts()[0]
	if strings.Join(first.Args, " ") != "acp" {
		t.Fatalf("args %q", first.Args)
	}
	for _, kv := range first.Env {
		if strings.HasPrefix(kv, "KIMI_MODEL_") || strings.HasPrefix(kv, "AGENT_BRIDGE_") {
			t.Fatalf("leaked %s", kv)
		}
	}
	if !strings.Contains(strings.Join(first.Env, " "), "KIMI_DISABLE_TELEMETRY=1") {
		t.Fatal("telemetry must be disabled by default")
	}
	if !strings.Contains(h.logs.String(), `text="refused Bash"`) {
		t.Fatalf("a chat-only model must refuse tools:\n%s", h.logs.String())
	}
	reqs := h.acpRequests(0)
	if !strings.Contains(reqs, `"method":"session/new"`) || strings.Contains(reqs, `"configId":"model"`) {
		t.Fatalf("requests %s", reqs)
	}

	okCompletion(t, h.post(map[string]any{"model": "bridge/default", "messages": []map[string]any{
		msg("user", "hello"), msg("assistant", "Fake answer."), msg("user", "/help")}}, headers))
	reqs = h.acpRequests(1)
	if !strings.Contains(reqs, `"method":"session/resume"`) || !strings.Contains(reqs, `"text":" /help"`) {
		t.Fatalf("resume requests %s", reqs)
	}
}

func TestKimiAgents(t *testing.T) {
	h := newKimiHarness(t)
	body := userMsg("ops", "deploy")
	body["reasoning_effort"] = "high"
	okCompletion(t, h.post(body, nil))
	reqs := h.acpRequests(0)
	for _, want := range []string{`"configId":"model"`, `"value":"k2"`, `"configId":"thinking"`, `"value":"high"`,
		`\u003csystem_instructions\u003e\nagent prompt\n\u003c/system_instructions\u003e`} {
		if !strings.Contains(reqs, want) {
			t.Errorf("missing %s in %s", want, reqs)
		}
	}
	if !strings.Contains(h.logs.String(), `text="allowed Bash"`) {
		t.Fatal("allowed_tools must be approved")
	}

	okCompletion(t, h.post(userMsg("auto", "deploy"), nil))
	if r := h.acpRequests(1); !strings.Contains(r, `"configId":"mode"`) || !strings.Contains(r, `"value":"auto"`) {
		t.Fatalf("mode not set: %s", h.acpRequests(1))
	}
	if strings.Count(h.logs.String(), `text="refused Bash"`) != 1 {
		t.Fatal("disallowed_tools must be refused in auto mode")
	}

	expectError(t, h.post(userMsg("bridge/nothing", "x"), nil), http.StatusBadGateway, errUpstream)
}

func TestKimiFunctionCallingImagesAndJSON(t *testing.T) {
	h := newKimiHarness(t, "FAKE_SCENARIO=tools")
	c := okCompletion(t, h.post(toolsBody("bridge/default", false, msg("user", "What's the weather in Rome?")), nil))
	if c.Choices[0].FinishReason != "tool_calls" || c.Choices[0].Message.ToolCalls[0].Function.Name != "get_weather" {
		t.Fatalf("first %+v", c.Choices[0])
	}
	c = okCompletion(t, h.post(followUp("bridge/default", false, c, "sunny"), nil))
	if c.Choices[0].Message.text() != "results: sunny" {
		t.Fatalf("final %+v", c.Choices[0])
	}
	if env := strings.Join(h.starts()[0].Env, " "); !strings.Contains(env, "KIMI_MCP_TOOL_TIMEOUT_MS=") {
		t.Fatal("missing MCP tool timeout")
	}

	h = newKimiHarness(t)
	body := map[string]any{"model": "bridge/default", "messages": []map[string]any{{"role": "user", "content": []map[string]any{
		{"type": "text", "text": "what is this?"},
		{"type": "image_url", "image_url": map[string]any{"url": pngDataURL()}},
	}}}}
	if got := okCompletion(t, h.post(body, nil)).Choices[0].Message.text(); got != "Fake answer. [images=1]" {
		t.Fatalf("content %q", got)
	}
	body = userMsg("bridge/default", "answer")
	body["response_format"] = map[string]any{"type": "json_object"}
	if got := okCompletion(t, h.post(body, nil)).Choices[0].Message.text(); got != `{"answer":"Fake answer."}` {
		t.Fatalf("json %q", got)
	}
}

func TestKimiOutcomes(t *testing.T) {
	h := newKimiHarness(t, "FAKE_SCENARIO=empty")
	expectError(t, h.post(userMsg("bridge/default", "x"), nil), http.StatusBadGateway, errUpstream)

	h = newKimiHarness(t, "FAKE_SCENARIO=prompt_error")
	e := expectError(t, h.post(userMsg("bridge/default", "x"), nil), http.StatusBadGateway, errUpstream)
	if !strings.Contains(e.Message, "Authentication required") {
		t.Fatalf("message %q", e.Message)
	}

	h = newKimiHarness(t, "FAKE_SCENARIO=max_turns")
	c := okCompletion(t, h.post(userMsg("bridge/default", "x"), nil))
	if c.Choices[0].FinishReason != "length" || c.Choices[0].Message.text() != "Partial answer." {
		t.Fatalf("choice %+v", c.Choices[0])
	}

	h = newKimiHarness(t, "FAKE_SCENARIO=resume_missing")
	headers := map[string]string{"X-Session-Id": "gone"}
	okCompletion(t, h.post(userMsg("bridge/default", "one"), headers))
	c = okCompletion(t, h.post(map[string]any{"model": "bridge/default", "messages": []map[string]any{
		msg("user", "one"), msg("assistant", "Fake answer."), msg("user", "two")}}, headers))
	if len(h.starts()) != 3 || !strings.Contains(h.acpRequests(2), "one") {
		t.Fatalf("expected a fallback to a new session with the history, got %d processes", len(h.starts()))
	}
}
