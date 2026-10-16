package runner

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/r3n3r0/agent-bridge/internal/config"
)

func TestKimiPrompt(t *testing.T) {
	blocks := kimiPrompt(Spec{SystemPrompt: "be kind", JSONSchema: `{"type":"object","properties":{"a":{}}}`}, "/compact now",
		[]ImageBlock{{MediaType: "image/png", Data: []byte("x")}})
	text := blocks[0].Text
	if !strings.HasPrefix(text, "<system_instructions>\nbe kind\n</system_instructions>\n\n/compact now") ||
		!strings.Contains(text, `matching this JSON schema`) {
		t.Fatalf("text %q", text)
	}
	if len(blocks) != 2 || blocks[1].Type != "image" || blocks[1].MimeType != "image/png" || blocks[1].Data != "eA==" {
		t.Fatalf("blocks %+v", blocks)
	}
	resumed := kimiPrompt(Spec{SystemPrompt: "be kind", ResumeID: "s", JSONSchema: jsonObjectSchema}, "/help", nil)
	if got := resumed[0].Text; !strings.HasPrefix(got, " /help") || !strings.Contains(got, jsonObjectInstruction) {
		t.Fatalf("resumed %q", got)
	}
}

func TestKimiPolicy(t *testing.T) {
	cases := []struct {
		spec Spec
		tool string
		want bool
	}{
		{Spec{ClientTools: "/x"}, "mcp__functions__get_weather", true},
		{Spec{}, "mcp__functions__get_weather", false},
		{Spec{}, "Bash", false},
		{Spec{ToolsEnabled: true, AllowedTools: []string{"Bash"}}, "Bash", true},
		{Spec{ToolsEnabled: true, AllowedTools: []string{"Bash"}}, "Write", false},
		{Spec{ToolsEnabled: true, PermissionMode: "auto"}, "Write", true},
		{Spec{ToolsEnabled: true, PermissionMode: "auto", DisallowedTools: []string{"Write"}}, "Write", false},
	}
	for i, c := range cases {
		if got := kimiPolicy(c.spec)(c.tool); got != c.want {
			t.Errorf("case %d: %s = %v", i, c.tool, got)
		}
	}
}

func TestKimiOptions(t *testing.T) {
	var opts []acpConfigOption
	_ = json.Unmarshal([]byte(`[
		{"id":"model","currentValue":"m1","options":[{"value":"m1","name":"Kimi One"},{"value":"m2","name":"kimi-k2"}]},
		{"id":"thinking","currentValue":"off","options":[{"value":"off"},{"value":"low"},{"value":"high"}]},
		{"id":"mode","currentValue":"default","options":[]}]`), &opts)
	if v, err := modelValue(opts, "KIMI-K2"); err != nil || v != "m2" {
		t.Fatalf("model %q %v", v, err)
	}
	if _, err := modelValue(opts, "other"); err == nil || !strings.Contains(err.Error(), "Kimi One, kimi-k2") {
		t.Fatalf("err %v", err)
	}
	if v, _ := modelValue(nil, "x"); v != "x" {
		t.Fatal("without options the model is passed as is")
	}
	if currentModelName(opts, "") != "Kimi One" || currentModelName(opts, "m2") != "kimi-k2" || currentModelName(nil, "z") != "z" {
		t.Fatal("currentModelName")
	}
	for effort, want := range map[string]string{"": "", "none": "off", "low": "low", "medium": "", "max": "high"} {
		if got := thinkingValue(opts, effort); got != want {
			t.Errorf("effort %q -> %q, want %q", effort, got, want)
		}
	}
	onOff := []acpConfigOption{{ID: "thinking", Options: opts[1].Options[:1]}}
	onOff[0].Options = append(onOff[0].Options, struct {
		Value string `json:"value"`
		Name  string `json:"name"`
	}{Value: "on"})
	if thinkingValue(onOff, "medium") != "on" || thinkingValue(onOff, "xhigh") != "on" || thinkingValue(nil, "high") != "" {
		t.Fatal("on/off thinking")
	}
	if currentValue(opts, "mode") != "default" || currentValue(opts, "nope") != "" {
		t.Fatal("currentValue")
	}
}

