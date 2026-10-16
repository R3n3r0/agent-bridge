// Package prompt turns an OpenAI chat message list into the text sent to the
// CLI on stdin.
package prompt

import (
	"encoding/json"
	"errors"
	"fmt"
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
