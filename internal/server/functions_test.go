package server

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

var weatherTool = map[string]any{
	"type": "function",
	"function": map[string]any{
		"name":        "get_weather",
		"description": "Current weather for a city",
		"parameters": map[string]any{
			"type":       "object",
			"properties": map[string]any{"city": map[string]any{"type": "string"}},
			"required":   []string{"city"},
		},
	},
}

func toolsBody(model string, stream bool, messages ...map[string]any) map[string]any {
	b := map[string]any{"model": model, "tools": []any{weatherTool}, "messages": messages}
	if stream {
		b["stream"] = true
	}
	return b
}

func msg(role, content string) map[string]any {
	return map[string]any{"role": role, "content": content}
}

func followUp(model string, stream bool, c chatCompletion, results ...string) map[string]any {
	calls := c.Choices[0].Message.ToolCalls
	assistant := map[string]any{"role": "assistant", "content": nil, "tool_calls": calls}
	messages := []map[string]any{msg("user", "What's the weather in Rome?"), assistant}
	for i, call := range calls {
		messages = append(messages, map[string]any{"role": "tool", "tool_call_id": call.ID, "content": results[i]})
	}
	return toolsBody(model, stream, messages...)
}

func TestToolCallsRoundTrip(t *testing.T) {
	h := newHarness(t, harnessOpts{env: []string{"FAKE_SCENARIO=tools"}})
	resp := h.post(toolsBody("bridge/sonnet", false, msg("user", "What's the weather in Rome?")), nil)
	if resp.StatusCode != 200 {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("status %d: %s", resp.StatusCode, b)
	}
	c := decode[chatCompletion](t, resp)
	m := c.Choices[0].Message
	if c.Choices[0].FinishReason != "tool_calls" || len(m.ToolCalls) != 1 || m.text() != "Calling functions." {
		t.Fatalf("first response %+v", c.Choices[0])
	}
	call := m.ToolCalls[0]
	if !strings.HasPrefix(call.ID, "call_") || call.Type != "function" || call.Function.Name != "get_weather" ||
		call.Function.Arguments != `{"city":"Rome"}` {
		t.Fatalf("tool call %+v", call)
	}

	// Plain OpenAI follow-up: no session header, only the tool result.
	resp = h.post(followUp("bridge/sonnet", false, c, "sunny, 24C"), nil)
	c2 := decode[chatCompletion](t, resp)
	if c2.Choices[0].FinishReason != "stop" || c2.Choices[0].Message.text() != "results: sunny, 24C" ||
		len(c2.Choices[0].Message.ToolCalls) != 0 {
		t.Fatalf("final %+v", c2.Choices[0])
	}
	if n := len(h.starts()); n != 1 {
		t.Fatalf("the paused process must be resumed, got %d processes", n)
	}
	if v, _ := argValue(h.starts()[0].Args, "--tools"); v != "" {
		t.Fatal("built-in model must stay chat-only")
	}
	if v, _ := argValue(h.starts()[0].Args, "--allowedTools"); v != "mcp__functions" {
		t.Fatalf("args %q", h.starts()[0].Args)
	}
	waitFor(t, func() bool { return h.limiter.Active() == 0 })
}

func TestToolCallsParallelAndContentNull(t *testing.T) {
	calls := `[{"name":"get_weather","arguments":{"city":"Rome"}},{"name":"get_weather","arguments":{"city":"Paris"}}]`
	h := newHarness(t, harnessOpts{env: []string{"FAKE_SCENARIO=tools", "FAKE_TOOL_CALLS=" + calls, "FAKE_TOOL_PARALLEL=1", "FAKE_QUIET=1"}})
	resp := h.post(toolsBody("coder", false, msg("user", "Rome and Paris?")), nil)
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	var c chatCompletion
	if err := json.Unmarshal(body, &c); err != nil {
		t.Fatal(err)
	}
	if len(c.Choices[0].Message.ToolCalls) != 2 || !strings.Contains(string(body), `"content":null`) {
		t.Fatalf("response %s", body)
	}
	byCity := map[string]string{`{"city":"Rome"}`: "R", `{"city":"Paris"}`: "P"}
	tc := c.Choices[0].Message.ToolCalls
	c2 := decode[chatCompletion](t, h.post(followUp("coder", false, c, byCity[tc[0].Function.Arguments], byCity[tc[1].Function.Arguments]), nil))
	if c2.Choices[0].Message.text() != "results: R|P" {
		t.Fatalf("final %q", c2.Choices[0].Message.text())
	}
}

