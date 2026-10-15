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
		"<conversation_history>\n<user>first</user>\n<assistant>answer one</assistant>\n<tool>tool output</tool>\n</conversation_history>\n\n" +
		"<current_request>\nsecond\n\nthird\n</current_request>"
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
	msgs := []Message{{RoleSystem, "a"}, {RoleUser, "u"}, {RoleDeveloper, "b"}}
	if got := Instructions(msgs); got != "a\n\nb" {
		t.Fatalf("got %q", got)
	}
}
