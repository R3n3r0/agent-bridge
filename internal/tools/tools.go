// Package tools lets the CLI call functions defined by the API client
// (OpenAI "tools"), which are executed by the client and not on the server.
//
// The functions are exposed to the CLI as a standard MCP server (ServeMCP),
// run by the bridge binary itself as a stdio subprocess. Each call is
// forwarded over loopback HTTP to a Broker inside the bridge, which hands it
// to the Handler registered for the running turn. The handler blocks until
// the client sends the result in its next request.
package tools

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"

	"github.com/r3n3r0/agent-bridge/internal/runner"
)

// ServerName is the MCP server name under which client functions appear to
// the CLI: a function "get_weather" becomes the tool
// "mcp__functions__get_weather".
const ServerName = runner.ClientToolsServer

// ToolPrefix is the CLI tool name prefix of client functions.
const ToolPrefix = "mcp__" + ServerName + "__"

var nameRe = regexp.MustCompile(`^[a-zA-Z0-9_-]{1,64}$`)

// ValidName reports whether n is an acceptable function name.
func ValidName(n string) bool { return nameRe.MatchString(n) }

// Definition is a client function.
type Definition struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	Parameters  json.RawMessage `json:"parameters,omitempty"`
}

// InputSchema returns the JSON schema of the arguments, defaulting to an
// empty object schema.
func (d Definition) InputSchema() json.RawMessage {
	if len(d.Parameters) == 0 || string(d.Parameters) == "null" {
		return json.RawMessage(`{"type":"object","properties":{}}`)
	}
	return d.Parameters
}

// Call is a function call made by the model.
type Call struct {
	ID        string
	Name      string
	Arguments string // JSON object, as in OpenAI tool_calls
}

// Result is the outcome of a call, as returned to the model.
type Result struct {
	Content string `json:"content"`
	IsError bool   `json:"is_error"`
}

// ErrClosed is returned for calls that arrive after their turn ended.
var ErrClosed = errors.New("the turn that owns this function call has ended")

// Spec is everything the MCP subprocess needs: where to forward calls and
// which functions to list. It is written to a private file per turn.
type Spec struct {
	Endpoint string       `json:"endpoint"`
	Token    string       `json:"token"`
	Tools    []Definition `json:"tools"`
}

// WriteFiles writes the spec and the MCP configuration that starts helper
// (command and arguments, "-spec <file>" is appended) into dir, and returns
// the path of the MCP configuration.
func WriteFiles(dir string, spec Spec, helper []string) (string, error) {
	if len(helper) == 0 {
		return "", errors.New("tools: no helper command configured")
	}
	specPath := filepath.Join(dir, "functions.json")
	data, err := json.Marshal(spec)
	if err != nil {
		return "", err
	}
	if err := os.WriteFile(specPath, data, 0o600); err != nil {
		return "", fmt.Errorf("tools: %w", err)
	}
	args := append(append([]string{}, helper[1:]...), "-spec", specPath)
	cfg := map[string]any{
		"mcpServers": map[string]any{
			ServerName: map[string]any{"type": "stdio", "command": helper[0], "args": args},
		},
	}
	data, err = json.Marshal(cfg)
	if err != nil {
		return "", err
	}
	cfgPath := filepath.Join(dir, "mcp.json")
	if err := os.WriteFile(cfgPath, data, 0o600); err != nil {
		return "", fmt.Errorf("tools: %w", err)
	}
	return cfgPath, nil
}

// ReadSpec loads a spec written by WriteFiles.
func ReadSpec(path string) (Spec, error) {
	var s Spec
	data, err := os.ReadFile(path)
	if err != nil {
		return s, fmt.Errorf("tools: %w", err)
	}
	if err := json.Unmarshal(data, &s); err != nil {
		return s, fmt.Errorf("tools: %s: %w", path, err)
	}
	return s, nil
}
