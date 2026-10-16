// Package runner is the only part of the bridge that knows the CLI flags and
// its stream-json event format. It builds arguments and environment, runs
// one process per turn, parses events and limits concurrency.
package runner

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/r3n3r0/agent-bridge/internal/config"
)

// MinCLIVersion is the oldest CLI version the bridge is tested against. It is
// the release that introduced the 10 MB stdin limit the prompt builder relies
// on.
const MinCLIVersion = "2.1.128"

// Spec describes the agent settings effective for one turn, after per-request
// overrides have been applied.
type Spec struct {
	Model            string
	SystemPrompt     string
	SystemPromptMode string
	AllowedTools     []string
	DisallowedTools  []string
	ToolsEnabled     bool
	PermissionMode   string
	MaxTurns         int
	AddDirs          []string
	MCPConfig        string
	ResumeID         string
	// ClientTools, when set, is the MCP configuration exposing the API
	// client's functions under ClientToolsServer. They are available even
	// to chat-only agents, since they run on the client, not on the server.
	ClientTools string
	// StructuredInput sends the prompt as a stream-json user message on
	// stdin (see UserMessage) instead of plain text; needed for images.
	StructuredInput bool
	// Effort is the reasoning effort (--effort); empty keeps the default.
	Effort string
	// JSONSchema asks for validated structured output (--json-schema).
	JSONSchema string
}

// Effort levels accepted by the CLI.
var EffortLevels = []string{"low", "medium", "high", "xhigh", "max"}

// ClientToolsServer is the MCP server name used for client functions; the
// CLI names the tools "mcp__<server>__<function>".
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
		MaxTurns:         a.MaxTurns,
		AddDirs:          append([]string(nil), a.AddDirs...),
		MCPConfig:        a.MCPConfig,
	}
}

// Args returns the CLI arguments for s, in a fixed order. The prompt is
// never an argument: it is written to stdin.
func Args(s Spec) []string {
	args := []string{"-p", "--output-format", "stream-json", "--verbose"}
	if s.StructuredInput {
		args = append(args, "--input-format", "stream-json")
	}
	if s.Model != "" {
		args = append(args, "--model", s.Model)
	}
	if s.Effort != "" {
		args = append(args, "--effort", s.Effort)
	}
	if s.SystemPrompt != "" {
		if s.SystemPromptMode == config.PromptModeReplace {
			args = append(args, "--system-prompt", s.SystemPrompt)
		} else {
			args = append(args, "--append-system-prompt", s.SystemPrompt)
		}
	}
	var allowed []string
	if s.ToolsEnabled {
		allowed = append(allowed, s.AllowedTools...)
	} else {
		// Chat-only: disable every built-in tool and skip anything that
		// could add tools or file access back.
		args = append(args, "--tools", "")
	}
	if s.ClientTools != "" {
		allowed = append(allowed, "mcp__"+ClientToolsServer)
	}
	if len(allowed) > 0 {
		args = append(args, "--allowedTools", strings.Join(allowed, ","))
	}
	if len(s.DisallowedTools) > 0 {
		args = append(args, "--disallowedTools", strings.Join(s.DisallowedTools, ","))
	}
	if s.PermissionMode != "" {
		args = append(args, "--permission-mode", s.PermissionMode)
	}
	if s.MaxTurns > 0 {
		args = append(args, "--max-turns", strconv.Itoa(s.MaxTurns))
	}
	if s.ToolsEnabled {
		for _, d := range s.AddDirs {
			args = append(args, "--add-dir", d)
		}
		if s.MCPConfig != "" {
			args = append(args, "--mcp-config", s.MCPConfig)
		}
	}
	if s.ClientTools != "" {
		args = append(args, "--mcp-config", s.ClientTools)
	}
	if s.JSONSchema != "" {
		args = append(args, "--json-schema", s.JSONSchema)
	}
	if s.ResumeID != "" {
		args = append(args, "--resume", s.ResumeID)
	}
	return args
}

// Env returns the child environment: base minus every AGENT_BRIDGE_*
// variable and, in subscription mode, minus upstream API credentials.
func Env(base []string, mode config.AuthMode) []string {
	out := make([]string, 0, len(base))
	for _, kv := range base {
		k, _, _ := strings.Cut(kv, "=")
		if strings.HasPrefix(k, "AGENT_BRIDGE_") {
			continue
		}
		if mode == config.AuthSubscription && (k == config.EnvUpstreamKey || k == config.EnvUpstreamTok) {
			continue
		}
		out = append(out, kv)
	}
	return out
}

// WithMaxOutputTokens limits the output tokens of each model request of the
// process; n <= 0 leaves env unchanged. It replaces any inherited value.
func WithMaxOutputTokens(env []string, n int) []string {
	if n <= 0 {
		return env
	}
	const key = "CLAUDE_CODE_MAX_OUTPUT_TOKENS"
	out := make([]string, 0, len(env)+1)
	for _, kv := range env {
		if !strings.HasPrefix(kv, key+"=") {
			out = append(out, kv)
		}
	}
	return append(out, key+"="+strconv.Itoa(n))
}

// WithClientToolsTimeout sets the CLI's MCP tool timeout to d unless the
// environment already sets it, so a call can wait for the client as long as
// the turn may last.
func WithClientToolsTimeout(env []string, d time.Duration) []string {
	const key = "MCP_TOOL_TIMEOUT"
	for _, kv := range env {
		if strings.HasPrefix(kv, key+"=") {
			return env
		}
	}
	if d <= 0 {
		d = 24 * time.Hour
	}
	return append(env, key+"="+strconv.FormatInt(d.Milliseconds(), 10))
}

// ImageBlock is an image sent to the CLI.
type ImageBlock struct {
	MediaType string
	Data      []byte
}

// UserMessage returns the stream-json stdin line carrying text and images.
// Each image is preceded by a "[image N]" label matching the placeholders
// in the text.
func UserMessage(text string, images []ImageBlock) string {
	type source struct {
		Type      string `json:"type"`
		MediaType string `json:"media_type"`
		Data      string `json:"data"`
	}
	type block struct {
		Type   string  `json:"type"`
		Text   string  `json:"text,omitempty"`
		Source *source `json:"source,omitempty"`
	}
	content := []block{{Type: "text", Text: text}}
	for i, img := range images {
		content = append(content,
			block{Type: "text", Text: fmt.Sprintf("[image %d]", i+1)},
			block{Type: "image", Source: &source{Type: "base64", MediaType: img.MediaType, Data: base64.StdEncoding.EncodeToString(img.Data)}},
		)
	}
	msg := map[string]any{
		"type":               "user",
		"message":            map[string]any{"role": "user", "content": content},
		"parent_tool_use_id": nil,
	}
	b, _ := json.Marshal(msg)
	return string(b) + "\n"
}
