package server

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/r3n3r0/agent-bridge/internal/prompt"
	"github.com/r3n3r0/agent-bridge/internal/redact"
)

// chatRequest holds the request fields the bridge uses. Every other field
// (temperature, top_p, seed, logprobs, penalties, ...) is accepted and
// ignored: the CLI has no equivalent.
type chatRequest struct {
	Model         string              `json:"model"`
	Messages      []prompt.RawMessage `json:"messages"`
	Stream        bool                `json:"stream"`
	StreamOptions *struct {
		IncludeUsage bool `json:"include_usage"`
	} `json:"stream_options"`
	User string `json:"user"`

	Tools             []toolDef       `json:"tools"`
	ToolChoice        json.RawMessage `json:"tool_choice"`
	ParallelToolCalls *bool           `json:"parallel_tool_calls"`
	// Deprecated OpenAI function calling.
	Functions    []functionDef   `json:"functions"`
	FunctionCall json.RawMessage `json:"function_call"`

	// Generation parameters mapped to the CLI or emulated (generation.go).
	N                   *int            `json:"n"`
	Stop                json.RawMessage `json:"stop"`
	MaxTokens           *int            `json:"max_tokens"`
	MaxCompletionTokens *int            `json:"max_completion_tokens"`
	ReasoningEffort     string          `json:"reasoning_effort"`
	ResponseFormat      json.RawMessage `json:"response_format"`
}

type functionDef struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Parameters  json.RawMessage `json:"parameters"`
}

type toolDef struct {
	Type     string      `json:"type"`
	Function functionDef `json:"function"`
}

// functionCall is the function part of a tool call (and the deprecated
// message.function_call).
type functionCall struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

type toolCall struct {
	ID       string       `json:"id"`
	Type     string       `json:"type"`
	Function functionCall `json:"function"`
}

// streamToolCall is a tool call in a stream delta; index is always present.
type streamToolCall struct {
	Index    int          `json:"index"`
	ID       string       `json:"id"`
	Type     string       `json:"type"`
	Function functionCall `json:"function"`
}

type usage struct {
	PromptTokens     int64 `json:"prompt_tokens"`
	CompletionTokens int64 `json:"completion_tokens"`
	TotalTokens      int64 `json:"total_tokens"`
}

type chatMessage struct {
	Role         string        `json:"role"`
	Content      *string       `json:"content"` // null when only tool calls are returned
	ToolCalls    []toolCall    `json:"tool_calls,omitempty"`
	FunctionCall *functionCall `json:"function_call,omitempty"`
}

func (m chatMessage) text() string {
	if m.Content == nil {
		return ""
	}
	return *m.Content
}

type choice struct {
	Index        int         `json:"index"`
	Message      chatMessage `json:"message"`
	FinishReason string      `json:"finish_reason"`
}

type chatCompletion struct {
	ID      string   `json:"id"`
	Object  string   `json:"object"`
	Created int64    `json:"created"`
	Model   string   `json:"model"`
	Choices []choice `json:"choices"`
	Usage   usage    `json:"usage"`
}

type delta struct {
	Role         string           `json:"role,omitempty"`
	Content      string           `json:"content,omitempty"`
	ToolCalls    []streamToolCall `json:"tool_calls,omitempty"`
	FunctionCall *functionCall    `json:"function_call,omitempty"`
}

type chunkChoice struct {
	Index        int     `json:"index"`
	Delta        delta   `json:"delta"`
	FinishReason *string `json:"finish_reason"`
}

type chatChunk struct {
	ID      string        `json:"id"`
	Object  string        `json:"object"`
	Created int64         `json:"created"`
	Model   string        `json:"model"`
	Choices []chunkChoice `json:"choices"`
	Usage   *usage        `json:"usage,omitempty"`
}

type modelObject struct {
	ID          string `json:"id"`
	Object      string `json:"object"`
	Created     int64  `json:"created"`
	OwnedBy     string `json:"owned_by"`
	Description string `json:"description,omitempty"`
}

type modelList struct {
	Object string        `json:"object"`
	Data   []modelObject `json:"data"`
}

func completionID() string { return "chatcmpl-" + randomHex(12) }

func randomHex(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b)
}

// Error codes, used both as type and code in the OpenAI error object.
const (
	errInvalidRequest   = "invalid_request_error"
	errUnsupported      = "unsupported_content"
	errAuthentication   = "authentication_error"
	errPermission       = "permission_error"
	errOverride         = "override_not_allowed"
	errModelNotFound    = "model_not_found"
	errSessionBusy      = "session_busy"
	errQueueFull        = "queue_full"
	errUpstream         = "cli_error"
	errTimeout          = "timeout"
	errServerError      = "server_error"
	retryAfterQueueFull = 30
)

type apiError struct {
	Status     int
	Code       string
	Message    string
	RetryAfter int
}

func (e *apiError) Error() string { return e.Message }

func newError(status int, code, msg string) *apiError {
	return &apiError{Status: status, Code: code, Message: msg}
}

type errorBody struct {
	Error errorObject `json:"error"`
}

type errorObject struct {
	Message string `json:"message"`
	Type    string `json:"type"`
	Code    string `json:"code"`
}

func (e *apiError) body() errorBody {
	return errorBody{Error: errorObject{Message: redact.String(e.Message), Type: e.Code, Code: e.Code}}
}

func writeError(w http.ResponseWriter, e *apiError) {
	if e.RetryAfter > 0 {
		w.Header().Set("Retry-After", strconv.Itoa(e.RetryAfter))
	}
	writeJSON(w, e.Status, e.body())
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(v)
}
