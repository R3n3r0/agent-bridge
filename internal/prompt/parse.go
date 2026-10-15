package prompt

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

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
