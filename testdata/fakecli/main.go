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
//	FAKE_STRUCTURED  with --json-schema: the structured_output JSON (default
//	                 {"answer": <result>}), "none" to omit it, "fail" for
//	                 error_max_structured_output_retries
//
// With --input-format stream-json the result also describes the received
// user message: "[text=... images=image/png,...]".
//
// The tools scenario acts like the CLI with client functions: it starts the
// MCP server named in the last --mcp-config, lists its tools and calls
// them, then answers with the results. Without --mcp-config it answers
// with the prompt it received.
//
// When the first argument is "exec" the program imitates the codex CLI
// instead ("codex exec --json"); see codex.go.
package main

import (
	"fmt"
	"os"
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
	if len(args) == 1 && args[0] == "--version" && os.Getenv("FAKE_VERSION") != "" {
		fmt.Println(os.Getenv("FAKE_VERSION"))
		return
	}
	if len(args) > 0 && args[0] == "exec" {
		runCodex(args)
		return
	}
	runStreamJSON(args)
}
