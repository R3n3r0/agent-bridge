package runner

import (
	"strings"

	"github.com/r3n3r0/agent-bridge/internal/config"
)

// Env returns the child environment of the default CLI: base minus every
// AGENT_BRIDGE_* variable and, in subscription mode, minus upstream API
// credentials.
func Env(base []string, mode config.AuthMode) []string {
	return FilterEnv(base, mode, config.CredentialEnv(config.BackendDefault))
}

// FilterEnv returns base minus every AGENT_BRIDGE_* variable and, in
// subscription mode, minus the credential variables listed in creds.
func FilterEnv(base []string, mode config.AuthMode, creds []string) []string {
	out := make([]string, 0, len(base))
	for _, kv := range base {
		k, _, _ := strings.Cut(kv, "=")
		if strings.HasPrefix(k, "AGENT_BRIDGE_") {
			continue
		}
		if mode == config.AuthSubscription && contains(creds, k) {
			continue
		}
		out = append(out, kv)
	}
	return out
}

func contains(list []string, v string) bool {
	for _, s := range list {
		if s == v {
			return true
		}
	}
	return false
}
