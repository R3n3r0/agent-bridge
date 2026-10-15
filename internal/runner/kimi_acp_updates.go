package runner

import (
	"encoding/json"
	"strings"
)

func (d *acpDialogue) onNotification(m acpMessage) []Event {
	if m.Method != "session/update" {
		return []Event{{Kind: KindIgnored, Type: m.Method}}
	}
	var p struct {
		Update struct {
			SessionUpdate string `json:"sessionUpdate"`
			Title         string `json:"title"`
			Content       struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"content"`
		} `json:"update"`
	}
	_ = json.Unmarshal(m.Params, &p)
	u := p.Update
	switch u.SessionUpdate {
	case "agent_message_chunk":
		if u.Content.Type == "text" {
			d.text.WriteString(u.Content.Text)
		}
		return nil
	case "tool_call":
		events := d.flushText()
		return append(events, Event{Kind: KindToolUse, Type: u.SessionUpdate, SessionID: d.sessionID, ToolName: u.Title})
	}
	return []Event{{Kind: KindIgnored, Type: u.SessionUpdate, SessionID: d.sessionID}}
}

// onRequest answers the agent's requests: permissions by policy, anything
// else (file system, terminals) is not offered by the bridge.
func (d *acpDialogue) onRequest(m acpMessage) []Event {
	if m.Method != "session/request_permission" {
		d.respondError(m.ID, -32601, "method not supported by the client")
		return []Event{{Kind: KindIgnored, Type: m.Method}}
	}
	var p struct {
		ToolCall struct {
			Title string `json:"title"`
		} `json:"toolCall"`
		Options []struct {
			OptionID string `json:"optionId"`
			Kind     string `json:"kind"`
		} `json:"options"`
	}
	_ = json.Unmarshal(m.Params, &p)
	tool := p.ToolCall.Title
	allowed := d.turn.permission(tool)
	want := []string{"reject_once", "reject_always"}
	verdict := "refused"
	if allowed {
		want, verdict = []string{"allow_once", "allow_always"}, "allowed"
	}
	for _, kind := range want {
		for _, o := range p.Options {
			if o.Kind == kind {
				d.respond(m.ID, map[string]any{"outcome": map[string]any{"outcome": "selected", "optionId": o.OptionID}})
				return []Event{{Kind: KindIgnored, Type: "permission", SessionID: d.sessionID, Text: verdict + " " + tool}}
			}
		}
	}
	d.respondError(m.ID, -32602, "no suitable permission option")
	return []Event{{Kind: KindIgnored, Type: "permission", SessionID: d.sessionID, Text: "refused " + tool}}
}

// flushText returns the text received since the last tool call as an
// intermediate text event.
func (d *acpDialogue) flushText() []Event {
	text := strings.TrimSpace(d.text.String())
	d.text.Reset()
	if text == "" {
		return nil
	}
	return []Event{{Kind: KindText, Type: "agent_message", SessionID: d.sessionID, Text: text}}
}

// end turns the prompt's stop reason into the result.
func (d *acpDialogue) end(stopReason string) []Event {
	final := strings.TrimSpace(d.text.String())
	events := d.flushText()
	res := &ResultEvent{Subtype: SubtypeSuccess, Result: final, SessionID: d.sessionID, NumTurns: 1}
	switch stopReason {
	case "end_turn", "":
		if final == "" {
			return append(events, d.fail("the turn ended without an answer; the CLI logs may have details")...)
		}
	case "max_tokens", "max_turn_requests":
		res.Subtype, res.IsError = SubtypeMaxTurns, true
	case "refusal":
		return append(events, d.fail("the model refused to answer")...)
	default:
		return append(events, d.fail("the turn stopped: "+stopReason)...)
	}
	d.close()
	return append(events, Event{Kind: KindResult, Type: "session/prompt", SessionID: d.sessionID, Result: res})
}

// fail ends the turn with an error result.
func (d *acpDialogue) fail(msg string) []Event {
	if d.finished {
		return nil
	}
	d.close()
	res := &ResultEvent{Subtype: "error", IsError: true, Result: msg, SessionID: d.sessionID, NumTurns: 1}
	return []Event{{Kind: KindResult, Type: "error", SessionID: d.sessionID, Result: res}}
}

func (d *acpDialogue) close() {
	d.finished = true
	if d.w != nil {
		d.w.Close()
	}
}
