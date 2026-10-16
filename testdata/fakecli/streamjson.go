package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// runStreamJSON imitates the default CLI.
func runStreamJSON(args []string) {
	if len(args) == 1 && args[0] == "--version" {
		fmt.Println("2.1.200 (fake)")
		return
	}
	stdin, _ := io.ReadAll(os.Stdin)
	writeRecord("start", args, string(stdin))
	defer writeRecord("end", args, string(stdin))

	jsonSchema = flagValue(args, "--json-schema")
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
	if flagValue(args, "--input-format") == "stream-json" {
		result += " " + describeStructuredInput(stdin)
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
		out, err := runMCPConfig(cfg)
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

// describeStructuredInput summarizes a stream-json user message: its text
// and the media types of its images.
func describeStructuredInput(stdin []byte) string {
	var msg struct {
		Type    string `json:"type"`
		Message struct {
			Content []struct {
				Type   string `json:"type"`
				Text   string `json:"text"`
				Source struct {
					MediaType string `json:"media_type"`
					Data      string `json:"data"`
				} `json:"source"`
			} `json:"content"`
		} `json:"message"`
	}
	if err := json.Unmarshal(stdin, &msg); err != nil || msg.Type != "user" {
		return "[invalid structured input]"
	}
	var images []string
	text := ""
	for i, c := range msg.Message.Content {
		switch {
		case i == 0 && c.Type == "text":
			text = c.Text
		case c.Type == "image" && c.Source.Data != "":
			images = append(images, c.Source.MediaType)
		}
	}
	return fmt.Sprintf("[text=%q images=%s]", text, strings.Join(images, ","))
}