func TestKimiPrepare(t *testing.T) {
	dir := t.TempDir()
	mcp := filepath.Join(dir, "mcp.json")
	os.WriteFile(mcp, []byte(`{"mcpServers":{"web":{"url":"https://x","headers":{"A":"b"}},"fs":{"command":"fs","env":{"K":"v"}}}}`), 0o600)
	inv, err := KimiBackend{}.Prepare(Turn{
		Spec:        Spec{ToolsEnabled: true, MCPConfig: mcp, ClientTools: mcp, MaxTurns: 7, AddDirs: []string{"/a"}},
		Dir:         "/w",
		BaseEnv:     []string{"KIMI_DISABLE_TELEMETRY=0", "KIMI_LOOP_MAX_STEPS_PER_TURN=1", "KIMI_MODEL_API_KEY=k"},
		AuthMode:    config.AuthSubscription,
		ToolTimeout: time.Minute,
	})
	if err != nil {
		t.Fatal(err)
	}
	env := strings.Join(inv.Env, " ")
	for _, want := range []string{"KIMI_DISABLE_TELEMETRY=0", "KIMI_CODE_NO_AUTO_UPDATE=1", "KIMI_LOOP_MAX_STEPS_PER_TURN=7", "KIMI_MCP_TOOL_TIMEOUT_MS=60000"} {
		if !strings.Contains(env, want) {
			t.Errorf("missing %s in %s", want, env)
		}
	}
	if strings.Contains(env, "KIMI_MODEL_API_KEY") || strings.Contains(env, "STEPS_PER_TURN=1 ") {
		t.Fatalf("env %s", env)
	}
	d := inv.Parser.(*acpDialogue)
	b, _ := json.Marshal(d.turn.servers)
	if string(b) != `[{"name":"fs","command":"fs","args":[],"env":[{"name":"K","value":"v"}]},{"type":"http","name":"web","url":"https://x","headers":[{"name":"A","value":"b"}]},`+
		`{"name":"fs","command":"fs","args":[],"env":[{"name":"K","value":"v"}]},{"type":"http","name":"web","url":"https://x","headers":[{"name":"A","value":"b"}]}]` {
		t.Fatalf("servers %s", b)
	}
	if d.turn.cwd != "/w" || d.turn.mode != "default" || len(d.turn.addDirs) != 1 {
		t.Fatalf("turn %+v", d.turn)
	}
	inv, _ = KimiBackend{}.Prepare(Turn{Spec: Spec{AddDirs: []string{"/a"}, PermissionMode: "yolo"}})
	if d := inv.Parser.(*acpDialogue); d.turn.addDirs != nil || d.turn.mode != "default" {
		t.Fatalf("chat-only turn %+v", d.turn)
	}
	if _, err := (KimiBackend{}).Prepare(Turn{Spec: Spec{ClientTools: filepath.Join(dir, "none")}}); err == nil {
		t.Fatal("expected an error for a missing client tools file")
	}
	if _, err := (KimiBackend{}).Prepare(Turn{Spec: Spec{ToolsEnabled: true, MCPConfig: filepath.Join(dir, "none")}}); err == nil {
		t.Fatal("expected an error for a missing MCP file")
	}
}

type bufCloser struct {
	bytes.Buffer
	closed bool
}

func (b *bufCloser) Close() error { b.closed = true; return nil }

func TestACPDialogue(t *testing.T) {
	d := newACPDialogue(acpTurn{cwd: "/w", resumeID: "s1", mode: "plan", prompt: []acpBlock{{Type: "text", Text: "hi"}},
		permission: func(tool string) bool { return tool == "Read" }})
	w := &bufCloser{}
	if err := d.Start(w); err != nil {
		t.Fatal(err)
	}
	feed := func(line string) []Event { return d.Parse([]byte(line)) }
	feed(`{"jsonrpc":"2.0","id":1,"result":{}}`)
	if !strings.Contains(w.String(), `"method":"session/resume"`) {
		t.Fatalf("written %s", w.String())
	}
	evs := feed(`{"jsonrpc":"2.0","id":2,"result":{"configOptions":[{"id":"mode","currentValue":"default"}]}}`)
	if evs[0].Kind != KindInit || evs[0].SessionID != "s1" {
		t.Fatalf("init %+v", evs)
	}
	if feed(`{"jsonrpc":"2.0","id":3,"result":{}}`) != nil || !strings.Contains(w.String(), `"method":"session/prompt"`) {
		t.Fatalf("mode step: %s", w.String())
	}
	for _, line := range []string{
		`{"jsonrpc":"2.0","method":"session/update","params":{"update":{"sessionUpdate":"agent_message_chunk","content":{"type":"text","text":"Reading."}}}}`,
		`{"jsonrpc":"2.0","method":"other/notice","params":{}}`,
		`{"jsonrpc":"2.0","method":"session/update","params":{"update":{"sessionUpdate":"agent_thought_chunk"}}}`,
		`{"jsonrpc":"2.0","id":99,"result":{}}`,
		`{"jsonrpc":"2.0","id":"x","result":{}}`,
		`garbage`,
	} {
		feed(line)
	}
	evs = feed(`{"jsonrpc":"2.0","method":"session/update","params":{"update":{"sessionUpdate":"tool_call","title":"Read"}}}`)
	if len(evs) != 2 || evs[0].Kind != KindText || evs[0].Text != "Reading." || evs[1].ToolName != "Read" {
		t.Fatalf("tool call %+v", evs)
	}
	opts := `"options":[{"optionId":"y","kind":"allow_always"},{"optionId":"n","kind":"reject_once"}]`
	feed(`{"jsonrpc":"2.0","id":"p1","method":"session/request_permission","params":{"toolCall":{"title":"Read"},` + opts + `}}`)
	feed(`{"jsonrpc":"2.0","id":"p2","method":"session/request_permission","params":{"toolCall":{"title":"Bash"},` + opts + `}}`)
	feed(`{"jsonrpc":"2.0","id":"p3","method":"session/request_permission","params":{"toolCall":{"title":"Bash"},"options":[]}}`)
	feed(`{"jsonrpc":"2.0","id":"f1","method":"fs/read_text_file","params":{}}`)
	out := w.String()
	if !strings.Contains(out, `"optionId":"y"`) || !strings.Contains(out, `"optionId":"n"`) || strings.Count(out, `"error"`) != 2 {
		t.Fatalf("responses %s", out)
	}
	feed(`{"jsonrpc":"2.0","method":"session/update","params":{"update":{"sessionUpdate":"agent_message_chunk","content":{"type":"text","text":"Done."}}}}`)
	evs = feed(`{"jsonrpc":"2.0","id":4,"result":{"stopReason":"end_turn"}}`)
	res := evs[len(evs)-1].Result
	if res == nil || res.Result != "Done." || res.IsError || !w.closed {
		t.Fatalf("result %+v", evs)
	}
	if feed(`{"jsonrpc":"2.0","id":5,"error":{"code":1,"message":"late"}}`)[0].Kind != KindIgnored {
		t.Fatal("responses after the end are ignored")
	}
}

