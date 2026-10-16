package config

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func defaults() *Config {
	return &Config{
		Listen:               DefaultListen,
		Backend:              BackendDefault,
		Binary:               DefaultBinary,
		CodexBinary:          DefaultCodexBinary,
		KimiBinary:           DefaultKimiBinary,
		KimiRegion:           KimiRegionGlobal,
		MaxConcurrent:        DefaultMaxConcurrent,
		MaxQueue:             DefaultMaxQueue,
		QueueTimeoutSeconds:  DefaultQueueTimeout,
		ShutdownGraceSeconds: DefaultShutdownGrace,
		StreamProgress:       StreamComments,
		BuiltinModels:        true,
		BuiltinAnyModel:      true,
		BuiltinPrefix:        DefaultBuiltinPrefix,
		SystemMessages:       SystemAsPrompt,
		Defaults:             DefaultsSection{TimeoutSeconds: DefaultTimeout},
		Sessions: SessionsSection{
			Store:      SessionStoreMemory,
			TTLHours:   DefaultTTLHours,
			MaxEntries: DefaultMaxEntries,
			OnBusy:     OnBusyWait,
		},
		ClientTools: ClientToolsSection{
			Enabled:            true,
			Mode:               ToolModeStateful,
			WaitTimeoutSeconds: DefaultToolWaitTimeout,
			BatchWindowMs:      DefaultToolBatchWindowMs,
		},
		Images: ImagesSection{
			Enabled:             true,
			FetchURLs:           true,
			MaxBytes:            DefaultImageMaxBytes,
			MaxCount:            DefaultImageMaxCount,
			FetchTimeoutSeconds: DefaultImageFetchTimeout,
		},
		LogFormat: defaultLogFormat,
		LogLevel:  defaultLogLevel,
	}
}

// Load builds the configuration from flags, environment and file, then
// resolves and validates agents. getenv is usually os.Getenv. It does not
// check the authentication mode or the network exposure: see ValidateAuth
// and ValidateNetwork.
func Load(f *Flags, getenv func(string) string) (*Config, error) {
	cfg := defaults()

	path := getenv(EnvConfig)
	if f.isSet("config") || (f != nil && f.Config != "") {
		path = f.Config
	}
	if path != "" {
		abs, err := filepath.Abs(path)
		if err != nil {
			return nil, fmt.Errorf("config: %w", err)
		}
		data, err := os.ReadFile(abs)
		if err != nil {
			return nil, fmt.Errorf("config: %w", err)
		}
		dec := json.NewDecoder(bytes.NewReader(data))
		dec.DisallowUnknownFields()
		if err := dec.Decode(cfg); err != nil {
			return nil, fmt.Errorf("config %s: %w", abs, err)
		}
		cfg.Path = abs
	}

	// Environment.
	if v := getenv(EnvListen); v != "" {
		cfg.Listen = v
	}
	if v, ok := envBool(getenv(EnvAdmin)); ok {
		cfg.Admin = v
	}
	if v, ok := envBool(getenv(EnvAllowRemote)); ok {
		cfg.AllowRemote = v
	}
	if v := getenv(EnvBackend); v != "" {
		cfg.Backend = v
	}
	if v := getenv(EnvKimiRegion); v != "" {
		cfg.KimiRegion = v
	}
	if v := getenv(EnvLogFormat); v != "" {
		cfg.LogFormat = v
	}
	if v := getenv(EnvLogLevel); v != "" {
		cfg.LogLevel = v
	}
	cfg.AuthMode = AuthMode(getenv(EnvAuthMode))
	cfg.APIKey = getenv(EnvAPIKey)

	// Flags.
	if f.isSet("listen") {
		cfg.Listen = f.Listen
	}
	if f.isSet("allow-remote") {
		cfg.AllowRemote = f.AllowRemote
	}
	if f.isSet("max-concurrent") {
		cfg.MaxConcurrent = f.MaxConcurrent
	}
	if f.isSet("claude-bin") {
		cfg.Binary = f.Binary
	}
	if f.isSet("backend") {
		cfg.Backend = f.Backend
	}
	if f.isSet("codex-bin") {
		cfg.CodexBinary = f.CodexBinary
	}
	if f.isSet("kimi-bin") {
		cfg.KimiBinary = f.KimiBinary
	}
	if f.isSet("log-format") {
		cfg.LogFormat = f.LogFormat
	}
	if f.isSet("log-level") {
		cfg.LogLevel = f.LogLevel
	}

	if err := cfg.resolve(); err != nil {
		return nil, err
	}
	return cfg, nil
}

// Dir returns the directory used to resolve relative paths: the directory of
// the configuration file, or the current directory without a file.
func (c *Config) Dir() string {
	if c.Path != "" {
		return filepath.Dir(c.Path)
	}
	wd, err := os.Getwd()
	if err != nil {
		return "."
	}
	return wd
}

func (c *Config) abs(p string) string {
	if p == "" || filepath.IsAbs(p) {
		return p
	}
	return filepath.Join(c.Dir(), p)
}

// envBool parses a boolean environment variable; ok is false when it is
// empty or not a boolean.
func envBool(v string) (value, ok bool) {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "1", "true", "yes", "on":
		return true, true
	case "0", "false", "no", "off":
		return false, true
	}
	return false, false
}
