package config

import "testing"

func TestBooleanEnvironment(t *testing.T) {
	cfg, err := Load(flags(t), env(map[string]string{EnvAdmin: "true", EnvAllowRemote: "1"}))
	if err != nil || !cfg.Admin || !cfg.AllowRemote {
		t.Fatalf("cfg %+v %v", cfg, err)
	}
	cfg, _ = Load(flags(t), env(map[string]string{EnvAdmin: "off", EnvAllowRemote: "maybe"}))
	if cfg.Admin || cfg.AllowRemote {
		t.Fatalf("cfg %+v", cfg)
	}
	cfg, _ = Load(flags(t, "-allow-remote=false"), env(map[string]string{EnvAllowRemote: "yes"}))
	if cfg.AllowRemote {
		t.Fatal("the flag must win over the environment")
	}
	if cfg.DefaultCLIBinary != DefaultBinary {
		t.Fatalf("default CLI binary %q", cfg.DefaultCLIBinary)
	}
}
