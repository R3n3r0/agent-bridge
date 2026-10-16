package runner

import "encoding/json"

type rawEvent struct {
	Type         string `json:"type"`
	Subtype      string `json:"subtype"`
	SessionID    string `json:"session_id"`
	Model        string `json:"model"`
	APIKeySource string `json:"apiKeySource"`
	Message      *struct {
		Usage   *Usage `json:"usage"`
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
		if raw.Message.Usage != nil {
			out = append(out, Event{Kind: KindUsage, Type: raw.Type, SessionID: raw.SessionID, Usage: raw.Message.Usage})
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
