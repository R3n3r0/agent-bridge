package server

import (
	"context"
	"encoding/json"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/openai/openai-go"
	"github.com/openai/openai-go/option"
)

func genRequest(t *testing.T, body string) *chatRequest {
	t.Helper()
	var r chatRequest
	if err := json.Unmarshal([]byte(body), &r); err != nil {
		t.Fatal(err)
	}
	return &r
}

func TestParseGeneration(t *testing.T) {
	g, err := parseGeneration(genRequest(t, `{"n":3,"stop":["a","bb"],"max_tokens":10,"max_completion_tokens":20,
		"reasoning_effort":"minimal","response_format":{"type":"json_schema","json_schema":{"name":"x","schema":{ "type" : "object" }}}}`))
	if err != nil || g.n != 3 || len(g.stop) != 2 || g.maxTokens != 20 || g.effort != "minimal" || g.jsonSchema != `{"type":"object"}` {
		t.Fatalf("got %+v %v", g, err)
	}
	g, _ = parseGeneration(genRequest(t, `{"stop":"END","max_tokens":5,"reasoning_effort":"xhigh","response_format":{"type":"json_object"}}`))
	if g.n != 1 || g.stop[0] != "END" || g.maxTokens != 5 || g.effort != "xhigh" || g.jsonSchema != jsonObjectShape {
		t.Fatalf("got %+v", g)
	}
	g, _ = parseGeneration(genRequest(t, `{"response_format":{"type":"text"},"stop":null}`))
	if g.jsonSchema != "" || g.stop != nil {
		t.Fatalf("got %+v", g)
	}
	for _, bad := range []string{
		`{"n":0}`, `{"n":9}`, `{"stop":42}`, `{"stop":["a","b","c","d","e"]}`, `{"stop":[""]}`,
		`{"max_tokens":0}`, `{"max_completion_tokens":-1}`, `{"reasoning_effort":"extreme"}`,
		`{"response_format":{"type":"xml"}}`, `{"response_format":{"type":"json_schema"}}`,
		`{"response_format":{"type":"json_schema","json_schema":{"schema":[1]}}}`, `{"response_format":"json"}`,
		`{"response_format":{"type":"json_schema","json_schema":{"schema":{"d":"` + strings.Repeat("x", maxSchemaBytes) + `"}}}}`,
	} {
		if _, err := parseGeneration(genRequest(t, bad)); err == nil || err.Code != errInvalidRequest {
			t.Errorf("%.60s accepted", bad)
		}
	}
}

func TestApplyStop(t *testing.T) {
	cases := []struct {
		text  string
		stops []string
		want  string
	}{
		{"hello world END more", []string{"END"}, "hello world "},
		{"a b c", []string{"c", "b"}, "a "},
		{"nothing here", []string{"zz"}, "nothing here"},
		{"STOP at start", []string{"STOP"}, ""},
	}
	for _, c := range cases {
		if got := applyStop(c.text, c.stops); got != c.want {
			t.Errorf("%q %v: got %q", c.text, c.stops, got)
		}
	}
}

func TestGenerationParametersEndToEnd(t *testing.T) {
	h := newHarness(t, harnessOpts{})
	body := userMsg("bridge/sonnet", "q")
	body["reasoning_effort"] = "high"
	body["max_completion_tokens"] = 300
	body["temperature"] = 0.2
	body["stop"] = []string{"answer"}
	c := decode[chatCompletion](t, h.post(body, nil))
	if c.Choices[0].Message.text() != "Fake " || c.Choices[0].FinishReason != "stop" {
		t.Fatalf("stop: %+v", c.Choices[0])
	}
	r := h.starts()[0]
	if v, _ := argValue(r.Args, "--effort"); v != "high" {
		t.Fatalf("args %q", r.Args)
	}
	if !strings.Contains(strings.Join(r.Env, "\n"), "CLAUDE_CODE_MAX_OUTPUT_TOKENS=300") {
		t.Fatal("max tokens not passed")
	}

	body = userMsg("bridge/sonnet", "q")
	body["response_format"] = map[string]any{"type": "json_schema", "json_schema": map[string]any{
		"name": "answer", "schema": map[string]any{"type": "object", "properties": map[string]any{"answer": map[string]any{"type": "string"}}},
	}}
	body["stop"] = "answer" // never applied to structured output
	c = decode[chatCompletion](t, h.post(body, nil))
	if c.Choices[0].Message.text() != `{"answer":"Fake answer."}` {
		t.Fatalf("structured: %q", c.Choices[0].Message.text())
	}
	if v, _ := argValue(h.starts()[1].Args, "--json-schema"); !strings.Contains(v, `"answer"`) {
		t.Fatalf("schema arg %q", v)
	}

	fail := newHarness(t, harnessOpts{env: []string{"FAKE_STRUCTURED=fail"}})
	body = userMsg("bridge/sonnet", "q")
	body["response_format"] = map[string]any{"type": "json_object"}
	expectError(t, fail.post(body, nil), 502, errUpstream)
}

