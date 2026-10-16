package config

// Upstream credential variables of the codex CLI.
const (
	EnvCodexKey   = "CODEX_API_KEY"
	EnvCodexToken = "CODEX_ACCESS_TOKEN"
	EnvOpenAIKey  = "OPENAI_API_KEY"
)

// Codex settings.
const (
	DefaultCodexBinary = "codex"
	CodexSandboxRO     = "read-only"
	CodexSandboxWW     = "workspace-write"
	// CodexSandboxFull runs commands without a sandbox; it requires
	// allow_bypass_permissions.
	CodexSandboxFull = "danger-full-access"
)

// CodexSandboxes lists the accepted values of the agent "sandbox" field.
var CodexSandboxes = []string{CodexSandboxRO, CodexSandboxWW, CodexSandboxFull}

// validateCodexAgent checks the agent fields the codex CLI cannot honor.
func (c *Config) validateCodexAgent(a *Agent, f AgentFile, add func(field, format string, args ...any)) {
	if f.PermissionMode != "" && f.PermissionMode != defaultPermissionMode {
		add("permission_mode", "is not supported by the codex backend: use sandbox")
	}
	if len(f.AllowedTools) > 0 || len(f.DisallowedTools) > 0 {
		add("allowed_tools", "allowed_tools and disallowed_tools are not supported by the codex backend")
	}
	if f.MaxTurns != nil && *f.MaxTurns > 0 {
		add("max_turns", "is not supported by the codex backend")
	}
	a.MaxTurns = 0
	if a.Sandbox == "" {
		a.Sandbox = CodexSandboxWW
	}
	if !oneOf(a.Sandbox, CodexSandboxes...) {
		add("sandbox", "must be one of read-only, workspace-write, danger-full-access, got %q", a.Sandbox)
	}
	if a.Sandbox == CodexSandboxFull && !c.AllowBypassPermissions {
		add("sandbox", "danger-full-access requires allow_bypass_permissions: true")
	}
}
