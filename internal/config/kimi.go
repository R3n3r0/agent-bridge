package config

// Upstream credential variables of the Kimi Code CLI. KIMI_MODEL_NAME and
// KIMI_MODEL_API_KEY define the model the CLI uses in api mode.
const (
	EnvKimiModelName = "KIMI_MODEL_NAME"
	EnvKimiModelKey  = "KIMI_MODEL_API_KEY"
	EnvKimiKey       = "KIMI_API_KEY"
)

// Kimi settings.
const (
	DefaultKimiBinary = "kimi"
	// EnvKimiRegion selects the Kimi login region from the environment.
	EnvKimiRegion = "AGENT_BRIDGE_KIMI_REGION"
	// KimiRegionGlobal logs in on kimi.ai, the service outside mainland
	// China; KimiRegionChina logs in on kimi.com.
	KimiRegionGlobal = "global"
	KimiRegionChina  = "mainland-cn"
	// KimiModeYolo approves every tool call; it requires
	// allow_bypass_permissions.
	KimiModeYolo = "yolo"
)

// KimiModes lists the accepted values of permission_mode with the kimi
// backend: the CLI's session modes.
var KimiModes = []string{"default", "plan", "auto", KimiModeYolo}

// validateKimiAgent checks the agent fields of the kimi backend.
func (c *Config) validateKimiAgent(a *Agent, _ AgentFile, add func(field, format string, args ...any)) {
	if !oneOf(a.PermissionMode, KimiModes...) {
		add("permission_mode", "must be one of %s with the kimi backend, got %q", joinQuoted(KimiModes), a.PermissionMode)
	}
	if a.PermissionMode == KimiModeYolo && !c.AllowBypassPermissions {
		add("permission_mode", "yolo requires allow_bypass_permissions: true")
	}
}
