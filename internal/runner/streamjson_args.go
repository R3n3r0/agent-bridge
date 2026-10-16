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

// MinCLIVersion is the oldest version of the default CLI the bridge is tested
// against. It is the release that introduced the 10 MB stdin limit the
// prompt builder relies on.
const MinCLIVersion = "2.1.128"

// effortLevels maps EffortLevels to the levels of the default CLI.
var effortLevels = map[string]string{
	"none": "low", "minimal": "low", "low": "low", "medium": "medium",
	"high": "high", "xhigh": "xhigh", "max": "max",
}

// Args returns the arguments of the default CLI for s, in a fixed order. The prompt is
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
		if level, ok := effortLevels[s.Effort]; ok {
			args = append(args, "--effort", level)
		}
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