func TestACPDialogueFailures(t *testing.T) {
	run := func(turn acpTurn, lines ...string) *ResultEvent {
		d := newACPDialogue(turn)
		_ = d.Start(&bufCloser{})
		var last *ResultEvent
		for _, l := range lines {
			for _, ev := range d.Parse([]byte(l)) {
				if ev.Result != nil {
					last = ev.Result
				}
			}
		}
		return last
	}
	opts := `{"jsonrpc":"2.0","id":2,"result":{"sessionId":"s","configOptions":[{"id":"model","options":[{"value":"a","name":"A"}]},{"id":"thinking","options":[{"value":"on"}]}]}}`
	if r := run(acpTurn{}, `{"jsonrpc":"2.0","id":1,"error":{"code":1,"message":"boom"}}`); r == nil || r.Result != "initialize: boom" {
		t.Fatalf("initialize %+v", r)
	}
	if r := run(acpTurn{model: "zzz"}, `{"jsonrpc":"2.0","id":1,"result":{}}`, opts); r == nil || !strings.Contains(r.Result, "not configured") {
		t.Fatalf("model %+v", r)
	}
	r := run(acpTurn{effort: "high"}, `{"jsonrpc":"2.0","id":1,"result":{}}`, opts,
		`{"jsonrpc":"2.0","id":3,"error":{"code":1,"message":"thinking unsupported"}}`,
		`{"jsonrpc":"2.0","method":"session/update","params":{"update":{"sessionUpdate":"agent_message_chunk","content":{"type":"text","text":"x"}}}}`,
		`{"jsonrpc":"2.0","id":4,"result":{"stopReason":"max_tokens"}}`)
	if r == nil || r.Subtype != SubtypeMaxTurns || r.Result != "x" {
		t.Fatalf("optional step and max tokens %+v", r)
	}
	for stop, want := range map[string]string{"refusal": "refused", "cancelled": "stopped: cancelled", "end_turn": "without an answer"} {
		r := run(acpTurn{}, `{"jsonrpc":"2.0","id":1,"result":{}}`, opts, `{"jsonrpc":"2.0","id":3,"result":{"stopReason":"`+stop+`"}}`)
		if r == nil || !r.IsError || !strings.Contains(r.Result, want) {
			t.Fatalf("%s: %+v", stop, r)
		}
	}
	if !(KimiBackend{}).ResumeMissing(&Outcome{Result: &ResultEvent{IsError: true, Result: "session/resume: Unknown sessionId: s"}}) ||
		(KimiBackend{}).ResumeMissing(&Outcome{Result: &ResultEvent{Result: "ok"}}) || (KimiBackend{}).ResumeMissing(&Outcome{}) {
		t.Fatal("ResumeMissing")
	}
	b := KimiBackend{}
	if b.Name() != config.BackendKimi || b.MinVersion() != KimiMinVersion || b.CumulativeUsage() || b.NewParser() == nil {
		t.Fatal("backend basics")
	}
}
