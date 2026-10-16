package config

import (
	"errors"
	"fmt"
	"sort"
	"strings"
)

func (c *Config) resolve() error {
	var errs []error
	add := func(format string, a ...any) { errs = append(errs, fmt.Errorf(format, a...)) }

	if c.Listen == "" {
		add("listen: must not be empty")
	}
	c.DefaultCLIBinary = c.Binary
	switch c.Backend {
	case BackendDefault:
		if c.Binary == "" {
			add("claude_binary: must not be empty")
		}
	case BackendCodex:
		if c.CodexBinary == "" {
			add("codex_binary: must not be empty")
		}
		if c.Defaults.MaxTurns > 0 {
			add("defaults.max_turns: is not supported by the codex backend")
		}
		c.Binary = c.CodexBinary
	case BackendKimi:
		if c.KimiBinary == "" {
			add("kimi_binary: must not be empty")
		}
		c.Binary = c.KimiBinary
	default:
		add("backend: must be claude, codex or kimi, got %q", c.Backend)
	}
	if c.MaxConcurrent < 1 {
		add("max_concurrent: must be at least 1, got %d", c.MaxConcurrent)
	}
	for name, v := range map[string]int{
		"max_queue":                         c.MaxQueue,
		"queue_timeout_seconds":             c.QueueTimeoutSeconds,
		"shutdown_grace_seconds":            c.ShutdownGraceSeconds,
		"defaults.timeout_seconds":          c.Defaults.TimeoutSeconds,
		"defaults.max_turns":                c.Defaults.MaxTurns,
		"sessions.ttl_hours":                c.Sessions.TTLHours,
		"sessions.max_entries":              c.Sessions.MaxEntries,
		"client_tools.wait_timeout_seconds": c.ClientTools.WaitTimeoutSeconds,
		"client_tools.batch_window_ms":      c.ClientTools.BatchWindowMs,
		"images.max_count":                  c.Images.MaxCount,
		"images.fetch_timeout_seconds":      c.Images.FetchTimeoutSeconds,
	} {
		if v < 0 {
			add("%s: must not be negative, got %d", name, v)
		}
	}
	if c.Images.MaxBytes < 0 {
		add("images.max_bytes: must not be negative, got %d", c.Images.MaxBytes)
	}
	if !prefixRe.MatchString(c.BuiltinPrefix) {
		add("builtin_prefix: must be 1-32 characters from [a-zA-Z0-9._-] followed by \"/\", got %q", c.BuiltinPrefix)
	}
	if !oneOf(c.SystemMessages, SystemAsPrompt, SystemAppend, SystemReplace) {
		add("system_messages: must be prompt, append or replace, got %q", c.SystemMessages)
	}
	if !oneOf(c.ClientTools.Mode, ToolModeStateful, ToolModeStateless) {
		add("client_tools.mode: must be stateful or stateless, got %q", c.ClientTools.Mode)
	}
	if !oneOf(c.StreamProgress, StreamComments, StreamContent, StreamNone) {
		add("stream_progress: must be comments, content or none, got %q", c.StreamProgress)
	}
	if !oneOf(c.LogFormat, "text", "json") {
		add("log format: must be text or json, got %q", c.LogFormat)
	}
	if !oneOf(c.LogLevel, "debug", "info", "warn", "error") {
		add("log level: must be debug, info, warn or error, got %q", c.LogLevel)
	}
	for _, o := range c.CORSOrigins {
		if o == "" || strings.Contains(o, "*") {
			add("cors_origins: wildcards and empty origins are not allowed, got %q", o)
		}
	}
	if !oneOf(c.Sessions.Store, SessionStoreMemory, SessionStoreFile) {
		add("sessions.store: must be memory or file, got %q", c.Sessions.Store)
	}
	if !oneOf(c.Sessions.OnBusy, OnBusyWait, OnBusyReject) {
		add("sessions.on_busy: must be wait or reject, got %q", c.Sessions.OnBusy)
	}
	if c.Sessions.Path == "" {
		c.Sessions.Path = DefaultSessionsFile
	}
	c.Sessions.Path = c.abs(c.Sessions.Path)
	if c.UsageLog != "" {
		c.UsageLog = c.abs(c.UsageLog)
	}

	roots := make([]string, 0, len(c.Overrides.AllowedRoots))
	for _, r := range c.Overrides.AllowedRoots {
		p, err := canonicalDir(c.abs(r))
		if err != nil {
			add("overrides.allowed_roots: %q: %v", r, err)
			continue
		}
		roots = append(roots, p)
	}
	c.Overrides.AllowedRoots = roots

	c.Agents = map[string]*Agent{}
	names := make([]string, 0, len(c.AgentsFile))
	for n := range c.AgentsFile {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, name := range names {
		a, aerrs := c.resolveAgent(name, c.AgentsFile[name])
		errs = append(errs, aerrs...)
		if a != nil {
			c.Agents[name] = a
		}
	}
	if c.BuiltinModels {
		for _, model := range c.BuiltinNames() {
			name := c.BuiltinPrefix + model
			c.Agents[name] = c.builtinAgent(name, model)
		}
	}
	return errors.Join(errs...)
}