func TestMultipleChoices(t *testing.T) {
	h := newHarness(t, harnessOpts{})
	body := userMsg("bridge/sonnet", "q")
	body["n"] = 3
	resp := h.post(body, map[string]string{headerSessionID: "ignored-for-n"})
	if resp.Header.Get(headerTurns) != "6" || resp.Header.Get(headerCost) != "0.036900" {
		t.Fatalf("headers %v", resp.Header)
	}
	c := decode[chatCompletion](t, resp)
	if len(c.Choices) != 3 || c.Usage.PromptTokens != 45 || c.Usage.CompletionTokens != 15 {
		t.Fatalf("completion %+v", c)
	}
	for i, ch := range c.Choices {
		if ch.Index != i || ch.Message.text() != "Fake answer." {
			t.Fatalf("choice %d %+v", i, ch)
		}
	}
	starts := h.starts()
	if len(starts) != 3 {
		t.Fatalf("processes %d", len(starts))
	}
	for _, s := range starts {
		if _, ok := argValue(s.Args, "--resume"); ok {
			t.Fatal("choices must not resume sessions")
		}
	}

	// Streaming: every choice gets its role chunk, content and finish.
	body["stream"] = true
	body["stream_options"] = map[string]any{"include_usage": true}
	resp = h.post(body, nil)
	evs := sseEvents(t, resp.Body)
	resp.Body.Close()
	roles, finishes := map[int]bool{}, map[int]bool{}
	var usageTotal int64
	for _, e := range evs {
		if !strings.HasPrefix(e, "data: {") {
			continue
		}
		ch := chunkOf(t, e)
		if ch.Usage != nil {
			usageTotal = ch.Usage.TotalTokens
			continue
		}
		cc := ch.Choices[0]
		if cc.Delta.Role == "assistant" {
			roles[cc.Index] = true
		}
		if cc.FinishReason != nil {
			finishes[cc.Index] = true
		}
	}
	if len(roles) != 3 || len(finishes) != 3 || usageTotal != 60 || evs[len(evs)-1] != "data: [DONE]" {
		t.Fatalf("stream roles=%v finishes=%v usage=%d events=%q", roles, finishes, usageTotal, evs)
	}

	// An error in one choice fails the whole request.
	bad := newHarness(t, harnessOpts{env: []string{"FAKE_SCENARIO=exec_error"}})
	body = userMsg("bridge/sonnet", "q")
	body["n"] = 2
	expectError(t, bad.post(body, nil), 502, errUpstream)
}

func TestMultipleChoicesWithToolsAreStateless(t *testing.T) {
	h := newHarness(t, harnessOpts{env: []string{"FAKE_SCENARIO=tools"}})
	body := toolsBody("bridge/sonnet", false, msg("user", "weather?"))
	body["n"] = 2
	resp := h.post(body, nil)
	data, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	var c chatCompletion
	if err := json.Unmarshal(data, &c); err != nil || len(c.Choices) != 2 || c.Choices[1].FinishReason != "tool_calls" {
		t.Fatalf("response %s", data)
	}
	waitFor(t, func() bool { return h.limiter.Active() == 0 })
}

func TestOfficialOpenAIClientGenerationParams(t *testing.T) {
	h := newHarness(t, harnessOpts{})
	client := openai.NewClient(option.WithBaseURL(h.ts.URL+"/v1/"), option.WithAPIKey("unused"), option.WithMaxRetries(0))
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	completion, err := client.Chat.Completions.New(ctx, openai.ChatCompletionNewParams{
		Model:           "bridge/sonnet",
		Messages:        []openai.ChatCompletionMessageParamUnion{openai.UserMessage("q")},
		N:               openai.Int(2),
		ReasoningEffort: openai.ReasoningEffortLow,
		ResponseFormat: openai.ChatCompletionNewParamsResponseFormatUnion{
			OfJSONObject: &openai.ResponseFormatJSONObjectParam{},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(completion.Choices) != 2 || completion.Choices[1].Message.Content != `{"answer":"Fake answer."}` {
		t.Fatalf("completion %+v", completion.Choices)
	}
}
