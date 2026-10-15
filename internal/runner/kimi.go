package runner

import (
	"encoding/base64"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/r3n3r0/agent-bridge/internal/config"
)

// KimiMinVersion is the oldest Kimi Code CLI version the bridge is tested
// against.
const KimiMinVersion = "2.2.0"

// kimiSystemTag wraps the agent's system prompt at the top of the first
// prompt of a session: ACP has no system prompt field.
const kimiSystemTag = "system_instructions"

// KimiBackend drives the Kimi Code CLI as an ACP agent ("kimi acp"): the
// bridge is the ACP client. It creates or resumes the session with the MCP
// servers of the turn, selects model, thinking level and mode, sends the
// prompt (text and images) and answers the agent's permission requests
// according to the agent's tool settings.
type KimiBackend struct{}

var _ Backend = KimiBackend{}

// Name implements Backend.
func (KimiBackend) Name() string { return config.BackendKimi }

// MinVersion implements Backend.
func (KimiBackend) MinVersion() string { return KimiMinVersion }

// CumulativeUsage implements Backend; the CLI reports no token usage.
func (KimiBackend) CumulativeUsage() bool { return false }

// NewParser implements Backend. Prepare always sets the parser of the
// invocation, which depends on the turn; this one only reads output.
func (KimiBackend) NewParser() Parser { return newACPDialogue(acpTurn{}) }

// ResumeMissing implements Backend.
func (KimiBackend) ResumeMissing(out *Outcome) bool {
	if out.Result == nil || !out.Result.IsError {
		return false
	}
	text := strings.ToLower(out.Result.Result)
	return strings.Contains(text, "unknown sessionid") || strings.Contains(text, "not found")
}

// Prepare implements Backend.
func (KimiBackend) Prepare(t Turn) (*Invocation, error) {
	s := t.Spec
	var servers []acpMCPServer
	if s.ToolsEnabled && s.MCPConfig != "" {
		list, err := readMCPServers(s.MCPConfig)
		if err != nil {
			return nil, err
		}
		servers = append(servers, toACPServers(list)...)
	}
	if s.ClientTools != "" {
		list, err := readMCPServers(s.ClientTools)
		if err != nil {
			return nil, err
		}
		servers = append(servers, toACPServers(list)...)
	}
	mode := s.PermissionMode
	if mode == "" || !s.ToolsEnabled {
		mode = "default"
	}
	turn := acpTurn{
		cwd:        t.Dir,
		resumeID:   s.ResumeID,
		model:      s.Model,
		effort:     s.Effort,
		mode:       mode,
		servers:    servers,
		addDirs:    s.AddDirs,
		prompt:     kimiPrompt(s, t.Prompt, t.Images),
		permission: kimiPolicy(s),
	}
	if !s.ToolsEnabled {
		turn.addDirs = nil
	}
	env := FilterEnv(t.BaseEnv, t.AuthMode, config.CredentialEnv(config.BackendKimi))
	env = setDefaultEnv(env, "KIMI_DISABLE_TELEMETRY", "1")
	env = setDefaultEnv(env, "KIMI_CODE_NO_AUTO_UPDATE", "1")
	if s.ClientTools != "" {
		d := t.ToolTimeout
		if d <= 0 {
			d = 24 * time.Hour
		}
		env = setDefaultEnv(env, "KIMI_MCP_TOOL_TIMEOUT_MS", strconv.FormatInt(d.Milliseconds(), 10))
	}
	if s.MaxTurns > 0 {
		env = replaceEnv(env, "KIMI_LOOP_MAX_STEPS_PER_TURN", strconv.Itoa(s.MaxTurns))
	}
	return &Invocation{Args: []string{"acp"}, Env: env, Parser: newACPDialogue(turn), Cleanup: func() {}}, nil
}

// kimiPrompt builds the ACP prompt blocks: the agent's system prompt on new
// sessions, the prompt, a structured output instruction, then the images.
func kimiPrompt(s Spec, prompt string, images []ImageBlock) []acpBlock {
	text := prompt
	if s.SystemPrompt != "" && s.ResumeID == "" {
		text = fmt.Sprintf("<%s>\n%s\n</%s>\n\n%s", kimiSystemTag, s.SystemPrompt, kimiSystemTag, text)
	}
	switch s.JSONSchema {
	case "":
	case jsonObjectSchema:
		text += "\n\n" + jsonObjectInstruction
	default:
		text += "\n\nYour final answer must be a single JSON object matching this JSON schema, with no other text before or after it:\n" + s.JSONSchema
	}
	if strings.HasPrefix(text, "/") {
		// A leading slash would run a CLI command instead of a turn.
		text = " " + text
	}
	blocks := []acpBlock{{Type: "text", Text: text}}
	for _, img := range images {
		blocks = append(blocks, acpBlock{Type: "image", MimeType: img.MediaType, Data: base64.StdEncoding.EncodeToString(img.Data)})
	}
	return blocks
}

// kimiPolicy decides the agent's permission requests: client functions are
// always allowed; chat-only agents allow nothing else; disallowed_tools are
// always refused; in the default mode only allowed_tools are approved, in
// the other modes every request the CLI still asks about is approved.
func kimiPolicy(s Spec) func(tool string) bool {
	clientPrefix := "mcp__" + ClientToolsServer + "__"
	return func(tool string) bool {
		switch {
		case s.ClientTools != "" && (strings.HasPrefix(tool, clientPrefix) || strings.HasPrefix(tool, ClientToolsServer+"__")):
			return true
		case !s.ToolsEnabled, contains(s.DisallowedTools, tool):
			return false
		case s.PermissionMode == "" || s.PermissionMode == "default":
			return contains(s.AllowedTools, tool)
		}
		return true
	}
}

// setDefaultEnv sets key unless env already sets it.
func setDefaultEnv(env []string, key, value string) []string {
	for _, kv := range env {
		if strings.HasPrefix(kv, key+"=") {
			return env
		}
	}
	return append(env, key+"="+value)
}

// replaceEnv sets key, replacing any inherited value.
func replaceEnv(env []string, key, value string) []string {
	out := make([]string, 0, len(env)+1)
	for _, kv := range env {
		if !strings.HasPrefix(kv, key+"=") {
			out = append(out, kv)
		}
	}
	return append(out, key+"="+value)
}
