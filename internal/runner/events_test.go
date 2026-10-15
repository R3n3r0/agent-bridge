package runner

import "testing"

func TestParseLine(t *testing.T) {
	ev := ParseLine([]byte(`{"type":"system","subtype":"init","session_id":"s1","model":"model-x","apiKeySource":"none"}`))
	if len(ev) != 1 || ev[0].Kind != KindInit || ev[0].SessionID != "s1" || ev[0].Model != "model-x" || ev[0].APIKeySource != "none" {
		t.Fatalf("init: %+v", ev)
	}

	ev = ParseLine([]byte(`{"type":"assistant","session_id":"s1","message":{"content":[
		{"type":"text","text":"hello"},{"type":"tool_use","name":"Bash"},{"type":"text","text":""},{"type":"thinking"}]}}`))
	if len(ev) != 2 || ev[0].Kind != KindText || ev[0].Text != "hello" || ev[1].Kind != KindToolUse || ev[1].ToolName != "Bash" {
		t.Fatalf("assistant: %+v", ev)
	}

	ev = ParseLine([]byte(`{"type":"result","subtype":"success","is_error":false,"result":"done","session_id":"s2",
		"total_cost_usd":0.5,"num_turns":3,"duration_ms":1200,
		"usage":{"input_tokens":1,"output_tokens":2,"cache_read_input_tokens":3,"cache_creation_input_tokens":4}}`))
	if len(ev) != 1 || ev[0].Kind != KindResult {
		t.Fatalf("result: %+v", ev)
	}
	r := ev[0].Result
	if r.Subtype != "success" || r.Result != "done" || r.SessionID != "s2" || r.CostUSD != 0.5 || r.NumTurns != 3 ||
		r.DurationMs != 1200 || r.Usage != (Usage{1, 2, 3, 4}) || ev[0].SessionID != "s2" {
		t.Fatalf("result fields: %+v", r)
	}

	for _, line := range []string{`not json`, `{"type":"result","num_turns":"x"}`} {
		if ev := ParseLine([]byte(line)); len(ev) != 1 || ev[0].Kind != KindMalformed {
			t.Fatalf("%s: %+v", line, ev)
		}
	}
	for _, line := range []string{
		`{"type":"user"}`, `{"type":"future"}`, `{"type":"system","subtype":"other"}`,
		`{"type":"assistant"}`, `{"type":"assistant","message":{"content":[]}}`,
	} {
		if ev := ParseLine([]byte(line)); len(ev) != 1 || ev[0].Kind != KindIgnored {
			t.Fatalf("%s: %+v", line, ev)
		}
	}
}

func TestLineWriter(t *testing.T) {
	var lines []string
	w := &lineWriter{max: 10, fn: func(b []byte) { lines = append(lines, string(b)) }}
	_, _ = w.Write([]byte("ab"))
	_, _ = w.Write([]byte("c\n\n  \nde"))
	_, _ = w.Write([]byte("f\n0123456789AB\nok"))
	w.flush()
	want := []string{"abc", "def", "\x00overflow", "ok"}
	if len(lines) != len(want) {
		t.Fatalf("got %q", lines)
	}
	for i := range want {
		if lines[i] != want[i] {
			t.Fatalf("got %q", lines)
		}
	}
}

func TestTailBuffer(t *testing.T) {
	b := &tailBuffer{max: 4}
	_, _ = b.Write([]byte("abc"))
	_, _ = b.Write([]byte("defg"))
	if b.String() != "defg" {
		t.Fatalf("got %q", b.String())
	}
}
