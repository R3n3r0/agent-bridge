// Command fakecli imitates the headless CLI for tests. It never contacts any
// service.
//
// Environment:
//
//	FAKE_SCENARIO    success (default), max_turns, exec_error, no_result, slow,
//	                 spawn_child, resume_missing, new_session_id, garbage
//	FAKE_RECORD      file where a JSON line is appended at start and at end
//	                 with arguments, environment, working directory and stdin
//	FAKE_SLEEP_MS    sleep for the slow and spawn_child scenarios (default 5000)
//	FAKE_CHILD_PID   file receiving the pid of the spawn_child child process
//	FAKE_RESULT      result text (default "Fake answer.")
//	FAKE_KEY_SOURCE  apiKeySource reported in the init event (default "none")
//	FAKE_TOOL_CALLS  for the tools scenario: JSON array of {"name","arguments"}
//	                 (default: the first listed function with {"city":"Rome"})
//	FAKE_TOOL_PARALLEL=1  send the calls concurrently instead of one by one
//	FAKE_QUIET=1     no assistant text before the calls
//
// The tools scenario acts like the CLI with client functions: it starts the
// MCP server named in the last --mcp-config, lists its tools and calls
// them, then answers with the results. Without --mcp-config it answers
// with the prompt it received.
package main

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

type record struct {
	Phase string    `json:"phase"`
	PID   int       `json:"pid"`
	Time  time.Time `json:"time"`
	Args  []string  `json:"args"`
	Env   []string  `json:"env"`
	Cwd   string    `json:"cwd"`
	Stdin string    `json:"stdin"`
}

func main() {
	if os.Getenv("FAKE_CHILD") == "1" {
		time.Sleep(10 * time.Minute)
		return
	}
	args := os.Args[1:]
	if len(args) == 1 && args[0] == "--version" {
		fmt.Println("2.1.200 (fake)")
		return
	}
	stdin, _ := io.ReadAll(os.Stdin)
	writeRecord("start", args, string(stdin))
	defer writeRecord("end", args, string(stdin))

	resume := flagValue(args, "--resume")
	model := flagValue(args, "--model")
	if model == "" {
		model = "fake-model"
	}
	chatOnly := hasEmptyTools(args)
	sessionID := resume
	if sessionID == "" {
		sessionID = newID()
	}
	result := os.Getenv("FAKE_RESULT")
	if result == "" {
		result = "Fake answer."
	}
	keySource := os.Getenv("FAKE_KEY_SOURCE")
	if keySource == "" {
		keySource = "none"
	}
	initEv := map[string]any{"type": "system", "subtype": "init", "session_id": sessionID, "model": model, "apiKeySource": keySource}

	switch os.Getenv("FAKE_SCENARIO") {
	case "", "success":
		emit(initEv)
		emitAssistant(sessionID, "Looking at the request.", !chatOnly)
		emit(map[string]any{"type": "user", "session_id": sessionID})
		emit(resultEvent(sessionID, "success", false, result))
	case "max_turns":
		emit(initEv)
		emitAssistant(sessionID, "Still working.", false)
		emit(resultEvent(sessionID, "error_max_turns", true, "Partial answer."))
	case "exec_error":
		emit(initEv)
		emit(resultEvent(sessionID, "error_during_execution", true, "upstream failure"))
	case "no_result":
		fmt.Fprintln(os.Stderr, "fatal: something broke, key sk-ant-api03-SECRETSECRET")
		os.Exit(1)
	case "slow":
		emit(initEv)
		emitAssistant(sessionID, "Starting a long task.", false)
		time.Sleep(sleepDuration())
		emit(resultEvent(sessionID, "success", false, result))
	case "spawn_child":
		emit(initEv)
		child := exec.Command(os.Args[0])
		child.Env = append(os.Environ(), "FAKE_CHILD=1")
		if err := child.Start(); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		if p := os.Getenv("FAKE_CHILD_PID"); p != "" {
			_ = os.WriteFile(p, []byte(strconv.Itoa(child.Process.Pid)), 0o600)
		}
		time.Sleep(sleepDuration())
		emit(resultEvent(sessionID, "success", false, result))
	case "resume_missing":
		if resume != "" {
			fmt.Fprintf(os.Stderr, "No conversation found with session ID: %s\n", resume)
			os.Exit(1)
		}
		emit(initEv)
		emit(resultEvent(sessionID, "success", false, result))
	case "new_session_id":
		emit(initEv)
		emit(resultEvent(newID(), "success", false, result))
	case "tools":
		emit(initEv)
		cfg := lastFlagValue(args, "--mcp-config")
		if cfg == "" {
			emit(resultEvent(sessionID, "success", false, "no functions; prompt: "+string(stdin)))
			return
		}
		if os.Getenv("FAKE_QUIET") != "1" {
			emitAssistant(sessionID, "Calling functions.", false)
		}
		out, err := runMCP(cfg)
		if err != nil {
			fmt.Fprintln(os.Stderr, "mcp:", err)
			os.Exit(1)
		}
		emit(resultEvent(sessionID, "success", false, "results: "+strings.Join(out, "|")))
	case "garbage":
		fmt.Println("this is not json")
		emit(map[string]any{"type": "future_event", "payload": 1})
		emit(initEv)
		fmt.Println("{{{")
		emit(map[string]any{"type": "system", "subtype": "compact_boundary"})
		emitAssistant(sessionID, "Working.", false)
		fmt.Println("")
		emit(resultEvent(sessionID, "success", false, result))
	default:
		fmt.Fprintln(os.Stderr, "unknown FAKE_SCENARIO")
		os.Exit(2)
	}
}

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

