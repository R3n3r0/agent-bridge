package main

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"os"
	"strconv"
	"strings"
	"time"
)

func sleepDuration() time.Duration {
	ms, err := strconv.Atoi(os.Getenv("FAKE_SLEEP_MS"))
	if err != nil {
		ms = 5000
	}
	return time.Duration(ms) * time.Millisecond
}

func emit(v any) {
	b, _ := json.Marshal(v)
	os.Stdout.Write(append(b, '\n'))
}

func emitAssistant(sessionID, text string, withTool bool) {
	content := []map[string]any{{"type": "text", "text": text}}
	if withTool {
		content = append(content, map[string]any{"type": "tool_use", "id": "toolu_1", "name": "Read", "input": map[string]any{"file_path": "README.md"}})
	}
	emit(map[string]any{"type": "assistant", "session_id": sessionID, "message": map[string]any{"role": "assistant", "content": content}})
}

// jsonSchema is the --json-schema argument, if any.
var jsonSchema string

func resultEvent(sessionID, subtype string, isError bool, result string) map[string]any {
	ev := resultFields(sessionID, subtype, isError, result)
	if jsonSchema == "" || subtype != "success" || isError {
		return ev
	}
	switch v := os.Getenv("FAKE_STRUCTURED"); v {
	case "none":
	case "fail":
		ev["subtype"], ev["is_error"] = "error_max_structured_output_retries", true
	case "":
		ev["structured_output"] = map[string]any{"answer": result}
	default:
		ev["structured_output"] = json.RawMessage(v)
	}
	return ev
}

func resultFields(sessionID, subtype string, isError bool, result string) map[string]any {
	return map[string]any{
		"type":           "result",
		"subtype":        subtype,
		"is_error":       isError,
		"result":         result,
		"session_id":     sessionID,
		"total_cost_usd": 0.0123,
		"num_turns":      2,
		"duration_ms":    42,
		"usage": map[string]any{
			"input_tokens":                10,
			"output_tokens":               5,
			"cache_read_input_tokens":     3,
			"cache_creation_input_tokens": 2,
		},
	}
}

func flagValue(args []string, name string) string {
	for i := 0; i+1 < len(args); i++ {
		if args[i] == name {
			return args[i+1]
		}
	}
	return ""
}

func hasEmptyTools(args []string) bool {
	for i := 0; i+1 < len(args); i++ {
		if args[i] == "--tools" && args[i+1] == "" {
			return true
		}
	}
	return false
}

func newID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	h := hex.EncodeToString(b)
	return strings.Join([]string{h[:8], h[8:12], h[12:16], h[16:20], h[20:]}, "-")
}

func writeRecord(phase string, args []string, stdin string) {
	path := os.Getenv("FAKE_RECORD")
	if path == "" {
		return
	}
	cwd, _ := os.Getwd()
	b, _ := json.Marshal(record{Phase: phase, PID: os.Getpid(), Time: time.Now(), Args: args, Env: os.Environ(), Cwd: cwd, Stdin: stdin})
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return
	}
	defer f.Close()
	f.Write(append(b, '\n'))
}

func lastFlagValue(args []string, name string) string {
	v := ""
	for i := 0; i+1 < len(args); i++ {
		if args[i] == name {
			v = args[i+1]
		}
	}
	return v
}
