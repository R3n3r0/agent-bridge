package config

import (
	"strings"
	"testing"
)

func TestCodexBackend(t *testing.T) {
	dir := t.TempDir()
	p := writeConfig(t, dir, `{"backend": "codex", "codex_binary": "/opt/codex", "allow_bypass_permissions": true, "agents": {
		"coder": {"cwd": ".", "sandbox": "danger-full-access"},
		"reader": {"cwd": ".", "model": "gpt-x"},
		"chat": {"tools_enabled": false}}}`)
	cfg, err := Load(flags(t, "-config", p), env(nil))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Binary != "/opt/codex" || cfg.Backend != BackendCodex {
		t.Fatalf("binary %q backend %q", cfg.Binary, cfg.Backend)
	}
	if a := cfg.Agents["coder"]; a.Sandbox != CodexSandboxFull || a.Model != "" || a.MaxTurns != 0 {
		t.Fatalf("coder %+v", a)
	}
	if a := cfg.Agents["reader"]; a.Sandbox != CodexSandboxWW || a.Model != "gpt-x" {
		t.Fatalf("reader %+v", a)
	}
	if a, ok := cfg.Agent("bridge/default"); !ok || a.Model != "" || !a.Builtin || !strings.Contains(a.Description, "configured in the CLI") {
		t.Fatalf("default alias %+v", a)
	}
	if a, ok := cfg.Agent("bridge/gpt-5"); !ok || a.Model != "gpt-5" {
		t.Fatalf("any model %+v", a)
	}
	if _, ok := cfg.Agent("bridge/sonnet"); !ok {
		t.Fatal("any model names are passed through")
	}
	if _, ok := cfg.Agents["bridge/sonnet"]; ok {
		t.Fatal("aliases of the default CLI must not be registered")
	}
}

func TestCodexFlagsAndEnv(t *testing.T) {
	cfg, err := Load(flags(t, "-codex-bin", "/x/codex"), env(map[string]string{EnvBackend: "codex"}))
	if err != nil || cfg.Backend != BackendCodex || cfg.Binary != "/x/codex" {
		t.Fatalf("cfg %+v err %v", cfg, err)
	}
	cfg, err = Load(flags(t, "-backend", "claude"), env(map[string]string{EnvBackend: "codex"}))
	if err != nil || cfg.Backend != BackendDefault || cfg.Binary != DefaultBinary {
		t.Fatalf("flag must win: %+v %v", cfg, err)
	}
}

func TestCodexValidation(t *testing.T) {
	dir := t.TempDir()
	p := writeConfig(t, dir, `{"backend": "codex", "codex_binary": "", "defaults": {"max_turns": 3}, "agents": {
		"a": {"cwd": ".", "permission_mode": "acceptEdits", "allowed_tools": ["Read"], "max_turns": 2},
		"b": {"cwd": ".", "sandbox": "danger-full-access"},
		"c": {"cwd": ".", "sandbox": "everything"}}}`)
	_, err := Load(flags(t, "-config", p), env(nil))
	if err == nil {
		t.Fatal("expected errors")
	}
	for _, want := range []string{
		"codex_binary: must not be empty",
		"defaults.max_turns: is not supported by the codex backend",
		`agent "a": permission_mode: is not supported by the codex backend`,
		`agent "a": allowed_tools`,
		`agent "a": max_turns: is not supported`,
		`agent "b": sandbox: danger-full-access requires allow_bypass_permissions`,
		`agent "c": sandbox: must be one of`,
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("missing %q in:\n%v", want, err)
		}
	}

	p = writeConfig(t, dir, `{"agents": {"a": {"cwd": ".", "sandbox": "read-only"}}}`)
	if _, err := Load(flags(t, "-config", p), env(nil)); err == nil || !strings.Contains(err.Error(), "only supported by the codex backend") {
		t.Fatalf("err %v", err)
	}
	if _, err := Load(flags(t, "-backend", "other"), env(nil)); err == nil || !strings.Contains(err.Error(), "backend: must be claude or codex") {
		t.Fatalf("err %v", err)
	}
}

func TestCodexValidateAuth(t *testing.T) {
	cfg := &Config{Backend: BackendCodex, AuthMode: AuthSubscription}
	if err := cfg.ValidateAuth(env(map[string]string{EnvOpenAIKey: "x"})); err != nil {
		t.Fatalf("OPENAI_API_KEY is not used by codex exec and is only removed: %v", err)
	}
	if err := cfg.ValidateAuth(env(map[string]string{EnvCodexKey: "x"})); err == nil {
		t.Fatal("subscription mode must refuse CODEX_API_KEY")
	}
	cfg.AuthMode = AuthAPI
	if err := cfg.ValidateAuth(env(map[string]string{EnvUpstreamKey: "x"})); err == nil || !strings.Contains(err.Error(), EnvCodexKey) {
		t.Fatalf("api mode must require %s: %v", EnvCodexKey, err)
	}
	if err := cfg.ValidateAuth(env(map[string]string{EnvCodexKey: "x"})); err != nil {
		t.Fatal(err)
	}
	if got := CredentialEnv(BackendCodex); len(got) != 3 {
		t.Fatalf("credentials %v", got)
	}
}
