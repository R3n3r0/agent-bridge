package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

// acpMsg is a JSON-RPC message of the fake ACP agent.
type acpMsg struct {
	ID     json.RawMessage `json:"id,omitempty"`
	Method string          `json:"method,omitempty"`
	Params json.RawMessage `json:"params,omitempty"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  json.RawMessage `json:"error,omitempty"`
}

// fakeACP is the state of the fake "kimi acp" agent.
type fakeACP struct {
	in        *bufio.Scanner
	received  []string
	sessionID string
	servers   []struct {
		Name    string   `json:"name"`
		Command string   `json:"command"`
		Args    []string `json:"args"`
	}
}

// runACP imitates "kimi acp": an ACP agent on stdin and stdout. The
// scenarios are those of FAKE_SCENARIO: success (default), tools, empty,
// max_turns, resume_missing, prompt_error.
func runACP(args []string) {
	a := &fakeACP{in: bufio.NewScanner(os.Stdin)}
	a.in.Buffer(make([]byte, 1<<20), 64<<20)
	writeRecord("start", args, "")
	defer func() { writeRecord("end", args, strings.Join(a.received, "\n")) }()
	for {
		m, ok := a.read()
		if !ok {
			return
		}
		a.handle(m)
	}
}

func (a *fakeACP) read() (acpMsg, bool) {
	if !a.in.Scan() {
		return acpMsg{}, false
	}
	line := a.in.Text()
	a.received = append(a.received, line)
	var m acpMsg
	_ = json.Unmarshal([]byte(line), &m)
	return m, true
}

func (a *fakeACP) send(v any) { emit(v) }

func (a *fakeACP) reply(id json.RawMessage, result any) {
	a.send(map[string]any{"jsonrpc": "2.0", "id": id, "result": result})
}

func (a *fakeACP) fail(id json.RawMessage, code int, msg string) {
	a.send(map[string]any{"jsonrpc": "2.0", "id": id, "error": map[string]any{"code": code, "message": msg}})
}

func (a *fakeACP) update(u map[string]any) {
	a.send(map[string]any{"jsonrpc": "2.0", "method": "session/update", "params": map[string]any{"sessionId": a.sessionID, "update": u}})
}

func (a *fakeACP) chunk(text string) {
	a.update(map[string]any{"sessionUpdate": "agent_message_chunk", "content": map[string]any{"type": "text", "text": text}})
}

func configOptions() []map[string]any {
	opt := func(values ...string) []map[string]any {
		var out []map[string]any
		for _, v := range values {
			name, value, _ := strings.Cut(v, "=")
			out = append(out, map[string]any{"value": value, "name": name})
		}
		return out
	}
	return []map[string]any{
		{"type": "select", "id": "model", "currentValue": "default-model", "options": opt("Default Model=default-model", "kimi-k2=k2")},
		{"type": "select", "id": "thinking", "currentValue": "off", "options": opt("off=off", "low=low", "high=high")},
		{"type": "select", "id": "mode", "currentValue": "default", "options": opt("default=default", "plan=plan", "auto=auto", "yolo=yolo")},
	}
}

func (a *fakeACP) handle(m acpMsg) {
	scenario := os.Getenv("FAKE_SCENARIO")
	var p struct {
		SessionID string          `json:"sessionId"`
		ConfigID  string          `json:"configId"`
		Value     string          `json:"value"`
		Servers   json.RawMessage `json:"mcpServers"`
		Prompt    []struct {
			Type     string `json:"type"`
			Text     string `json:"text"`
			MimeType string `json:"mimeType"`
		} `json:"prompt"`
	}
	_ = json.Unmarshal(m.Params, &p)
	switch m.Method {
	case "initialize":
		a.reply(m.ID, map[string]any{"protocolVersion": 1, "agentCapabilities": map[string]any{"loadSession": true}})
	case "session/new", "session/resume":
		if m.Method == "session/resume" && scenario == "resume_missing" {
			a.fail(m.ID, -32602, "Unknown sessionId: "+p.SessionID)
			return
		}
		_ = json.Unmarshal(p.Servers, &a.servers)
		a.sessionID = p.SessionID
		res := map[string]any{"configOptions": configOptions()}
		if m.Method == "session/new" {
			a.sessionID = "session_" + newID()
			res["sessionId"] = a.sessionID
		}
		a.reply(m.ID, res)
	case "session/set_config_option":
		if p.ConfigID == "model" && p.Value != "default-model" && p.Value != "k2" {
			a.fail(m.ID, -32602, "unknown model")
			return
		}
		a.reply(m.ID, map[string]any{"configOptions": configOptions()})
	case "session/prompt":
		a.prompt(m.ID, scenario, p.Prompt[0].Text, len(p.Prompt)-1)
	default:
		if len(m.ID) > 0 {
			a.fail(m.ID, -32601, "unknown method "+m.Method)
		}
	}
}

func (a *fakeACP) prompt(id json.RawMessage, scenario, text string, images int) {
	result := os.Getenv("FAKE_RESULT")
	if result == "" {
		result = "Fake answer."
	}
	switch scenario {
	case "empty":
		a.reply(id, map[string]any{"stopReason": "end_turn"})
		return
	case "max_turns":
		a.chunk("Partial answer.")
		a.reply(id, map[string]any{"stopReason": "max_turn_requests"})
		return
	case "prompt_error":
		a.fail(id, -32000, "Authentication required")
		return
	case "tools":
		a.toolsPrompt(id)
		return
	}
	a.chunk("Looking at the request.")
	tool := os.Getenv("FAKE_PERMISSION_TOOL")
	if tool == "" {
		tool = "Bash"
	}
	a.update(map[string]any{"sessionUpdate": "tool_call", "toolCallId": "t1", "title": tool})
	a.askPermission(tool)
	if strings.HasPrefix(text, "/") {
		result += " [slash]"
	}
	if images > 0 {
		result += fmt.Sprintf(" [images=%d]", images)
	}
	if strings.Contains(text, "single JSON object") {
		b, _ := json.Marshal(map[string]any{"answer": result})
		result = string(b)
	}
	a.chunk(result)
	a.reply(id, map[string]any{"stopReason": "end_turn"})
}

// askPermission sends a permission request and returns the selected option.
func (a *fakeACP) askPermission(tool string) string {
	a.send(map[string]any{"jsonrpc": "2.0", "id": "perm-1", "method": "session/request_permission", "params": map[string]any{
		"sessionId": a.sessionID,
		"toolCall":  map[string]any{"toolCallId": "t1", "title": tool},
		"options": []map[string]any{
			{"optionId": "approve", "name": "Approve", "kind": "allow_once"},
			{"optionId": "reject", "name": "Reject", "kind": "reject_once"},
		},
	}})
	for {
		m, ok := a.read()
		if !ok {
			return ""
		}
		if string(m.ID) == `"perm-1"` && m.Method == "" {
			var r struct {
				Outcome struct {
					OptionID string `json:"optionId"`
				} `json:"outcome"`
			}
			_ = json.Unmarshal(m.Result, &r)
			return r.Outcome.OptionID
		}
	}
}

func (a *fakeACP) toolsPrompt(id json.RawMessage) {
	for _, s := range a.servers {
		if s.Name != "functions" {
			continue
		}
		if os.Getenv("FAKE_QUIET") != "1" {
			a.chunk("Calling functions.")
		}
		a.update(map[string]any{"sessionUpdate": "tool_call", "toolCallId": "f1", "title": "mcp__functions__get_weather"})
		out, err := runMCP(s.Command, s.Args)
		if err != nil {
			fmt.Fprintln(os.Stderr, "mcp:", err)
			os.Exit(1)
		}
		a.chunk("results: " + strings.Join(out, "|"))
		a.reply(id, map[string]any{"stopReason": "end_turn"})
		return
	}
	a.chunk("no functions")
	a.reply(id, map[string]any{"stopReason": "end_turn"})
}
