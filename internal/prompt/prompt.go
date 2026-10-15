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
	ErrNoCurrentRequest   = errors.New("the last message must be followed by at least one user message after the last assistant message")
	ErrTooLarge           = fmt.Errorf("prompt exceeds %d bytes", MaxBytes)
	ErrInvalidRole        = errors.New("invalid message role")
)

// Roles accepted in requests.
const (
	RoleSystem    = "system"
	RoleDeveloper = "developer"
	RoleUser      = "user"
	RoleAssistant = "assistant"
	RoleTool      = "tool"
)

// RawMessage is a message as received in the request body.
type RawMessage struct {
	Role    string          `json:"role"`
	Content json.RawMessage `json:"content"`
}

// Message is a message with its content reduced to text.
type Message struct {
	Role string
	Text string
}

type contentPart struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

// Parse validates roles and reduces each message content to plain text.
// Content may be a string, null, or an array of parts; only "text" parts are
// accepted.
func Parse(raw []RawMessage) ([]Message, error) {
	if len(raw) == 0 {
		return nil, ErrNoMessages
	}
	out := make([]Message, 0, len(raw))
	hasUser := false
	for i, m := range raw {
		switch m.Role {
		case RoleSystem, RoleDeveloper, RoleUser, RoleAssistant, RoleTool:
		default:
			return nil, fmt.Errorf("%w %q at messages[%d]", ErrInvalidRole, m.Role, i)
		}
		text, err := contentText(m.Content)
		if err != nil {
			return nil, fmt.Errorf("messages[%d]: %w", i, err)
		}
		if m.Role == RoleUser {
			hasUser = true
		}
		out = append(out, Message{Role: m.Role, Text: text})
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

// CurrentRequest returns the user messages after the last assistant message.
func CurrentRequest(msgs []Message) []string {
	var out []string
	for _, m := range msgs[lastAssistant(msgs)+1:] {
		if m.Role == RoleUser {
			out = append(out, m.Text)
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
// messages are sent, since the previous context is already in the session.
func Build(msgs []Message, resumed bool) (string, error) {
	current := CurrentRequest(msgs)
	if len(current) == 0 {
		return "", ErrNoCurrentRequest
	}
	var p string
	if resumed {
		p = strings.Join(current, "\n\n")
	} else {
		p = buildNew(msgs, current)
	}
	if len(p) > MaxBytes {
		return "", ErrTooLarge
	}
	return p, nil
}

func buildNew(msgs []Message, current []string) string {
	instructions := Instructions(msgs)
	users := 0
	for _, m := range msgs {
		if m.Role == RoleUser {
			users++
		}
	}
	if users == 1 && instructions == "" && len(msgs) == 1 {
		return current[0]
	}

	last := lastAssistant(msgs)
	var history strings.Builder
	for i, m := range msgs {
		if m.Role == RoleSystem || m.Role == RoleDeveloper {
			continue
		}
		if i > last && m.Role == RoleUser {
			continue
		}
		fmt.Fprintf(&history, "<%s>%s</%s>\n", m.Role, m.Text, m.Role)
	}

	var sections []string
	if instructions != "" {
		sections = append(sections, "<instructions>\n"+instructions+"\n</instructions>")
	}
	if history.Len() > 0 {
		sections = append(sections, "<conversation_history>\n"+history.String()+"</conversation_history>")
	}
	sections = append(sections, "<current_request>\n"+strings.Join(current, "\n\n")+"\n</current_request>")
	return strings.Join(sections, "\n\n")
}
