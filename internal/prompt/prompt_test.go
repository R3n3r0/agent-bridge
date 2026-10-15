package prompt

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func raw(role string, content any) RawMessage {
	b, _ := json.Marshal(content)
	return RawMessage{Role: role, Content: b}
}

func mustParse(t *testing.T, msgs ...RawMessage) []Message {
	t.Helper()
	m, err := Parse(msgs)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	return m
}

func TestParseContentForms(t *testing.T) {
	msgs := mustParse(t,
		raw("system", "sys"),
		raw("user", []map[string]string{{"type": "text", "text": "a"}, {"type": "text", "text": "b"}}),
		RawMessage{Role: "assistant", Content: json.RawMessage("null")},
		RawMessage{Role: "tool"},
	)
	if msgs[0].Text != "sys" || msgs[1].Text != "a\nb" || msgs[2].Text != "" || msgs[3].Text != "" {
		t.Fatalf("unexpected texts: %+v", msgs)
	}
}

func TestParseErrors(t *testing.T) {
	cases := []struct {
		name string
		msgs []RawMessage
		want error
	}{
		{"empty", nil, ErrNoMessages},
		{"no user", []RawMessage{raw("system", "x")}, ErrNoUserMessage},
		{"bad role", []RawMessage{raw("robot", "x")}, ErrInvalidRole},
		{"image", []RawMessage{raw("user", []map[string]any{{"type": "image_url", "image_url": map[string]string{"url": "x"}}})}, ErrUnsupportedContent},
		{"audio", []RawMessage{raw("user", []map[string]any{{"type": "text", "text": "hi"}, {"type": "input_audio"}})}, ErrUnsupportedContent},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := Parse(c.msgs)
			if !errors.Is(err, c.want) {
				t.Fatalf("got %v, want %v", err, c.want)
			}
		})
	}
	if _, err := Parse([]RawMessage{raw("user", 42)}); err == nil {
		t.Fatal("number content accepted")
	}
	if _, err := Parse([]RawMessage{{Role: "user", Content: json.RawMessage(`"unterminated`)}}); err == nil {
		t.Fatal("invalid string accepted")
	}
	if _, err := Parse([]RawMessage{{Role: "user", Content: json.RawMessage(`[1]`)}}); err == nil {
		t.Fatal("invalid parts accepted")
	}
}

func TestBuildSingleUserMessage(t *testing.T) {
	msgs := mustParse(t, raw("user", "hello"))
	got, err := Build(msgs, false)
	if err != nil || got != "hello" {
		t.Fatalf("got %q, %v", got, err)
	}
}

func TestBuildNewSessionFull(t *testing.T) {
	msgs := mustParse(t,
		raw("system", "be brief"),
		raw("developer", "use Go"),
		raw("user", "first"),
		raw("assistant", "answer one"),
		raw("tool", "tool output"),
		raw("user", "second"),
		raw("user", "third"),
	)
	got, err := Build(msgs, false)
	if err != nil {
		t.Fatal(err)
	}
	want := "<instructions>\nbe brief\n\nuse Go\n</instructions>\n\n" +
		"<conversation_history>\n<user>first</user>\n<assistant>answer one</assistant>\n</conversation_history>\n\n" +
		"<current_request>\n<tool>tool output</tool>\n\nsecond\n\nthird\n</current_request>"
	if got != want {
		t.Fatalf("got:\n%s\nwant:\n%s", got, want)
	}
}

func TestBuildOmitsEmptySections(t *testing.T) {
	msgs := mustParse(t, raw("system", "rules"), raw("user", "q"))
	got, _ := Build(msgs, false)
	want := "<instructions>\nrules\n</instructions>\n\n<current_request>\nq\n</current_request>"
	if got != want {
		t.Fatalf("got %q", got)
	}

	msgs = mustParse(t, raw("user", "q1"), raw("user", "q2"))
	got, _ = Build(msgs, false)
	want = "<current_request>\nq1\n\nq2\n</current_request>"
	if got != want {
		t.Fatalf("got %q", got)
	}

	msgs = mustParse(t, raw("user", "q1"), raw("assistant", "a1"), raw("user", "q2"))
	got, _ = Build(msgs, false)
	want = "<conversation_history>\n<user>q1</user>\n<assistant>a1</assistant>\n</conversation_history>\n\n<current_request>\nq2\n</current_request>"
	if got != want {
		t.Fatalf("got %q", got)
	}
}

func TestBuildResumed(t *testing.T) {
	msgs := mustParse(t,
		raw("system", "ignored"),
		raw("user", "old"),
		raw("assistant", "reply"),
		raw("user", "new one"),
		raw("user", "new two"),
	)
	got, err := Build(msgs, true)
	if err != nil || got != "new one\n\nnew two" {
		t.Fatalf("got %q, %v", got, err)
	}
}

func TestBuildNoCurrentRequest(t *testing.T) {
	msgs := mustParse(t, raw("user", "q"), raw("assistant", "a"))
	if _, err := Build(msgs, false); !errors.Is(err, ErrNoCurrentRequest) {
		t.Fatalf("got %v", err)
	}
	if _, err := Build(msgs, true); !errors.Is(err, ErrNoCurrentRequest) {
		t.Fatalf("got %v", err)
	}
}

