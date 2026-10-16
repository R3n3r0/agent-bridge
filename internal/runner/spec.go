// Package runner is the only part of the bridge that knows the command
// lines and output formats of the supported CLIs. A Backend builds the
// invocation of one turn and parses its events; the Executor runs the
// process; the Limiter bounds concurrency.
package runner

import "github.com/r3n3r0/agent-bridge/internal/config"

// Spec describes the agent settings effective for one turn, after per-request
// overrides have been applied. Each Backend translates it into its own
// command line.
type Spec struct {
	Model            string
	SystemPrompt     string
	SystemPromptMode string
	AllowedTools     []string
	DisallowedTools  []string
	ToolsEnabled     bool
	PermissionMode   string
	// Sandbox is the sandbox policy of agents with tools (codex backend).
	Sandbox   string
	MaxTurns  int
	AddDirs   []string
	MCPConfig string
	ResumeID  string
	// ClientTools, when set, is the MCP configuration exposing the API
	// client's functions under ClientToolsServer. They are available even
	// to chat-only agents, since they run on the client, not on the server.
	ClientTools string
	// StructuredInput sends the prompt as a stream-json user message on
	// stdin (see UserMessage) instead of plain text; needed for images.
	StructuredInput bool
	// Effort is the requested reasoning effort, one of EffortLevels; empty
	// keeps the default. Each backend maps it to its own levels.
	Effort string
	// JSONSchema asks for structured output matching this JSON schema.
	JSONSchema string
}

// EffortLevels are the accepted reasoning effort values: the OpenAI ones
// plus "xhigh" and "max".
var EffortLevels = []string{"none", "minimal", "low", "medium", "high", "xhigh", "max"}

// ClientToolsServer is the MCP server name used for client functions.
const ClientToolsServer = "functions"

// SpecFromAgent copies the agent settings into a Spec.
func SpecFromAgent(a *config.Agent) Spec {
	return Spec{
		Model:            a.Model,
		SystemPrompt:     a.SystemPrompt,
		SystemPromptMode: a.SystemPromptMode,
		AllowedTools:     append([]string(nil), a.AllowedTools...),
		DisallowedTools:  append([]string(nil), a.DisallowedTools...),
		ToolsEnabled:     a.ToolsEnabled,
		PermissionMode:   a.PermissionMode,
		Sandbox:          a.Sandbox,
		MaxTurns:         a.MaxTurns,
		AddDirs:          append([]string(nil), a.AddDirs...),
		MCPConfig:        a.MCPConfig,
	}
}

// ImageBlock is an image sent to the CLI.
type ImageBlock struct {
	MediaType string
	Data      []byte
}
