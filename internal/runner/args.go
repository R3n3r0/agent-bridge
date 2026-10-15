// Package runner is the only part of the bridge that knows the CLI flags and
// its stream-json event format. It builds arguments and environment, runs
// one process per turn, parses events and limits concurrency.
package runner

import (
	"strconv"
	"strings"

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
}

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
	if s.Model != "" {
		args = append(args, "--model", s.Model)
	}
	if s.SystemPrompt != "" {
		if s.SystemPromptMode == config.PromptModeReplace {
			args = append(args, "--system-prompt", s.SystemPrompt)
		} else {
			args = append(args, "--append-system-prompt", s.SystemPrompt)
		}
	}
	if !s.ToolsEnabled {
		// Chat-only: disable every built-in tool and skip anything that
		// could add tools or file access back.
		args = append(args, "--tools", "")
	} else if len(s.AllowedTools) > 0 {
		args = append(args, "--allowedTools", strings.Join(s.AllowedTools, ","))
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
