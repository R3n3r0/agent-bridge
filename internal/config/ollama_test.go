package config

import (
	"strings"
	"testing"
)

func TestOllamaSettings(t *testing.T) {
	cfg, err := Load(flags(t), env(nil))
	if err != nil || cfg.OllamaURL != "" {
		t.Fatalf("default %q %v", cfg.OllamaURL, err)
	}
	cfg, err = Load(flags(t), env(map[string]string{EnvOllamaURL: "http://ollama:11434", EnvOllamaClientURL: "http://127.0.0.1:11434/v1"}))
	if err != nil || cfg.OllamaURL != "http://ollama:11434" || cfg.OllamaClientURL != "http://127.0.0.1:11434/v1" {
		t.Fatalf("env %+v %v", cfg, err)
	}
	_, err = Load(flags(t), env(map[string]string{EnvOllamaURL: "ollama:11434"}))
	if err == nil || !strings.Contains(err.Error(), "ollama_url: must be an http or https URL") {
		t.Fatalf("bad url: %v", err)
	}
	_, err = Load(flags(t), env(map[string]string{EnvOllamaClientURL: "http://x/v1"}))
	if err == nil || !strings.Contains(err.Error(), "ollama_client_url: requires ollama_url") {
		t.Fatalf("client without url: %v", err)
	}
}
