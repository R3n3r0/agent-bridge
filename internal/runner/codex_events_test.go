package runner

import (
	"encoding/json"
	"testing"
)

func jsonUnquote(s string, v *string) error { return json.Unmarshal([]byte(s), v) }

func TestCodexParserTurn(t *testing.T) {
	p := CodexBackend{}.NewParser()
	lines := []string{
		`{"type":"thread.started","thread_id":"t1"}`,
		`{"type":"turn.started"}`,
		`{"type":"item.completed","item":{"id":"i0","type":"error","message":"model metadata not found"}}`,
		`{"type":"item.started","item":{"id":"i1","type":"command_execution","command":"ls"}}`,
		`{"type":"item.started","item":{"id":"i2","type":"mcp_tool_call","server":"functions","tool":"get_weather"}}`,
		`{"type":"item.started","item":{"id":"i3","type":"todo_list"}}`,
		`{"type":"item.completed","item":{"id":"i4","type":"reasoning","text":"thinking"}}`,
		`{"type":"item.completed","item":{"id":"i5","type":"agent_message","text":"first"}}`,
		`{"type":"item.completed","item":{"id":"i6","type":"agent_message","text":"final"}}`,
		`{"type":"error","message":"Reconnecting... 1/5"}`,
		`not json`,
		`{"type":"turn.completed","usage":{"input_tokens":100,"cached_input_tokens":40,"cache_write_input_tokens":5,"output_tokens":7}}`,
	}
	var kinds []string
	var events []Event
	for _, l := range lines {
		for _, ev := range p.Parse([]byte(l)) {
			kinds = append(kinds, ev.Kind)
			events = append(events, ev)
		}
	}
	want := []string{KindInit, KindIgnored, KindIgnored, KindToolUse, KindToolUse, KindIgnored, KindIgnored,
		KindText, KindText, KindIgnored, KindMalformed, KindUsage, KindResult}
	if len(kinds) != len(want) {
		t.Fatalf("kinds %v", kinds)
	}
	for i := range want {
		if kinds[i] != want[i] {
			t.Fatalf("kinds %v, want %v", kinds, want)
		}
	}
	if events[2].Text != "model metadata not found" || events[2].Type != "warning" || events[4].ToolName != "mcp:functions/get_weather" {
		t.Fatalf("events %+v", events[2:5])
	}
	res := events[len(events)-1].Result
	if res.Result != "final" || res.SessionID != "t1" || res.Subtype != SubtypeSuccess || res.IsError ||
		res.Usage.InputTokens != 60 || res.Usage.CacheReadTokens != 40 || res.Usage.CacheCreationTokens != 5 || res.Usage.OutputTokens != 7 {
		t.Fatalf("result %+v", res)
	}
}

func TestCodexParserFailure(t *testing.T) {
	p := CodexBackend{}.NewParser()
	p.Parse([]byte(`{"type":"thread.started","thread_id":"t2"}`))
	evs := p.Parse([]byte(`{"type":"turn.failed","error":{"message":"rate limited"}}`))
	if len(evs) != 1 || evs[0].Result == nil || !evs[0].Result.IsError || evs[0].Result.Result != "rate limited" || evs[0].SessionID != "t2" {
		t.Fatalf("events %+v", evs)
	}
	evs = p.Parse([]byte(`{"type":"turn.failed"}`))
	if evs[0].Result.Result != "turn failed" {
		t.Fatalf("events %+v", evs[0].Result)
	}
	if evs := p.Parse([]byte(`{"type":"item.completed"}`)); evs[0].Kind != KindIgnored {
		t.Fatal("item without body")
	}
	if evs := p.Parse([]byte(`{"type":"item.completed","item":{"type":"agent_message","text":""}}`)); evs[0].Kind != KindIgnored {
		t.Fatal("empty message")
	}
}

func TestTOMLEncoding(t *testing.T) {
	if got := tomlString("a<b>&\"\\\n\x01é"); got != `"a<b>&\"\\\n\u0001é"` {
		t.Fatalf("string %s", got)
	}
	if got := tomlStrings(nil); got != "[]" {
		t.Fatalf("empty array %s", got)
	}
	if got := tomlTable(map[string]string{"b": "2", "a": "1"}); got != `{"a" = "1", "b" = "2"}` {
		t.Fatalf("table %s", got)
	}
}