func TestToolCallsStream(t *testing.T) {
	h := newHarness(t, harnessOpts{env: []string{"FAKE_SCENARIO=tools"}})
	resp := h.post(toolsBody("coder", true, msg("user", "What's the weather in Rome?")), nil)
	evs := sseEvents(t, resp.Body)
	resp.Body.Close()
	var data []string
	for _, e := range evs {
		if strings.HasPrefix(e, "data: ") {
			data = append(data, e)
		}
	}
	// role, content, tool call, finish, [DONE]
	if len(data) != 5 || data[4] != "data: [DONE]" {
		t.Fatalf("events %q", evs)
	}
	if chunkOf(t, data[1]).Choices[0].Delta.Content != "Calling functions." {
		t.Fatalf("content chunk %s", data[1])
	}
	if !strings.Contains(data[2], `"tool_calls":[{"index":0,"id":"call_`) {
		t.Fatalf("tool call chunk %s", data[2])
	}
	tc := chunkOf(t, data[2]).Choices[0].Delta.ToolCalls[0]
	if fr := chunkOf(t, data[3]).Choices[0].FinishReason; fr == nil || *fr != "tool_calls" {
		t.Fatalf("finish %s", data[3])
	}

	c := chatCompletion{Choices: []choice{{Message: chatMessage{ToolCalls: []toolCall{{ID: tc.ID, Type: "function", Function: tc.Function}}}}}}
	resp = h.post(followUp("coder", true, c, "sunny"), nil)
	evs = sseEvents(t, resp.Body)
	resp.Body.Close()
	var text strings.Builder
	for _, e := range evs {
		if strings.HasPrefix(e, "data: {") {
			text.WriteString(chunkOf(t, e).Choices[0].Delta.Content)
		}
	}
	if text.String() != "results: sunny" {
		t.Fatalf("final stream %q", evs)
	}
}

func TestToolResultsAfterTimeoutStartFromHistory(t *testing.T) {
	h := newHarness(t, harnessOpts{env: []string{"FAKE_SCENARIO=tools"}, toolWait: 300 * time.Millisecond})
	c := decode[chatCompletion](t, h.post(toolsBody("coder", false, msg("user", "What's the weather in Rome?")), nil))
	waitFor(t, func() bool { return h.limiter.Active() == 0 })
	if !strings.Contains(h.logs.String(), "no function results received in time") {
		t.Fatal("timeout not logged")
	}
	// The new process sees the whole conversation, including the result.
	h.cfg.Agents["coder"].Timeout = 30 * time.Second
	body := followUp("coder", false, c, "sunny")
	body["tools"] = []any{} // simulate a client that stops offering functions
	c2 := decode[chatCompletion](t, h.post(body, nil))
	text := c2.Choices[0].Message.text()
	if !strings.HasPrefix(text, "no functions; prompt: ") || !strings.Contains(text, `name="get_weather">sunny</tool>`) ||
		!strings.Contains(text, `<tool_call id="`+c.Choices[0].Message.ToolCalls[0].ID) {
		t.Fatalf("fallback answer %q", text)
	}
	if len(h.starts()) != 2 {
		t.Fatalf("processes %d", len(h.starts()))
	}
}

