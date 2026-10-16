package runner

import "encoding/json"

// codexParser reads the JSONL events of "codex exec --json". The CLI has no
// result event: the parser synthesizes one at turn.completed or turn.failed,
// with the last agent message as result text.
type codexParser struct {
	threadID string
	lastText string
}

type codexItem struct {
	Type    string `json:"type"`
	Text    string `json:"text"`
	Message string `json:"message"`
	Command string `json:"command"`
	Server  string `json:"server"`
	Tool    string `json:"tool"`
}

type codexUsage struct {
	InputTokens       int64 `json:"input_tokens"`
	CachedInputTokens int64 `json:"cached_input_tokens"`
	CacheWriteTokens  int64 `json:"cache_write_input_tokens"`
	OutputTokens      int64 `json:"output_tokens"`
}

type codexEvent struct {
	Type     string      `json:"type"`
	ThreadID string      `json:"thread_id"`
	Item     *codexItem  `json:"item"`
	Usage    *codexUsage `json:"usage"`
	Message  string      `json:"message"`
	Error    *struct {
		Message string `json:"message"`
	} `json:"error"`
}

// Parse implements Parser.
func (p *codexParser) Parse(line []byte) []Event {
	var ev codexEvent
	if err := json.Unmarshal(line, &ev); err != nil {
		return []Event{{Kind: KindMalformed}}
	}
	ignored := []Event{{Kind: KindIgnored, Type: ev.Type, SessionID: p.threadID}}
	switch ev.Type {
	case "thread.started":
		p.threadID = ev.ThreadID
		return []Event{{Kind: KindInit, Type: ev.Type, SessionID: ev.ThreadID}}
	case "item.started":
		if name := codexToolName(ev.Item); name != "" {
			return []Event{{Kind: KindToolUse, Type: ev.Type, SessionID: p.threadID, ToolName: name}}
		}
	case "item.completed":
		if ev.Item == nil {
			break
		}
		switch ev.Item.Type {
		case "agent_message":
			if ev.Item.Text == "" {
				break
			}
			p.lastText = ev.Item.Text
			return []Event{{Kind: KindText, Type: ev.Type, SessionID: p.threadID, Text: ev.Item.Text}}
		case "error":
			ignored[0].Type, ignored[0].Text = "warning", ev.Item.Message
		}
	case "error":
		ignored[0].Text = ev.Message
	case "turn.completed":
		var u Usage
		if ev.Usage != nil {
			u = Usage{
				InputTokens:         max(ev.Usage.InputTokens-ev.Usage.CachedInputTokens, 0),
				CacheReadTokens:     ev.Usage.CachedInputTokens,
				CacheCreationTokens: ev.Usage.CacheWriteTokens,
				OutputTokens:        ev.Usage.OutputTokens,
			}
		}
		res := &ResultEvent{Subtype: SubtypeSuccess, Result: p.lastText, SessionID: p.threadID, Usage: u, NumTurns: 1}
		return []Event{
			{Kind: KindUsage, Type: ev.Type, SessionID: p.threadID, Usage: &u},
			{Kind: KindResult, Type: ev.Type, SessionID: p.threadID, Result: res},
		}
	case "turn.failed":
		msg := "turn failed"
		if ev.Error != nil && ev.Error.Message != "" {
			msg = ev.Error.Message
		}
		res := &ResultEvent{Subtype: "error", IsError: true, Result: msg, SessionID: p.threadID, NumTurns: 1}
		return []Event{{Kind: KindResult, Type: ev.Type, SessionID: p.threadID, Result: res}}
	}
	return ignored
}

// codexToolName names the tool an item uses, or "" for other items.
func codexToolName(it *codexItem) string {
	if it == nil {
		return ""
	}
	switch it.Type {
	case "command_execution":
		return "shell"
	case "file_change":
		return "apply_patch"
	case "web_search":
		return "web_search"
	case "mcp_tool_call":
		return "mcp:" + it.Server + "/" + it.Tool
	}
	return ""
}
