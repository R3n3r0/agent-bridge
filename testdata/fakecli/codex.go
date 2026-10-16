package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// codexArgs is a parsed "codex exec" command line.
type codexArgs struct {
	resume    string
	model     string
	config    map[string]string // -c key=value, raw values
	images    []string
	schema    string
	positions []string
}

func parseCodexArgs(args []string) codexArgs {
	c := codexArgs{config: map[string]string{}}
	rest := args[1:] // skip "exec"
	if len(rest) > 0 && rest[0] == "resume" {
		rest = rest[1:]
		c.resume = "?"
	}
	for i := 0; i < len(rest); i++ {
		a := rest[i]
		switch {
		case a == "-c" && i+1 < len(rest):
			k, v, _ := strings.Cut(rest[i+1], "=")
			c.config[k] = v
			i++
		case a == "-m" && i+1 < len(rest):
			c.model = rest[i+1]
			i++
		case a == "--output-schema" && i+1 < len(rest):
			c.schema = rest[i+1]
			i++
		case strings.HasPrefix(a, "--image="):
			c.images = append(c.images, strings.TrimPrefix(a, "--image="))
		case strings.HasPrefix(a, "--"):
		default:
			c.positions = append(c.positions, a)
		}
	}
	if c.resume != "" && len(c.positions) >= 2 {
		c.resume = c.positions[len(c.positions)-2]
	}
	return c
}

// configString decodes a TOML basic string value (JSON compatible).
func (c codexArgs) configString(key string) string {
	var s string
	_ = json.Unmarshal([]byte(c.config[key]), &s)
	return s
}

// runCodex imitates "codex exec --json" and "codex exec resume --json".
func runCodex(args []string) {
	stdin, _ := io.ReadAll(os.Stdin)
	writeRecord("start", args, string(stdin))
	defer writeRecord("end", args, string(stdin))
	c := parseCodexArgs(args)

	threadID := c.resume
	if threadID == "" {
		threadID = newID()
	}
	result := os.Getenv("FAKE_RESULT")
	if result == "" {
		result = "Fake answer."
	}
	if len(c.images) > 0 {
		var exts []string
		for _, p := range c.images {
			exts = append(exts, filepath.Ext(p))
		}
		result += fmt.Sprintf(" [images=%s]", strings.Join(exts, ","))
	}
	if c.schema != "" || strings.Contains(c.configString("developer_instructions"), "single JSON object") {
		result = codexStructured(result)
	}
	started := map[string]any{"type": "thread.started", "thread_id": threadID}

	switch os.Getenv("FAKE_SCENARIO") {
	case "", "success":
		emit(started)
		emit(map[string]any{"type": "turn.started"})
		emit(map[string]any{"type": "item.completed", "item": map[string]any{"id": "item_0", "type": "error", "message": "a warning"}})
		if c.configString("sandbox_mode") != "read-only" {
			emit(map[string]any{"type": "item.started", "item": map[string]any{"id": "item_1", "type": "command_execution", "command": "ls"}})
		}
		emit(codexMessage("Looking at the request."))
		emit(codexMessage(result))
		emit(codexCompleted(c.resume != ""))
	case "exec_error":
		emit(started)
		emit(map[string]any{"type": "error", "message": "Reconnecting... 1/5"})
		emit(map[string]any{"type": "turn.failed", "error": map[string]any{"message": "upstream failure"}})
		os.Exit(1)
	case "no_result":
		fmt.Fprintln(os.Stderr, "fatal: something broke, key sk-ant-api03-SECRETSECRET")
		os.Exit(1)
	case "resume_missing":
		if c.resume != "" {
			fmt.Fprintf(os.Stderr, "Error: thread/resume: thread/resume failed: no rollout found for thread id %s\n", c.resume)
			os.Exit(1)
		}
		emit(started)
		emit(codexMessage(result))
		emit(codexCompleted(false))
	case "slow":
		emit(started)
		emit(codexMessage("Starting a long task."))
		time.Sleep(sleepDuration())
		emit(codexMessage(result))
		emit(codexCompleted(false))
	case "tools":
		emit(started)
		cmd := c.configString("mcp_servers.functions.command")
		if cmd == "" {
			emit(codexMessage("no functions; prompt: " + string(stdin)))
			emit(codexCompleted(false))
			return
		}
		if os.Getenv("FAKE_QUIET") != "1" {
			emit(codexMessage("Calling functions."))
		}
		var mcpArgs []string
		_ = json.Unmarshal([]byte(c.config["mcp_servers.functions.args"]), &mcpArgs)
		out, err := runMCP(cmd, mcpArgs)
		if err != nil {
			fmt.Fprintln(os.Stderr, "mcp:", err)
			os.Exit(1)
		}
		emit(codexMessage("results: " + strings.Join(out, "|")))
		emit(codexCompleted(false))
	case "garbage":
		fmt.Println("this is not json")
		emit(map[string]any{"type": "future_event", "payload": 1})
		emit(started)
		fmt.Println("{{{")
		emit(codexMessage(result))
		emit(codexCompleted(false))
	default:
		fmt.Fprintln(os.Stderr, "unknown FAKE_SCENARIO")
		os.Exit(2)
	}
}

func codexMessage(text string) map[string]any {
	return map[string]any{"type": "item.completed", "item": map[string]any{"id": "item_m", "type": "agent_message", "text": text}}
}

// codexCompleted reports the thread totals: a resumed thread counts as two
// turns of 13 input (3 cached) and 5 output tokens each.
func codexCompleted(resumed bool) map[string]any {
	n := int64(1)
	if resumed {
		n = 2
	}
	return map[string]any{"type": "turn.completed", "usage": map[string]any{
		"input_tokens": 13 * n, "cached_input_tokens": 3 * n, "cache_write_input_tokens": 0,
		"output_tokens": 5 * n, "reasoning_output_tokens": 0,
	}}
}

// codexStructured is the final message when structured output is asked:
// FAKE_STRUCTURED if set, else {"answer": result}.
func codexStructured(result string) string {
	if v := os.Getenv("FAKE_STRUCTURED"); v != "" {
		return v
	}
	b, _ := json.Marshal(map[string]any{"answer": result})
	return string(b)
}