func resultEvent(sessionID, subtype string, isError bool, result string) map[string]any {
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

type rpcResp struct {
	ID     int             `json:"id"`
	Result json.RawMessage `json:"result"`
	Error  *struct {
		Message string `json:"message"`
	} `json:"error"`
}

// runMCP starts the "functions" MCP server of cfgPath and performs the
// calls; it returns the text of each result, in call order.
func runMCP(cfgPath string) ([]string, error) {
	data, err := os.ReadFile(cfgPath)
	if err != nil {
		return nil, err
	}
	var cfg struct {
		MCPServers map[string]struct {
			Command string   `json:"command"`
			Args    []string `json:"args"`
		} `json:"mcpServers"`
	}
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, err
	}
	srv, ok := cfg.MCPServers["functions"]
	if !ok {
		return nil, fmt.Errorf("no functions server in %s", cfgPath)
	}
	cmd := exec.Command(srv.Command, srv.Args...)
	cmd.Stderr = os.Stderr
	in, _ := cmd.StdinPipe()
	outPipe, _ := cmd.StdoutPipe()
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	defer func() {
		in.Close()
		_ = cmd.Wait()
	}()
	dec := json.NewDecoder(outPipe)
	send := func(v any) {
		b, _ := json.Marshal(v)
		in.Write(append(b, '\n'))
	}
	read := func() (rpcResp, error) {
		var r rpcResp
		err := dec.Decode(&r)
		return r, err
	}

	send(map[string]any{"jsonrpc": "2.0", "id": 1, "method": "initialize", "params": map[string]any{"protocolVersion": "2025-06-18"}})
	if _, err := read(); err != nil {
		return nil, err
	}
	send(map[string]any{"jsonrpc": "2.0", "method": "notifications/initialized"})
	send(map[string]any{"jsonrpc": "2.0", "id": 2, "method": "tools/list"})
	listed, err := read()
	if err != nil {
		return nil, err
	}
	var list struct {
		Tools []struct {
			Name string `json:"name"`
		} `json:"tools"`
	}
	_ = json.Unmarshal(listed.Result, &list)
	if len(list.Tools) == 0 {
		return nil, fmt.Errorf("no tools listed")
	}

	type call struct {
		Name      string          `json:"name"`
		Arguments json.RawMessage `json:"arguments"`
	}
	calls := []call{{Name: list.Tools[0].Name, Arguments: json.RawMessage(`{"city":"Rome"}`)}}
	if v := os.Getenv("FAKE_TOOL_CALLS"); v != "" {
		if err := json.Unmarshal([]byte(v), &calls); err != nil {
			return nil, err
		}
	}
	results := make([]string, len(calls))
	collect := func() error {
		r, err := read()
		if err != nil {
			return err
		}
		if r.Error != nil {
			return fmt.Errorf("call failed: %s", r.Error.Message)
		}
		var res struct {
			Content []struct {
				Text string `json:"text"`
			} `json:"content"`
			IsError bool `json:"isError"`
		}
		_ = json.Unmarshal(r.Result, &res)
		text := ""
		if len(res.Content) > 0 {
			text = res.Content[0].Text
		}
		if res.IsError {
			text = "error(" + text + ")"
		}
		results[r.ID-10] = text
		return nil
	}
	params := func(i int) map[string]any {
		return map[string]any{"jsonrpc": "2.0", "id": 10 + i, "method": "tools/call",
			"params": map[string]any{"name": calls[i].Name, "arguments": calls[i].Arguments}}
	}
	if os.Getenv("FAKE_TOOL_PARALLEL") == "1" {
		for i := range calls {
			send(params(i))
		}
		for range calls {
			if err := collect(); err != nil {
				return nil, err
			}
		}
		return results, nil
	}
	for i := range calls {
		send(params(i))
		if err := collect(); err != nil {
			return nil, err
		}
	}
	return results, nil
}