func TestBuildTooLarge(t *testing.T) {
	big := strings.Repeat("x", MaxBytes+1)
	msgs := []Message{{Role: RoleUser, Text: big}}
	if _, err := Build(msgs, false); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("got %v", err)
	}
	if _, err := Build(msgs, true); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("got %v", err)
	}
	ok := []Message{{Role: RoleUser, Text: strings.Repeat("x", MaxBytes)}}
	if _, err := Build(ok, false); err != nil {
		t.Fatalf("limit-sized prompt rejected: %v", err)
	}
}

func TestInstructions(t *testing.T) {
	msgs := []Message{{Role: RoleSystem, Text: "a"}, {Role: RoleUser, Text: "u"}, {Role: RoleDeveloper, Text: "b"}}
	if got := Instructions(msgs); got != "a\n\nb" {
		t.Fatalf("got %q", got)
	}
}

func toolConversation(t *testing.T) []Message {
	t.Helper()
	call := func(id, name, args string) RawToolCall {
		var c RawToolCall
		c.ID, c.Type, c.Function.Name, c.Function.Arguments = id, "function", name, args
		return c
	}
	return mustParse(t,
		raw("user", "weather in Rome and Paris?"),
		RawMessage{Role: "assistant", Content: json.RawMessage("null"), ToolCalls: []RawToolCall{
			call("call_1", "get_weather", `{"city":"Rome"}`), call("call_2", "get_weather", `{"city":"Paris"}`)}},
		RawMessage{Role: "tool", ToolCallID: "call_1", Content: json.RawMessage(`"sunny"`)},
		RawMessage{Role: "function", Name: "get_weather", ToolCallID: "call_2", Content: json.RawMessage(`"rain"`)},
	)
}

func TestToolMessages(t *testing.T) {
	msgs := toolConversation(t)
	if msgs[1].ToolCalls[1].Arguments != `{"city":"Paris"}` || msgs[2].Name != "get_weather" || msgs[3].Role != RoleTool {
		t.Fatalf("parsed %+v", msgs)
	}
	res := ToolResults(msgs)
	if len(res) != 2 || res["call_1"] != "sunny" || res["call_2"] != "rain" {
		t.Fatalf("results %v", res)
	}
	if len(CurrentRequest(msgs)) != 0 {
		t.Fatal("tool results are not user requests")
	}

	got, err := Build(msgs, true)
	want := `<tool tool_call_id="call_1" name="get_weather">sunny</tool>` + "\n\n" +
		`<tool tool_call_id="call_2" name="get_weather">rain</tool>`
	if err != nil || got != want {
		t.Fatalf("resumed %q, %v", got, err)
	}

	got, _ = Build(msgs, false)
	want = "<conversation_history>\n<user>weather in Rome and Paris?</user>\n" +
		`<assistant><tool_call id="call_1" name="get_weather">{"city":"Rome"}</tool_call>` + "\n" +
		`<tool_call id="call_2" name="get_weather">{"city":"Paris"}</tool_call></assistant>` + "\n" +
		"</conversation_history>\n\n<current_request>\n" +
		`<tool tool_call_id="call_1" name="get_weather">sunny</tool>` + "\n\n" +
		`<tool tool_call_id="call_2" name="get_weather">rain</tool>` + "\n</current_request>"
	if got != want {
		t.Fatalf("new session:\n%s\nwant:\n%s", got, want)
	}
}

func TestAssistantTextAndToolCall(t *testing.T) {
	m := Message{Role: RoleAssistant, Text: "checking", ToolCalls: []ToolCall{{ID: "c", Name: "f", Arguments: "{}"}}}
	if got := renderAssistant(m); got != "<assistant>checking\n<tool_call id=\"c\" name=\"f\">{}</tool_call></assistant>" {
		t.Fatalf("got %q", got)
	}
}

func TestLegacyFunctionCall(t *testing.T) {
	msgs := mustParse(t,
		raw("user", "q"),
		RawMessage{Role: "assistant", FunctionCall: &struct {
			Name      string `json:"name"`
			Arguments string `json:"arguments"`
		}{"f", "{}"}},
		RawMessage{Role: "function", Name: "f", Content: json.RawMessage(`"r"`)},
	)
	got, _ := Build(msgs, false)
	if !strings.Contains(got, `<assistant><tool_call name="f">{}</tool_call></assistant>`) ||
		!strings.Contains(got, `<tool tool_call_id="" name="f">r</tool>`) {
		t.Fatalf("got %s", got)
	}
}

func TestBuildOmitInstructions(t *testing.T) {
	msgs := mustParse(t, raw("system", "agent role"), raw("developer", "more"), raw("user", "q"))
	got, err := BuildWith(msgs, Options{OmitInstructions: true})
	if err != nil || got != "q" {
		t.Fatalf("got %q %v", got, err)
	}
	msgs = mustParse(t, raw("system", "agent role"), raw("user", "q1"), raw("assistant", "a1"), raw("user", "q2"))
	got, _ = BuildWith(msgs, Options{OmitInstructions: true})
	if strings.Contains(got, "agent role") || strings.Contains(got, "<instructions>") || !strings.Contains(got, "<current_request>\nq2") {
		t.Fatalf("got %q", got)
	}
	got, _ = BuildWith(msgs, Options{OmitInstructions: true, Resumed: true})
	if got != "q2" {
		t.Fatalf("resumed %q", got)
	}
}
