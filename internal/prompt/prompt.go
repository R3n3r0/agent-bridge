// Package prompt turns an OpenAI chat message list into the text sent to the
// CLI on stdin.
package prompt

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// MaxBytes is the largest prompt accepted, in UTF-8 bytes. The CLI limits
// stdin to 10 MB; the margin leaves room for framing.
const MaxBytes = 9 << 20

// Errors returned by Parse and Build.
var (
	ErrUnsupportedContent = errors.New("only text content parts are supported")
	ErrNoMessages         = errors.New("messages must not be empty")
	ErrNoUserMessage      = errors.New("messages must contain at least one user message")
	ErrNoCurrentRequest   = errors.New("the last assistant message must be followed by at least one user or tool message")
	ErrTooLarge           = fmt.Errorf("prompt exceeds %d bytes", MaxBytes)
	ErrInvalidRole        = errors.New("invalid message role")
)

// Roles accepted in requests. RoleFunction is the legacy name of RoleTool.
const (
	RoleSystem    = "system"
	RoleDeveloper = "developer"
	RoleUser      = "user"
	RoleAssistant = "assistant"
	RoleTool      = "tool"
	RoleFunction  = "function"
)

// RawToolCall is an assistant tool call as received in the request body.
type RawToolCall struct {
	ID       string `json:"id"`
	Type     string `json:"type"`
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
}

// RawMessage is a message as received in the request body.
type RawMessage struct {
	Role       string          `json:"role"`
	Content    json.RawMessage `json:"content"`
	Name       string          `json:"name,omitempty"`
	ToolCalls  []RawToolCall   `json:"tool_calls,omitempty"`
	ToolCallID string          `json:"tool_call_id,omitempty"`
	// FunctionCall is the deprecated single function call of an assistant
	// message.
	FunctionCall *struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function_call,omitempty"`
}

// ToolCall is a function call made by the assistant in an earlier turn.
type ToolCall struct {
	ID        string
	Name      string
	Arguments string
}

// Message is a message with its content reduced to text.
type Message struct {
	Role       string
	Text       string
	ToolCalls  []ToolCall // assistant only
	ToolCallID string     // tool only
	Name       string     // tool only: function name, if known
}

type contentPart struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

// Parse validates roles and reduces each message content to plain text.
// Content may be a string, null, or an array of parts; only "text" parts are
// accepted. Legacy "function" messages are treated as tool messages.
func Parse(raw []RawMessage) ([]Message, error) {
	if len(raw) == 0 {
		return nil, ErrNoMessages
	}
	out := make([]Message, 0, len(raw))
	names := map[string]string{} // tool call id -> function name
	hasUser := false
	for i, m := range raw {
		role := m.Role
		switch role {
		case RoleSystem, RoleDeveloper, RoleUser, RoleAssistant, RoleTool:
		case RoleFunction:
			role = RoleTool
		default:
			return nil, fmt.Errorf("%w %q at messages[%d]", ErrInvalidRole, m.Role, i)
		}
		text, err := contentText(m.Content)
		if err != nil {
			return nil, fmt.Errorf("messages[%d]: %w", i, err)
		}
		msg := Message{Role: role, Text: text}
		switch role {
		case RoleUser:
			hasUser = true
		case RoleAssistant:
			for _, tc := range m.ToolCalls {
				msg.ToolCalls = append(msg.ToolCalls, ToolCall{ID: tc.ID, Name: tc.Function.Name, Arguments: tc.Function.Arguments})
				names[tc.ID] = tc.Function.Name
			}
			if fc := m.FunctionCall; fc != nil {
				msg.ToolCalls = append(msg.ToolCalls, ToolCall{Name: fc.Name, Arguments: fc.Arguments})
			}
		case RoleTool:
			msg.ToolCallID = m.ToolCallID
			msg.Name = m.Name
			if msg.Name == "" {
				msg.Name = names[m.ToolCallID]
			}
		}
		out = append(out, msg)
	}
	if !hasUser {
		return nil, ErrNoUserMessage
	}
	return out, nil
}

func contentText(c json.RawMessage) (string, error) {
	c = bytes.TrimSpace(c)
	if len(c) == 0 || bytes.Equal(c, []byte("null")) {
		return "", nil
	}
	switch c[0] {
	case '"':
		var s string
		if err := json.Unmarshal(c, &s); err != nil {
			return "", err
		}
		return s, nil
	case '[':
		var parts []contentPart
		if err := json.Unmarshal(c, &parts); err != nil {
			return "", err
		}
		texts := make([]string, 0, len(parts))
		for _, p := range parts {
			if p.Type != "text" {
				return "", fmt.Errorf("%w: part of type %q", ErrUnsupportedContent, p.Type)
			}
			texts = append(texts, p.Text)
		}
		return strings.Join(texts, "\n"), nil
	default:
		return "", errors.New("content must be a string or an array of parts")
	}
}

