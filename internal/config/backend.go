package config

// Supported CLI backends (the "backend" setting).
const (
	// BackendDefault is the CLI the bridge was first written for.
	BackendDefault = "claude"
	// BackendCodex is the codex CLI.
	BackendCodex = "codex"
)

// Backend settings.
const (
	EnvBackend         = "AGENT_BRIDGE_BACKEND"
	DefaultCodexBinary = "codex"
	// CodexDefaultAlias is the codex built-in model that runs the model
	// configured in the CLI itself.
	CodexDefaultAlias = "default"
)

// Upstream credential variables of the codex CLI.
const (
	EnvCodexKey    = "CODEX_API_KEY"
	EnvCodexToken  = "CODEX_ACCESS_TOKEN"
	EnvOpenAIKey   = "OPENAI_API_KEY"
	CodexSandboxRO = "read-only"
	CodexSandboxWW = "workspace-write"
	// CodexSandboxFull runs commands without a sandbox; it requires
	// allow_bypass_permissions.
	CodexSandboxFull = "danger-full-access"
)

// CodexSandboxes lists the accepted values of the agent "sandbox" field.
var CodexSandboxes = []string{CodexSandboxRO, CodexSandboxWW, CodexSandboxFull}

// CodexBuiltinAliases are the zero-config models of the codex backend.
var CodexBuiltinAliases = []string{CodexDefaultAlias}

// CredentialEnv returns the upstream credential variables of a backend,
// removed from the child environment in subscription mode.
func CredentialEnv(backend string) []string {
	if backend == BackendCodex {
		return []string{EnvCodexKey, EnvCodexToken, EnvOpenAIKey}
	}
	return []string{EnvUpstreamKey, EnvUpstreamTok}
}

// requiredCredential is the variable api mode requires; refusedCredentials
// are the variables subscription mode refuses to start with.
func requiredCredential(backend string) string {
	if backend == BackendCodex {
		return EnvCodexKey
	}
	return EnvUpstreamKey
}

func refusedCredentials(backend string) []string {
	if backend == BackendCodex {
		return []string{EnvCodexKey, EnvCodexToken}
	}
	return []string{EnvUpstreamKey, EnvUpstreamTok}
}

// BuiltinNames returns the zero-config model aliases of the backend.
func (c *Config) BuiltinNames() []string {
	if c.Backend == BackendCodex {
		return CodexBuiltinAliases
	}
	return BuiltinAliases
}

// defaultAgentModel is the model of agents that do not set one; empty
// means the model configured in the CLI.
func (c *Config) defaultAgentModel() string {
	if c.Backend == BackendCodex {
		return ""
	}
	return DefaultModel
}

// validateCodexAgent checks the agent fields the codex CLI cannot honor.
func (c *Config) validateCodexAgent(a *Agent, f AgentFile, add func(field, format string, args ...any)) {
	if c.Backend != BackendCodex {
		if f.Sandbox != "" {
			add("sandbox", "is only supported by the codex backend")
		}
		return
	}
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