func TestLegacyFunctions(t *testing.T) {
	h := newHarness(t, harnessOpts{env: []string{"FAKE_SCENARIO=tools"}})
	body := map[string]any{
		"model":         "coder",
		"functions":     []any{weatherTool["function"]},
		"function_call": "auto",
		"messages":      []map[string]any{msg("user", "Rome?")},
	}
	c := decode[chatCompletion](t, h.post(body, nil))
	m := c.Choices[0].Message
	if c.Choices[0].FinishReason != "function_call" || m.FunctionCall == nil || m.FunctionCall.Name != "get_weather" ||
		len(m.ToolCalls) != 0 {
		t.Fatalf("legacy response %+v", c.Choices[0])
	}
	waitFor(t, func() bool { return h.limiter.Active() == 0 })
	body["messages"] = []map[string]any{
		msg("user", "Rome?"),
		{"role": "assistant", "content": nil, "function_call": m.FunctionCall},
		{"role": "function", "name": "get_weather", "content": "sunny"},
	}
	c2 := decode[chatCompletion](t, h.post(body, nil))
	if c2.Choices[0].FinishReason != "function_call" && c2.Choices[0].FinishReason != "stop" {
		t.Fatalf("legacy follow-up %+v", c2.Choices[0])
	}
	if len(h.starts()) != 2 || !strings.Contains(h.starts()[1].Stdin, `name="get_weather">sunny</tool>`) {
		t.Fatalf("follow-up prompt %q", h.starts()[1].Stdin)
	}
}

func TestFunctionValidation(t *testing.T) {
	h := newHarness(t, harnessOpts{})
	base := func(extra map[string]any) map[string]any {
		b := userMsg("coder", "x")
		for k, v := range extra {
			b[k] = v
		}
		return b
	}
	fn := func(name string) map[string]any {
		return map[string]any{"type": "function", "function": map[string]any{"name": name}}
	}
	cases := []map[string]any{
		{"tools": []any{map[string]any{"type": "retrieval"}}},
		{"tools": []any{fn("bad name")}},
		{"tools": []any{fn("a"), fn("a")}},
		{"tools": []any{map[string]any{"type": "function", "function": map[string]any{"name": "a", "parameters": "x"}}}},
		{"tool_choice": "auto"},
		{"tools": []any{fn("a")}, "tool_choice": "sometimes"},
		{"tools": []any{fn("a")}, "tool_choice": map[string]any{"type": "function", "function": map[string]any{"name": "b"}}},
		{"tools": []any{fn("a")}, "tool_choice": 42},
		{"functions": []any{map[string]any{"name": "a"}}, "function_call": "required"},
		{"functions": []any{map[string]any{"name": "a"}}, "function_call": map[string]any{"name": "b"}},
	}
	for i, extra := range cases {
		resp := h.post(base(extra), nil)
		if resp.StatusCode != http.StatusBadRequest {
			b, _ := io.ReadAll(resp.Body)
			t.Fatalf("case %d: status %d %s", i, resp.StatusCode, b)
		}
		resp.Body.Close()
	}
	for i, extra := range []map[string]any{
		{"tools": []any{fn("a")}, "tool_choice": "none"},
		{"tools": []any{fn("a")}, "tool_choice": "required", "parallel_tool_calls": false},
		{"tools": []any{fn("a")}, "tool_choice": map[string]any{"type": "function", "function": map[string]any{"name": "a"}}},
		{"functions": []any{map[string]any{"name": "a"}}, "function_call": map[string]any{"name": "a"}},
	} {
		resp := h.post(base(extra), nil)
		if resp.StatusCode != http.StatusOK {
			b, _ := io.ReadAll(resp.Body)
			t.Fatalf("valid case %d: status %d %s", i, resp.StatusCode, b)
		}
		resp.Body.Close()
	}
}