// lastAssistant returns the index of the last assistant message, or -1.
func lastAssistant(msgs []Message) int {
	for i := len(msgs) - 1; i >= 0; i-- {
		if msgs[i].Role == RoleAssistant {
			return i
		}
	}
	return -1
}

// Current returns the user and tool messages after the last assistant
// message: what the model has not seen yet.
func Current(msgs []Message) []Message {
	var out []Message
	for _, m := range msgs[lastAssistant(msgs)+1:] {
		if m.Role == RoleUser || m.Role == RoleTool {
			out = append(out, m)
		}
	}
	return out
}

// CurrentRequest returns the texts of the user messages after the last
// assistant message.
func CurrentRequest(msgs []Message) []string {
	var out []string
	for _, m := range Current(msgs) {
		if m.Role == RoleUser {
			out = append(out, m.Text)
		}
	}
	return out
}

// ToolResults returns the tool messages after the last assistant message,
// keyed by tool call id. Messages without id are skipped.
func ToolResults(msgs []Message) map[string]string {
	out := map[string]string{}
	for _, m := range Current(msgs) {
		if m.Role == RoleTool && m.ToolCallID != "" {
			out[m.ToolCallID] = m.Text
		}
	}
	return out
}

// Instructions returns the system and developer texts joined by a blank line.
func Instructions(msgs []Message) string {
	var out []string
	for _, m := range msgs {
		if m.Role == RoleSystem || m.Role == RoleDeveloper {
			out = append(out, m.Text)
		}
	}
	return strings.Join(out, "\n\n")
}

// Build returns the prompt for a turn. With resumed set, only the new user
// and tool messages are sent, since the previous context is already in the
// session.
func Build(msgs []Message, resumed bool) (string, error) {
	current := Current(msgs)
	if len(current) == 0 {
		return "", ErrNoCurrentRequest
	}
	var p string
	if resumed {
		p = renderCurrent(current)
	} else {
		p = buildNew(msgs, current)
	}
	if len(p) > MaxBytes {
		return "", ErrTooLarge
	}
	return p, nil
}

func renderCurrent(current []Message) string {
	parts := make([]string, len(current))
	for i, m := range current {
		if m.Role == RoleTool {
			parts[i] = renderTool(m)
		} else {
			parts[i] = m.Text
		}
	}
	return strings.Join(parts, "\n\n")
}

func renderTool(m Message) string {
	if m.ToolCallID == "" && m.Name == "" {
		return "<tool>" + m.Text + "</tool>"
	}
	return fmt.Sprintf("<tool tool_call_id=%q name=%q>%s</tool>", m.ToolCallID, m.Name, m.Text)
}

func renderAssistant(m Message) string {
	var b strings.Builder
	b.WriteString("<assistant>")
	b.WriteString(m.Text)
	for _, tc := range m.ToolCalls {
		if b.Len() > len("<assistant>") {
			b.WriteString("\n")
		}
		if tc.ID == "" {
			fmt.Fprintf(&b, "<tool_call name=%q>%s</tool_call>", tc.Name, tc.Arguments)
			continue
		}
		fmt.Fprintf(&b, "<tool_call id=%q name=%q>%s</tool_call>", tc.ID, tc.Name, tc.Arguments)
	}
	b.WriteString("</assistant>")
	return b.String()
}

func buildNew(msgs []Message, current []Message) string {
	instructions := Instructions(msgs)
	if len(msgs) == 1 && msgs[0].Role == RoleUser {
		return msgs[0].Text
	}

	last := lastAssistant(msgs)
	var history strings.Builder
	for i, m := range msgs {
		if m.Role == RoleSystem || m.Role == RoleDeveloper || i > last {
			continue
		}
		switch m.Role {
		case RoleAssistant:
			history.WriteString(renderAssistant(m))
		case RoleTool:
			history.WriteString(renderTool(m))
		default:
			fmt.Fprintf(&history, "<%s>%s</%s>", m.Role, m.Text, m.Role)
		}
		history.WriteString("\n")
	}

	var sections []string
	if instructions != "" {
		sections = append(sections, "<instructions>\n"+instructions+"\n</instructions>")
	}
	if history.Len() > 0 {
		sections = append(sections, "<conversation_history>\n"+history.String()+"</conversation_history>")
	}
	sections = append(sections, "<current_request>\n"+renderCurrent(current)+"\n</current_request>")
	return strings.Join(sections, "\n\n")
}
