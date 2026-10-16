package runner

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/r3n3r0/agent-bridge/internal/config"
)

// CodexMinVersion is the oldest codex CLI version the bridge is tested
// against.
const CodexMinVersion = "0.162.0"

// maxInlineInstructions bounds the developer instructions passed as a
// command-line value; operating systems limit the size of one argument.
const maxInlineInstructions = 100_000

// jsonObjectSchema is the schema the server uses for response_format
// json_object. The codex CLI only accepts strict schemas, so this case is
// expressed as an instruction instead.
const jsonObjectSchema = `{"type":"object"}`

const jsonObjectInstruction = "Your final answer must be a single JSON object, with no other text before or after it."

// Defaults of the codex backend.
const (
	codexDefaultSandbox  = "workspace-write"
	codexNoToolTimeout   = 24 * time.Hour
	codexToolStartupSecs = 30
)

// codexEffort maps EffortLevels to the codex reasoning effort levels.
var codexEffort = map[string]string{
	"none": "none", "minimal": "minimal", "low": "low", "medium": "medium",
	"high": "high", "xhigh": "xhigh", "max": "xhigh",
}

// codexChatOnlyFeatures are the features that give the model tools of its
// own; chat-only agents turn them off. Unknown names are ignored by the CLI,
// so the list is safe across versions.
var codexChatOnlyFeatures = []string{
	"shell_tool", "unified_exec", "view_image", "multi_agent", "goals", "apps",
	"plugins", "image_generation", "browser_use", "computer_use", "tool_suggest",
	"skill_search", "sleep_tool",
}

// CodexBackend drives the codex CLI: "codex exec --json" (or "codex exec
// resume"), the prompt on stdin, every setting as a "-c key=value"
// override, images and schemas as temporary files.
type CodexBackend struct{}

var _ Backend = CodexBackend{}

// Name implements Backend.
func (CodexBackend) Name() string { return config.BackendCodex }

// MinVersion implements Backend.
func (CodexBackend) MinVersion() string { return CodexMinVersion }

// NewParser implements Backend.
func (CodexBackend) NewParser() Parser { return &codexParser{} }

// CumulativeUsage implements Backend: turn.completed reports the usage of
// the whole thread.
func (CodexBackend) CumulativeUsage() bool { return true }

// ResumeMissing implements Backend.
func (CodexBackend) ResumeMissing(out *Outcome) bool {
	text := strings.ToLower(out.Stderr)
	if out.Result != nil && !out.Result.IsError {
		return false
	}
	return strings.Contains(text, "no rollout found") || strings.Contains(text, "thread/resume failed")
}

// Prepare implements Backend.
func (CodexBackend) Prepare(t Turn) (*Invocation, error) {
	b := &codexBuilder{spec: t.Spec}
	inv, err := b.build(t)
	if err != nil {
		b.cleanup()
		return nil, err
	}
	return inv, nil
}

// codexBuilder accumulates the arguments and temporary files of one
// invocation.
type codexBuilder struct {
	spec Spec
	args []string
	dir  string
}

func (b *codexBuilder) set(key, value string) { b.args = append(b.args, "-c", key+"="+value) }

func (b *codexBuilder) cleanup() {
	if b.dir != "" {
		os.RemoveAll(b.dir)
	}
}

// file writes a private temporary file and returns its path.
func (b *codexBuilder) file(name string, data []byte) (string, error) {
	if b.dir == "" {
		dir, err := os.MkdirTemp("", "agent-bridge-codex-")
		if err != nil {
			return "", err
		}
		b.dir = dir
	}
	p := filepath.Join(b.dir, name)
	return p, os.WriteFile(p, data, 0o600)
}