func TestStatelessToolModeHeader(t *testing.T) {
	h := newHarness(t, harnessOpts{env: []string{"FAKE_SCENARIO=tools"}})
	hdr := map[string]string{headerToolMode: "stateless"}
	c := decode[chatCompletion](t, h.post(toolsBody("bridge/sonnet", false, msg("user", "What's the weather in Rome?")), hdr))
	if c.Choices[0].FinishReason != "tool_calls" {
		t.Fatalf("first step %+v", c.Choices[0])
	}
	waitFor(t, func() bool { return h.limiter.Active() == 0 })
	c2 := decode[chatCompletion](t, h.post(followUp("bridge/sonnet", false, c, "sunny"), hdr))
	// The second step is a new process; the fake CLI calls the function
	// again since it always does, so another tool_calls step is fine: what
	// matters is that the first process did not survive and the history
	// reached the new one.
	starts := h.starts()
	if len(starts) != 2 || !strings.Contains(starts[1].Stdin, `name="get_weather">sunny</tool>`) {
		t.Fatalf("second step %+v, prompt %q", c2.Choices[0], starts[len(starts)-1].Stdin)
	}
	if !strings.Contains(h.logs.String(), "outcome=tool_calls") {
		t.Fatal("stateless step not recorded as tool_calls")
	}
	expectError(t, h.post(toolsBody("coder", false, msg("user", "x")), map[string]string{headerToolMode: "sometimes"}), 400, errInvalidRequest)
}

func TestStatelessToolModeConfig(t *testing.T) {
	h := newHarness(t, harnessOpts{env: []string{"FAKE_SCENARIO=tools"}, configJSON: `"client_tools": {"mode": "stateless"}`})
	resp := h.post(toolsBody("coder", false, msg("user", "x")), nil)
	resp.Body.Close()
	waitFor(t, func() bool { return h.limiter.Active() == 0 })
	if !strings.Contains(h.logs.String(), "outcome=tool_calls") {
		t.Fatal("config mode not applied")
	}
}

func TestSystemModeHeaderAndAnyModel(t *testing.T) {
	h := newHarness(t, harnessOpts{})
	body := map[string]any{"model": "bridge/model-x-2[1m]", "messages": []map[string]any{
		msg("system", "You are the planner agent."), msg("user", "plan it"),
	}}
	resp := h.post(body, map[string]string{headerSystemMode: "replace"})
	if resp.StatusCode != 200 {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("status %d %s", resp.StatusCode, b)
	}
	c := decode[chatCompletion](t, resp)
	if c.Model != "bridge/model-x-2[1m]" {
		t.Fatalf("model %q", c.Model)
	}
	r := h.starts()[0]
	if v, _ := argValue(r.Args, "--system-prompt"); v != "You are the planner agent." {
		t.Fatalf("args %q", r.Args)
	}
	if v, _ := argValue(r.Args, "--model"); v != "model-x-2[1m]" {
		t.Fatalf("model arg %q", r.Args)
	}
	if v, ok := argValue(r.Args, "--tools"); !ok || v != "" {
		t.Fatal("any-model agents must be chat-only")
	}
	if r.Stdin != "plan it" {
		t.Fatalf("stdin %q", r.Stdin)
	}
	m := decode[modelObject](t, h.get("/v1/models/bridge/model-x-2[1m]", nil))
	if m.ID != "bridge/model-x-2[1m]" {
		t.Fatalf("model detail %+v", m)
	}
	expectError(t, h.post(userMsg("bridge/bad name", "x"), nil), 404, errModelNotFound)
	expectError(t, h.post(body, map[string]string{headerSystemMode: "inline"}), 400, errInvalidRequest)

	h2 := newHarness(t, harnessOpts{configJSON: `"builtin_any_model": false, "system_messages": "append"`})
	expectError(t, h2.post(userMsg("bridge/model-x", "x"), nil), 404, errModelNotFound)
	resp = h2.post(map[string]any{"model": "bridge/haiku", "messages": body["messages"]}, nil)
	resp.Body.Close()
	if v, _ := argValue(h2.starts()[0].Args, "--append-system-prompt"); v != "You are the planner agent." {
		t.Fatalf("config system_messages not applied: %q", h2.starts()[0].Args)
	}
}
