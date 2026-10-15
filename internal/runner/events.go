package runner

import (
	"encoding/json"
)

// Event kinds produced by ParseLine.
const (
	KindInit      = "init"
	KindText      = "text"
	KindToolUse   = "tool_use"
	KindResult    = "result"
	KindIgnored   = "ignored"
	KindMalformed = "malformed"
)

// Result subtypes with a specific meaning.
const (
	SubtypeSuccess  = "success"
	SubtypeMaxTurns = "error_max_turns"
)

// Usage holds token counters reported by the CLI.
type Usage struct {
	InputTokens         int64 `json:"input_tokens"`
	OutputTokens        int64 `json:"output_tokens"`
	CacheReadTokens     int64 `json:"cache_read_input_tokens"`
	CacheCreationTokens int64 `json:"cache_creation_input_tokens"`
}

// ResultEvent is the final event of a turn.
type ResultEvent struct {
	Subtype    string  `json:"subtype"`
	Result     string  `json:"result"`
	IsError    bool    `json:"is_error"`
	SessionID  string  `json:"session_id"`
	CostUSD    float64 `json:"total_cost_usd"`
	Usage      Usage   `json:"usage"`
	NumTurns   int     `json:"num_turns"`
	DurationMs int64   `json:"duration_ms"`
}

// Event is one parsed stream-json line. Only the fields relevant to Kind are
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
}

type rawEvent struct {
	Type         string `json:"type"`
	Subtype      string `json:"subtype"`
	SessionID    string `json:"session_id"`
	Model        string `json:"model"`
	APIKeySource string `json:"apiKeySource"`
	Message      *struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
			Name string `json:"name"`
		} `json:"content"`
	} `json:"message"`
}

// ParseLine decodes one line of stream-json output. Invalid lines produce a
// single KindMalformed event and unknown types a KindIgnored event, so newer
// CLI versions do not break the bridge.
func ParseLine(line []byte) []Event {
	var raw rawEvent
	if err := json.Unmarshal(line, &raw); err != nil {
		return []Event{{Kind: KindMalformed}}
	}
	switch raw.Type {
	case "system":
		if raw.Subtype == "init" {
			return []Event{{Kind: KindInit, Type: raw.Type, SessionID: raw.SessionID, Model: raw.Model, APIKeySource: raw.APIKeySource}}
		}
	case "assistant":
		if raw.Message == nil {
			break
		}
		var out []Event
		for _, c := range raw.Message.Content {
			switch c.Type {
			case "text":
				if c.Text != "" {
					out = append(out, Event{Kind: KindText, Type: raw.Type, SessionID: raw.SessionID, Text: c.Text})
				}
			case "tool_use":
				out = append(out, Event{Kind: KindToolUse, Type: raw.Type, SessionID: raw.SessionID, ToolName: c.Name})
			}
		}
		if len(out) > 0 {
			return out
		}
	case "result":
		var res ResultEvent
		if err := json.Unmarshal(line, &res); err != nil {
			return []Event{{Kind: KindMalformed}}
		}
		return []Event{{Kind: KindResult, Type: raw.Type, SessionID: res.SessionID, Result: &res}}
	}
	return []Event{{Kind: KindIgnored, Type: raw.Type, SessionID: raw.SessionID}}
}
