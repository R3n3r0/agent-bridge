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
