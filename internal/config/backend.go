package config

// Supported CLI backends (the "backend" setting).
const (
	// BackendDefault is the CLI the bridge was first written for.
	BackendDefault = "claude"
	// BackendCodex is the codex CLI.
	BackendCodex = "codex"
	// BackendKimi is the Kimi Code CLI, driven over ACP.
	BackendKimi = "kimi"
)

// EnvBackend selects the backend from the environment.
const EnvBackend = "AGENT_BRIDGE_BACKEND"

// DefaultAlias is the built-in model of the codex and kimi backends that
// runs the model configured in the CLI itself.
const DefaultAlias = "default"

// CredentialEnv returns the upstream credential variables of a backend,
// removed from the child environment in subscription mode.
func CredentialEnv(backend string) []string {
	switch backend {
	case BackendCodex:
		return []string{EnvCodexKey, EnvCodexToken, EnvOpenAIKey}
	case BackendKimi:
		return []string{EnvKimiModelKey, EnvKimiModelName, EnvKimiKey}
	}
	return []string{EnvUpstreamKey, EnvUpstreamTok}
}

// requiredCredentials are the variables api mode requires.
func requiredCredentials(backend string) []string {
	switch backend {
	case BackendCodex:
		return []string{EnvCodexKey}
	case BackendKimi:
		return []string{EnvKimiModelName, EnvKimiModelKey}
	}
	return []string{EnvUpstreamKey}
}

// refusedCredentials are the variables subscription mode refuses to start
// with.
func refusedCredentials(backend string) []string {
	switch backend {
	case BackendCodex:
		return []string{EnvCodexKey, EnvCodexToken}
	case BackendKimi:
		return []string{EnvKimiModelKey, EnvKimiKey}
	}
	return []string{EnvUpstreamKey, EnvUpstreamTok}
}

// BuiltinNames returns the zero-config model aliases of the backend.
func (c *Config) BuiltinNames() []string {
	if c.Backend == BackendCodex || c.Backend == BackendKimi {
		return []string{DefaultAlias}
	}
	return BuiltinAliases
}

// defaultAgentModel is the model of agents that do not set one; empty
// means the model configured in the CLI.
func (c *Config) defaultAgentModel() string {
	if c.Backend == BackendCodex || c.Backend == BackendKimi {
		return ""
	}
	return DefaultModel
}

// validateBackendAgent checks the agent fields the backend cannot honor.
func (c *Config) validateBackendAgent(a *Agent, f AgentFile, add func(field, format string, args ...any)) {
	if c.Backend != BackendCodex && f.Sandbox != "" {
		add("sandbox", "is only supported by the codex backend")
	}
	switch c.Backend {
	case BackendCodex:
		c.validateCodexAgent(a, f, add)
	case BackendKimi:
		c.validateKimiAgent(a, f, add)
	default:
		if !oneOf(a.PermissionMode, PermissionModes...) {
			add("permission_mode", "must be one of %s, got %q", joinQuoted(PermissionModes), a.PermissionMode)
		}
		if a.PermissionMode == PermissionBypass && !c.AllowBypassPermissions {
			add("permission_mode", "bypassPermissions requires allow_bypass_permissions: true")
		}
	}
}
