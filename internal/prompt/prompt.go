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
	ErrUnsupportedContent = errors.New("only text and image_url content parts are supported")
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

// Part is one element of a message content: text or an image reference.
type Part struct {
	Text     string
	ImageURL string // data: or http(s) URL; empty for text parts
}

// Message is a message with its content reduced to text. Parts is set only
// when the content contains images, and keeps text and images in order.
type Message struct {
	Role       string
	Text       string
	Parts      []Part
	ToolCalls  []ToolCall // assistant only
	ToolCallID string     // tool only
	Name       string     // tool only: function name, if known
}

type contentPart struct {
	Type     string `json:"type"`
	Text     string `json:"text"`
	ImageURL *struct {
		URL string `json:"url"`
	} `json:"image_url"`
}

// Parse validates roles and reduces each message content to plain text.
// Content may be a string, null, or an array of "text" and "image_url"
// parts; other part types are rejected. Legacy "function" messages are
// treated as tool messages.
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
		text, parts, err := contentParts(m.Content)
		if err != nil {
			return nil, fmt.Errorf("messages[%d]: %w", i, err)
		}
		msg := Message{Role: role, Text: text, Parts: parts}
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

// contentParts returns the text of a content and, when it contains images,
// its parts in order.
func contentParts(c json.RawMessage) (string, []Part, error) {
	c = bytes.TrimSpace(c)
	if len(c) == 0 || bytes.Equal(c, []byte("null")) {
		return "", nil, nil
	}
	switch c[0] {
	case '"':
		var s string
		if err := json.Unmarshal(c, &s); err != nil {
			return "", nil, err
		}
		return s, nil, nil
	case '[':
		var raw []contentPart
		if err := json.Unmarshal(c, &raw); err != nil {
			return "", nil, err
		}
		texts := make([]string, 0, len(raw))
		parts := make([]Part, 0, len(raw))
		images := false
		for _, p := range raw {
			switch p.Type {
			case "text":
				texts = append(texts, p.Text)
				parts = append(parts, Part{Text: p.Text})
			case "image_url":
				if p.ImageURL == nil || p.ImageURL.URL == "" {
					return "", nil, errors.New("image_url part without url")
				}
				parts = append(parts, Part{ImageURL: p.ImageURL.URL})
				images = true
			default:
				return "", nil, fmt.Errorf("%w: part of type %q", ErrUnsupportedContent, p.Type)
			}
		}
		if !images {
			parts = nil
		}
		return strings.Join(texts, "\n"), parts, nil
	default:
		return "", nil, errors.New("content must be a string or an array of parts")
	}
}

// CountImages returns the number of image parts in msgs.
func CountImages(msgs []Message) int {
	n := 0
	for _, m := range msgs {
		for _, p := range m.Parts {
			if p.ImageURL != "" {
				n++
			}
		}
	}
	return n
}

// Prompt is the text sent to the CLI and the images it references, in the
// order of their "[image N]" placeholders.
type Prompt struct {
	Text   string
	Images []string
}

// renderer renders message contents, replacing images with numbered
// placeholders and collecting their URLs.
type renderer struct {
	images []string
}

func (r *renderer) content(m Message) string {
	if len(m.Parts) == 0 {
		return m.Text
	}
	out := make([]string, 0, len(m.Parts))
	for _, p := range m.Parts {
		if p.ImageURL == "" {
			out = append(out, p.Text)
			continue
		}
		r.images = append(r.images, p.ImageURL)
		out = append(out, fmt.Sprintf("[image %d]", len(r.images)))
	}
	return strings.Join(out, "\n")
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

// Options control Build.
type Options struct {
	// Resumed sends only the new user and tool messages, since the previous
	// context is already in the session.
	Resumed bool
	// OmitInstructions leaves system and developer messages out of the
	// prompt, for when they are passed to the CLI as its system prompt.
	OmitInstructions bool
}

// Build returns the prompt for a turn; see Options.Resumed.
func Build(msgs []Message, resumed bool) (string, error) {
	return BuildWith(msgs, Options{Resumed: resumed})
}

// BuildWith returns the prompt text for a turn; images are rendered as
// placeholders only. Use BuildPrompt to also get the images.
func BuildWith(msgs []Message, o Options) (string, error) {
	p, err := BuildPrompt(msgs, o)
	return p.Text, err
}

// BuildPrompt returns the prompt for a turn and the images it references.
// With Resumed, only the images of the new messages are included.
func BuildPrompt(msgs []Message, o Options) (Prompt, error) {
	if o.OmitInstructions {
		kept := make([]Message, 0, len(msgs))
		for _, m := range msgs {
			if m.Role != RoleSystem && m.Role != RoleDeveloper {
				kept = append(kept, m)
			}
		}
		msgs = kept
	}
	current := Current(msgs)
	if len(current) == 0 {
		return Prompt{}, ErrNoCurrentRequest
	}
	r := &renderer{}
	var text string
	if o.Resumed {
		text = r.renderCurrent(current)
	} else {
		text = r.buildNew(msgs, current)
	}
	if len(text) > MaxBytes {
		return Prompt{}, ErrTooLarge
	}
	return Prompt{Text: text, Images: r.images}, nil
}

func (r *renderer) renderCurrent(current []Message) string {
	parts := make([]string, len(current))
	for i, m := range current {
		if m.Role == RoleTool {
			parts[i] = r.renderTool(m)
		} else {
			parts[i] = r.content(m)
		}
	}
	return strings.Join(parts, "\n\n")
}

func (r *renderer) renderTool(m Message) string {
	text := r.content(m)
	if m.ToolCallID == "" && m.Name == "" {
		return "<tool>" + text + "</tool>"
	}
	return fmt.Sprintf("<tool tool_call_id=%q name=%q>%s</tool>", m.ToolCallID, m.Name, text)
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

func (r *renderer) buildNew(msgs []Message, current []Message) string {
	instructions := Instructions(msgs)
	if len(msgs) == 1 && msgs[0].Role == RoleUser {
		return r.content(msgs[0])
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
			history.WriteString(r.renderTool(m))
		default:
			fmt.Fprintf(&history, "<%s>%s</%s>", m.Role, r.content(m), m.Role)
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
	sections = append(sections, "<current_request>\n"+r.renderCurrent(current)+"\n</current_request>")
	return strings.Join(sections, "\n\n")
}
