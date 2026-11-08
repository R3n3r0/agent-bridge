package config

import (
	"fmt"
	"net/url"
)

// Environment variables of the embedding models managed through Ollama.
const (
	EnvOllamaURL       = "AGENT_BRIDGE_OLLAMA_URL"
	EnvOllamaClientURL = "AGENT_BRIDGE_OLLAMA_CLIENT_URL"
)

// validateOllama checks ollama_url and ollama_client_url: both optional,
// absolute http(s) URLs when set.
func (c *Config) validateOllama(add func(format string, a ...any)) {
	for name, v := range map[string]string{"ollama_url": c.OllamaURL, "ollama_client_url": c.OllamaClientURL} {
		if v == "" {
			continue
		}
		if err := checkHTTPURL(v); err != nil {
			add("%s: %v", name, err)
		}
	}
	if c.OllamaClientURL != "" && c.OllamaURL == "" {
		add("ollama_client_url: requires ollama_url")
	}
}

func checkHTTPURL(v string) error {
	u, err := url.Parse(v)
	if err != nil {
		return err
	}
	if (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return fmt.Errorf("must be an http or https URL, got %q", v)
	}
	return nil
}
