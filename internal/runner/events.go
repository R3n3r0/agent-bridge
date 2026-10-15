package runner

import (
	"encoding/json"
)

// Event kinds produced by parsers.
const (
	KindInit      = "init"
	KindText      = "text"
	KindToolUse   = "tool_use"
	KindUsage     = "usage"
	KindResult    = "result"
	KindIgnored   = "ignored"
	KindMalformed = "malformed"
)

// Result subtypes with a specific meaning.
const (
	SubtypeSuccess              = "success"
	SubtypeMaxTurns             = "error_max_turns"
	SubtypeStructuredOutputFail = "error_max_structured_output_retries"
)

// Usage holds token counters reported by the CLI. InputTokens excludes the
// cached tokens, which are counted in CacheReadTokens.
type Usage struct {
	InputTokens         int64 `json:"input_tokens"`
	OutputTokens        int64 `json:"output_tokens"`
	CacheReadTokens     int64 `json:"cache_read_input_tokens"`
	CacheCreationTokens int64 `json:"cache_creation_input_tokens"`
}

// ResultEvent is the final event of a turn. Backends whose CLI has no such
// event synthesize it from the end-of-turn events.
type ResultEvent struct {
	Subtype    string  `json:"subtype"`
	Result     string  `json:"result"`
	IsError    bool    `json:"is_error"`
	SessionID  string  `json:"session_id"`
	CostUSD    float64 `json:"total_cost_usd"`
	Usage      Usage   `json:"usage"`
	NumTurns   int     `json:"num_turns"`
	DurationMs int64   `json:"duration_ms"`
	// StructuredOutput is the validated JSON produced with --json-schema.
	StructuredOutput json.RawMessage `json:"structured_output,omitempty"`
}

// Event is one parsed output line. Only the fields relevant to Kind are
// set; a single assistant line can produce several events.
type Event struct {
	Kind         string
	Type         string // raw "type" field, for logging unknown events
	SessionID    string
	Model        string
	APIKeySource string
	Text         string
	ToolName     string
	Result       *ResultEvent
	// Usage is set on KindUsage events: the token usage of one assistant
	// message.
	Usage *Usage
}
