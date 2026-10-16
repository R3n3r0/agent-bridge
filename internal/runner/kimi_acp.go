package runner

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"sync"
)

// acpProtocolVersion is the ACP version the bridge speaks.
const acpProtocolVersion = 1

// acpTurn is what the ACP dialogue of one turn needs.
type acpTurn struct {
	cwd      string
	resumeID string
	model    string
	effort   string
	mode     string
	servers  []acpMCPServer
	addDirs  []string
	prompt   []acpBlock
	// permission decides the agent's permission requests by tool name.
	permission func(tool string) bool
}

// acpStep is a setup request sent after the session exists.
type acpStep struct {
	method   string
	params   any
	optional bool // an error does not fail the turn
}

// acpDialogue is the ACP client side of one turn: it implements Parser and
// Responder. It initializes the connection, creates or resumes the session,
// applies model, thinking level and mode, sends the prompt and turns the
// session updates into events; the prompt response ends the turn and closes
// stdin, which makes the CLI exit.
type acpDialogue struct {
	turn acpTurn

	mu        sync.Mutex
	w         io.WriteCloser
	nextID    int
	pending   map[int]acpStep
	sessionID string
	steps     []acpStep
	text      strings.Builder
	finished  bool
}

var (
	_ Parser    = (*acpDialogue)(nil)
	_ Responder = (*acpDialogue)(nil)
)

func newACPDialogue(t acpTurn) *acpDialogue {
	if t.permission == nil {
		t.permission = func(string) bool { return false }
	}
	return &acpDialogue{turn: t, pending: map[int]acpStep{}}
}

// acpMessage is any JSON-RPC message.
type acpMessage struct {
	ID     json.RawMessage `json:"id,omitempty"`
	Method string          `json:"method,omitempty"`
	Params json.RawMessage `json:"params,omitempty"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

// Start implements Responder.
func (d *acpDialogue) Start(stdin io.WriteCloser) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.w = stdin
	return d.request(acpStep{method: "initialize", params: map[string]any{
		"protocolVersion": acpProtocolVersion,
		"clientCapabilities": map[string]any{
			"fs":       map[string]bool{"readTextFile": false, "writeTextFile": false},
			"terminal": false,
		},
	}})
}

// Parse implements Parser.
func (d *acpDialogue) Parse(line []byte) []Event {
	var m acpMessage
	if err := json.Unmarshal(line, &m); err != nil {
		return []Event{{Kind: KindMalformed}}
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	switch {
	case m.Method != "" && len(m.ID) > 0:
		return d.onRequest(m)
	case m.Method != "":
		return d.onNotification(m)
	}
	var id int
	if err := json.Unmarshal(m.ID, &id); err != nil {
		return []Event{{Kind: KindIgnored, Type: "response"}}
	}
	step, ok := d.pending[id]
	if !ok {
		return []Event{{Kind: KindIgnored, Type: "response"}}
	}
	delete(d.pending, id)
	if m.Error != nil {
		if step.optional {
			return append(d.next(), Event{Kind: KindIgnored, Type: "warning", Text: step.method + ": " + m.Error.Message})
		}
		return d.fail(fmt.Sprintf("%s: %s", step.method, m.Error.Message))
	}
	return d.onResponse(step, m.Result)
}

func (d *acpDialogue) onResponse(step acpStep, result json.RawMessage) []Event {
	switch step.method {
	case "initialize":
		return d.startSession()
	case "session/new", "session/resume":
		var r struct {
			SessionID     string            `json:"sessionId"`
			ConfigOptions []acpConfigOption `json:"configOptions"`
		}
		_ = json.Unmarshal(result, &r)
		d.sessionID = r.SessionID
		if d.sessionID == "" {
			d.sessionID = d.turn.resumeID
		}
		if err := d.plan(r.ConfigOptions); err != nil {
			return d.fail(err.Error())
		}
		init := Event{Kind: KindInit, Type: step.method, SessionID: d.sessionID, Model: currentModelName(r.ConfigOptions, d.turn.model)}
		return append([]Event{init}, d.next()...)
	case "session/prompt":
		var r struct {
			StopReason string `json:"stopReason"`
		}
		_ = json.Unmarshal(result, &r)
		return d.end(r.StopReason)
	}
	return d.next()
}

// startSession creates the session, or resumes the stored one.
func (d *acpDialogue) startSession() []Event {
	servers := d.turn.servers
	if servers == nil {
		servers = []acpMCPServer{}
	}
	params := map[string]any{"cwd": d.turn.cwd, "mcpServers": servers}
	method := "session/new"
	if d.turn.resumeID != "" {
		method = "session/resume"
		params["sessionId"] = d.turn.resumeID
	} else if len(d.turn.addDirs) > 0 {
		params["additionalDirectories"] = d.turn.addDirs
	}
	if err := d.request(acpStep{method: method, params: params}); err != nil {
		return d.fail(err.Error())
	}
	return nil
}

// plan queues model, thinking level and mode, then the prompt.
func (d *acpDialogue) plan(options []acpConfigOption) error {
	set := func(id, value string, optional bool) {
		d.steps = append(d.steps, acpStep{method: "session/set_config_option", optional: optional,
			params: map[string]any{"sessionId": d.sessionID, "configId": id, "value": value}})
	}
	if d.turn.model != "" {
		value, err := modelValue(options, d.turn.model)
		if err != nil {
			return err
		}
		set("model", value, false)
	}
	if v := thinkingValue(options, d.turn.effort); v != "" {
		set("thinking", v, true)
	}
	if d.turn.mode != "" && currentValue(options, "mode") != d.turn.mode {
		set("mode", d.turn.mode, false)
	}
	d.steps = append(d.steps, acpStep{method: "session/prompt",
		params: map[string]any{"sessionId": d.sessionID, "prompt": d.turn.prompt}})
	return nil
}

// next sends the next queued step.
func (d *acpDialogue) next() []Event {
	if len(d.steps) == 0 || d.finished {
		return nil
	}
	step := d.steps[0]
	d.steps = d.steps[1:]
	if err := d.request(step); err != nil {
		return d.fail(err.Error())
	}
	return nil
}