func (b *codexBuilder) build(t Turn) (*Invocation, error) {
	s := b.spec
	b.args = []string{"exec"}
	if s.ResumeID != "" {
		b.args = append(b.args, "resume")
	}
	b.args = append(b.args, "--json", "--skip-git-repo-check")
	if s.Model != "" {
		b.args = append(b.args, "-m", s.Model)
	}
	b.set("approval_policy", tomlString("never"))
	if level, ok := codexEffort[s.Effort]; ok {
		b.set("model_reasoning_effort", tomlString(level))
	}
	if err := b.output(); err != nil {
		return nil, err
	}
	if err := b.tools(t.ToolTimeout); err != nil {
		return nil, err
	}
	for i, img := range t.Images {
		p, err := b.file(fmt.Sprintf("image-%d%s", i+1, imageExt(img.MediaType)), img.Data)
		if err != nil {
			return nil, err
		}
		b.args = append(b.args, "--image="+p)
	}
	if s.ResumeID != "" {
		b.args = append(b.args, s.ResumeID)
	}
	b.args = append(b.args, "-")
	return &Invocation{
		Args:    b.args,
		Env:     FilterEnv(t.BaseEnv, t.AuthMode, config.CredentialEnv(config.BackendCodex)),
		Stdin:   t.Prompt,
		Cleanup: b.cleanup,
	}, nil
}

// output sets the system prompt and the structured output schema.
func (b *codexBuilder) output() error {
	s := b.spec
	instructions := s.SystemPrompt
	switch s.JSONSchema {
	case "":
	case jsonObjectSchema:
		instructions = joinNonEmpty(instructions, jsonObjectInstruction)
	default:
		p, err := b.file("schema.json", []byte(s.JSONSchema))
		if err != nil {
			return err
		}
		b.args = append(b.args, "--output-schema", p)
	}
	if instructions == "" {
		return nil
	}
	if s.SystemPromptMode == config.PromptModeReplace {
		p, err := b.file("instructions.md", []byte(instructions))
		if err != nil {
			return err
		}
		b.set("model_instructions_file", tomlString(p))
		return nil
	}
	if len(instructions) > maxInlineInstructions {
		return fmt.Errorf("system prompt of %d bytes exceeds the %d bytes the codex backend can append; use the replace mode",
			len(instructions), maxInlineInstructions)
	}
	b.set("developer_instructions", tomlString(instructions))
	return nil
}

// tools sets the sandbox, the built-in tools and the MCP servers.
func (b *codexBuilder) tools(toolTimeout time.Duration) error {
	s := b.spec
	if !s.ToolsEnabled {
		b.set("sandbox_mode", tomlString("read-only"))
		for _, f := range codexChatOnlyFeatures {
			b.set("features."+f, "false")
		}
		b.set("web_search", tomlString("disabled"))
	} else {
		sandbox := s.Sandbox
		if sandbox == "" {
			sandbox = codexDefaultSandbox
		}
		b.set("sandbox_mode", tomlString(sandbox))
		if len(s.AddDirs) > 0 {
			b.set("sandbox_workspace_write.writable_roots", tomlStrings(s.AddDirs))
		}
		if s.MCPConfig != "" {
			if err := b.mcp(s.MCPConfig, 0); err != nil {
				return err
			}
		}
	}
	if s.ClientTools != "" {
		if toolTimeout <= 0 {
			toolTimeout = codexNoToolTimeout
		}
		return b.mcp(s.ClientTools, toolTimeout)
	}
	return nil
}

// mcp adds the servers of an MCP configuration file; toolTimeout, if set,
// is the call timeout of every server of the file.
func (b *codexBuilder) mcp(path string, toolTimeout time.Duration) error {
	servers, err := readMCPServers(path)
	if err != nil {
		return err
	}
	for _, srv := range servers {
		for _, kv := range srv.overrides() {
			b.set(kv[0], kv[1])
		}
		// Servers come from the operator or are the client's own functions:
		// their tools run without asking, as approvals cannot be answered.
		b.set("mcp_servers."+srv.Name+".default_tools_approval_mode", tomlString("approve"))
		if toolTimeout > 0 {
			key := "mcp_servers." + srv.Name
			b.set(key+".tool_timeout_sec", strconv.FormatInt(int64(toolTimeout/time.Second), 10))
			b.set(key+".startup_timeout_sec", strconv.Itoa(codexToolStartupSecs))
		}
	}
	return nil
}

func imageExt(mediaType string) string {
	switch mediaType {
	case "image/jpeg":
		return ".jpg"
	case "image/gif":
		return ".gif"
	case "image/webp":
		return ".webp"
	}
	return ".png"
}

func joinNonEmpty(a, b string) string {
	if a == "" {
		return b
	}
	return a + "\n\n" + b
}
