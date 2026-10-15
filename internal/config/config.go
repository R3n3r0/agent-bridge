// Package config loads the bridge configuration from command-line flags,
// environment variables and an optional JSON file, applies defaults and
// validates the result.
//
// Precedence, highest first: flags, environment, file, defaults.
package config

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

// AuthMode selects how the CLI authenticates upstream.
type AuthMode string

// Supported authentication modes.
const (
	AuthSubscription AuthMode = "subscription"
	AuthAPI          AuthMode = "api"
)

// Environment variable names.
const (
	EnvAuthMode    = "AGENT_BRIDGE_AUTH_MODE"
	EnvAPIKey      = "AGENT_BRIDGE_API_KEY"
	EnvConfig      = "AGENT_BRIDGE_CONFIG"
	EnvListen      = "AGENT_BRIDGE_LISTEN"
	EnvLogFormat   = "AGENT_BRIDGE_LOG_FORMAT"
	EnvLogLevel    = "AGENT_BRIDGE_LOG_LEVEL"
	EnvUpstreamKey = "ANTHROPIC_API_KEY"
	EnvUpstreamTok = "ANTHROPIC_AUTH_TOKEN"
)

// Defaults for top-level settings.
const (
	DefaultListen            = "127.0.0.1:8787"
	DefaultBinary            = "claude"
	DefaultMaxConcurrent     = 2
	DefaultMaxQueue          = 32
	DefaultQueueTimeout      = 300
	DefaultShutdownGrace     = 30
	DefaultTimeout           = 1800
	DefaultModel             = "sonnet"
	DefaultTTLHours          = 168
	DefaultMaxEntries        = 10000
	DefaultSessionsFile      = "sessions.json"
	DefaultToolWaitTimeout   = 600
	DefaultImageMaxBytes     = 5 << 20
	DefaultImageMaxCount     = 20
	DefaultImageFetchTimeout = 20
	DefaultToolBatchWindowMs = 300
	StreamComments           = "comments"
	StreamContent            = "content"
	StreamNone               = "none"
	PromptModeAppend         = "append"
	PromptModeReplace        = "replace"
	SessionStoreMemory       = "memory"
	SessionStoreFile         = "file"
	OnBusyWait               = "wait"
	OnBusyReject             = "reject"
	PermissionBypass         = "bypassPermissions"
	DefaultBuiltinPrefix     = "bridge/"
	maxAgentNameLength       = 64
	defaultLogFormat         = "text"
	defaultLogLevel          = "info"
	defaultPermissionMode    = "default"
)

// PermissionModes lists the accepted values for permission_mode.
var PermissionModes = []string{"default", "acceptEdits", "plan", "bypassPermissions", "dontAsk", "auto"}

// BuiltinAliases are the CLI model aliases registered as zero-config models,
// each under builtin_prefix (e.g. "bridge/opus").
var BuiltinAliases = []string{"opus", "sonnet", "haiku"}

var prefixRe = regexp.MustCompile(`^[a-zA-Z0-9._-]{1,32}/$`)

var agentNameRe = regexp.MustCompile(`^[a-zA-Z0-9._/-]+$`)

// DefaultsSection is the "defaults" object of the file.
type DefaultsSection struct {
	TimeoutSeconds int `json:"timeout_seconds"`
	MaxTurns       int `json:"max_turns"`
}

// SessionsSection is the "sessions" object of the file.
type SessionsSection struct {
	Store      string `json:"store"`
	Path       string `json:"path"`
	TTLHours   int    `json:"ttl_hours"`
	MaxEntries int    `json:"max_entries"`
	OnBusy     string `json:"on_busy"`
}

// ClientToolsSection is the "client_tools" object of the file: functions
// defined by API clients (OpenAI tools) and executed by the clients.
type ClientToolsSection struct {
	Enabled bool `json:"enabled"`
	// Mode is ToolModeStateful (the process waits for the results) or
	// ToolModeStateless (the process stops after returning the calls and
	// the next request starts from the history sent by the client).
	Mode               string `json:"mode"`
	WaitTimeoutSeconds int    `json:"wait_timeout_seconds"`
	BatchWindowMs      int    `json:"batch_window_ms"`
}

// Client function modes.
const (
	ToolModeStateful  = "stateful"
	ToolModeStateless = "stateless"
)

// How request system and developer messages reach the model.
const (
	// SystemAsPrompt puts them at the top of the prompt, in <instructions>.
	SystemAsPrompt = "prompt"
	// SystemAppend appends them to the CLI system prompt.
	SystemAppend = "append"
	// SystemReplace makes them the system prompt, replacing the CLI one.
	SystemReplace = "replace"
)

// ImagesSection is the "images" object of the file: image_url content parts.
type ImagesSection struct {
	Enabled bool `json:"enabled"`
	// FetchURLs allows http(s) image URLs, downloaded by the server;
	// data: URLs are always accepted when images are enabled.
	FetchURLs bool `json:"fetch_urls"`
	// AllowPrivateNetworks allows downloads from non-public addresses.
	AllowPrivateNetworks bool  `json:"allow_private_networks"`
	MaxBytes             int64 `json:"max_bytes"`
	MaxCount             int   `json:"max_count"`
	FetchTimeoutSeconds  int   `json:"fetch_timeout_seconds"`
}

// OverridesSection is the "overrides" object of the file.
type OverridesSection struct {
	AllowedRoots      []string `json:"allowed_roots"`
	AllowTools        bool     `json:"allow_tools"`
	AllowSystemPrompt bool     `json:"allow_system_prompt"`
}

// AgentFile is an agent as written in the file. Pointer fields distinguish
// "absent" from the zero value.
type AgentFile struct {
	Model            string   `json:"model"`
	Description      string   `json:"description"`
	SystemPrompt     string   `json:"system_prompt"`
	SystemPromptMode string   `json:"system_prompt_mode"`
	AllowedTools     []string `json:"allowed_tools"`
	DisallowedTools  []string `json:"disallowed_tools"`
	ToolsEnabled     *bool    `json:"tools_enabled"`
	PermissionMode   string   `json:"permission_mode"`
	MaxTurns         *int     `json:"max_turns"`
	Cwd              string   `json:"cwd"`
	AddDirs          []string `json:"add_dirs"`
	MCPConfig        string   `json:"mcp_config"`
	TimeoutSeconds   *int     `json:"timeout_seconds"`
}

// Agent is a fully resolved and validated agent.
type Agent struct {
	Name             string
	Model            string
	Description      string
	SystemPrompt     string
	SystemPromptMode string
	AllowedTools     []string
	DisallowedTools  []string
	ToolsEnabled     bool
	PermissionMode   string
	MaxTurns         int
	Cwd              string
	AddDirs          []string
	MCPConfig        string
	Timeout          time.Duration
	Builtin          bool
}

// Config is the complete bridge configuration.
type Config struct {
	Listen                 string               `json:"listen"`
	AllowRemote            bool                 `json:"allow_remote"`
	Binary                 string               `json:"claude_binary"`
	MaxConcurrent          int                  `json:"max_concurrent"`
	MaxQueue               int                  `json:"max_queue"`
	QueueTimeoutSeconds    int                  `json:"queue_timeout_seconds"`
	ShutdownGraceSeconds   int                  `json:"shutdown_grace_seconds"`
	StreamProgress         string               `json:"stream_progress"`
	UsageLog               string               `json:"usage_log"`
	Metrics                bool                 `json:"metrics"`
	CORSOrigins            []string             `json:"cors_origins"`
	AllowBypassPermissions bool                 `json:"allow_bypass_permissions"`
	BuiltinModels          bool                 `json:"builtin_models"`
	BuiltinAnyModel        bool                 `json:"builtin_any_model"`
	BuiltinPrefix          string               `json:"builtin_prefix"`
	SystemMessages         string               `json:"system_messages"`
	Defaults               DefaultsSection      `json:"defaults"`
	Sessions               SessionsSection      `json:"sessions"`
	Overrides              OverridesSection     `json:"overrides"`
	ClientTools            ClientToolsSection   `json:"client_tools"`
	Images                 ImagesSection        `json:"images"`
	AgentsFile             map[string]AgentFile `json:"agents"`

	// Values that come only from flags or the environment.
	AuthMode  AuthMode `json:"-"`
	APIKey    string   `json:"-"`
	LogFormat string   `json:"-"`
	LogLevel  string   `json:"-"`

	// Path is the absolute path of the configuration file, empty if none.
	Path string `json:"-"`
	// Agents holds the resolved agents, including built-in models.
	Agents map[string]*Agent `json:"-"`
	// ScratchDir is the working directory of chat-only agents without cwd.
	ScratchDir string `json:"-"`
}

// Flags holds the command-line flags shared by the subcommands.
type Flags struct {
	Config        string
	Listen        string
	AllowRemote   bool
	MaxConcurrent int
	Binary        string
	LogFormat     string
	LogLevel      string
	set           map[string]bool
}

// RegisterFlags defines the serve flags on fs.
func RegisterFlags(fs *flag.FlagSet) *Flags {
	f := &Flags{}
	fs.StringVar(&f.Config, "config", "", "path of the JSON configuration file")
	fs.StringVar(&f.Listen, "listen", "", "listen address (default "+DefaultListen+")")
	fs.BoolVar(&f.AllowRemote, "allow-remote", false, "allow listening on a non-loopback address")
	fs.IntVar(&f.MaxConcurrent, "max-concurrent", 0, "maximum number of concurrent CLI processes")
	fs.StringVar(&f.Binary, "claude-bin", "", "path or name of the CLI binary")
	fs.StringVar(&f.LogFormat, "log-format", "", "log format: text|json")
	fs.StringVar(&f.LogLevel, "log-level", "", "log level: debug|info|warn|error")
	return f
}

// MarkSet records which flags were explicitly given; call after fs.Parse.
func (f *Flags) MarkSet(fs *flag.FlagSet) {
	f.set = map[string]bool{}
	fs.Visit(func(fl *flag.Flag) { f.set[fl.Name] = true })
}

func (f *Flags) isSet(name string) bool { return f != nil && f.set[name] }

func defaults() *Config {
	return &Config{
		Listen:               DefaultListen,
		Binary:               DefaultBinary,
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

func (c *Config) resolve() error {
	var errs []error
	add := func(format string, a ...any) { errs = append(errs, fmt.Errorf(format, a...)) }

	if c.Listen == "" {
		add("listen: must not be empty")
	}
	if c.Binary == "" {
		add("claude_binary: must not be empty")
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
		for _, model := range BuiltinAliases {
			name := c.BuiltinPrefix + model
			c.Agents[name] = c.builtinAgent(name, model)
		}
	}
	return errors.Join(errs...)
}

func (c *Config) resolveAgent(name string, f AgentFile) (*Agent, []error) {
	var errs []error
	add := func(field, format string, a ...any) {
		errs = append(errs, fmt.Errorf("agent %q: %s: %s", name, field, fmt.Sprintf(format, a...)))
	}
	if len(name) == 0 || len(name) > maxAgentNameLength || !agentNameRe.MatchString(name) {
		add("name", "must be 1-%d characters from [a-zA-Z0-9._/-]", maxAgentNameLength)
	}
	if c.isBuiltinName(name) {
		add("name", "conflicts with a built-in model")
	}
	a := &Agent{
		Name:             name,
		Model:            f.Model,
		Description:      f.Description,
		SystemPrompt:     f.SystemPrompt,
		SystemPromptMode: f.SystemPromptMode,
		AllowedTools:     f.AllowedTools,
		DisallowedTools:  f.DisallowedTools,
		ToolsEnabled:     true,
		PermissionMode:   f.PermissionMode,
		MaxTurns:         c.Defaults.MaxTurns,
		MCPConfig:        c.abs(f.MCPConfig),
		Timeout:          time.Duration(c.Defaults.TimeoutSeconds) * time.Second,
	}
	if a.Model == "" {
		a.Model = DefaultModel
	}
	if a.SystemPromptMode == "" {
		a.SystemPromptMode = PromptModeAppend
	}
	if !oneOf(a.SystemPromptMode, PromptModeAppend, PromptModeReplace) {
		add("system_prompt_mode", "must be append or replace, got %q", a.SystemPromptMode)
	}
	if a.PermissionMode == "" {
		a.PermissionMode = defaultPermissionMode
	}
	if !oneOf(a.PermissionMode, PermissionModes...) {
		add("permission_mode", "must be one of %s, got %q", strings.Join(PermissionModes, ", "), a.PermissionMode)
	}
	if a.PermissionMode == PermissionBypass && !c.AllowBypassPermissions {
		add("permission_mode", "bypassPermissions requires allow_bypass_permissions: true")
	}
	if f.ToolsEnabled != nil {
		a.ToolsEnabled = *f.ToolsEnabled
	}
	if f.MaxTurns != nil {
		if *f.MaxTurns < 0 {
			add("max_turns", "must not be negative, got %d", *f.MaxTurns)
		}
		a.MaxTurns = *f.MaxTurns
	}
	if f.TimeoutSeconds != nil {
		if *f.TimeoutSeconds < 0 {
			add("timeout_seconds", "must not be negative, got %d", *f.TimeoutSeconds)
		}
		a.Timeout = time.Duration(*f.TimeoutSeconds) * time.Second
	}
	switch {
	case f.Cwd == "" && a.ToolsEnabled:
		add("cwd", "is required when tools_enabled is true")
	case f.Cwd != "":
		p := c.abs(f.Cwd)
		st, err := os.Stat(p)
		switch {
		case err != nil:
			add("cwd", "%v", err)
		case !st.IsDir():
			add("cwd", "%s is not a directory", p)
		}
		a.Cwd = p
	}
	for _, d := range f.AddDirs {
		a.AddDirs = append(a.AddDirs, c.abs(d))
	}
	return a, errs
}

// isBuiltinName reports whether name is one of the registered aliases.
func (c *Config) isBuiltinName(name string) bool {
	if !c.BuiltinModels {
		return false
	}
	for _, a := range BuiltinAliases {
		if name == c.BuiltinPrefix+a {
			return true
		}
	}
	return false
}

// builtinAgent returns a chat-only agent running model.
func (c *Config) builtinAgent(name, model string) *Agent {
	return &Agent{
		Name:             name,
		Model:            model,
		Description:      "Built-in chat-only model (" + model + ")",
		SystemPromptMode: PromptModeAppend,
		PermissionMode:   defaultPermissionMode,
		MaxTurns:         c.Defaults.MaxTurns,
		Cwd:              c.ScratchDir,
		Timeout:          time.Duration(c.Defaults.TimeoutSeconds) * time.Second,
		Builtin:          true,
	}
}

var modelNameRe = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._\[\]-]{0,99}$`)

// Agent returns the agent for a model name: a configured or built-in agent,
// or, with builtin_any_model, a chat-only agent for any
// name made of the built-in prefix and a model, which is passed to the CLI
// as is.
func (c *Config) Agent(name string) (*Agent, bool) {
	if a, ok := c.Agents[name]; ok {
		return a, true
	}
	model, ok := strings.CutPrefix(name, c.BuiltinPrefix)
	if !ok || !c.BuiltinModels || !c.BuiltinAnyModel || !modelNameRe.MatchString(model) {
		return nil, false
	}
	return c.builtinAgent(name, model), true
}

// SetScratchDir assigns dir as working directory to every agent without one
// (built-in models and chat-only agents without cwd).
func (c *Config) SetScratchDir(dir string) {
	c.ScratchDir = dir
	for _, a := range c.Agents {
		if a.Cwd == "" {
			a.Cwd = dir
		}
	}
}

// AgentNames returns the agent names, configured agents first, each group
// sorted alphabetically.
func (c *Config) AgentNames() []string {
	var user, builtin []string
	for n, a := range c.Agents {
		if a.Builtin {
			builtin = append(builtin, n)
		} else {
			user = append(user, n)
		}
	}
	sort.Strings(user)
	sort.Strings(builtin)
	return append(user, builtin...)
}

// ValidateAuth checks that the authentication mode and the upstream
// credentials in the environment are consistent.
func (c *Config) ValidateAuth(getenv func(string) string) error {
	switch c.AuthMode {
	case AuthSubscription:
		for _, k := range []string{EnvUpstreamKey, EnvUpstreamTok} {
			if getenv(k) != "" {
				return fmt.Errorf("%s=subscription but %s is set: unset it, or use %s=api to run with an API key", EnvAuthMode, k, EnvAuthMode)
			}
		}
	case AuthAPI:
		if getenv(EnvUpstreamKey) == "" {
			return fmt.Errorf("%s=api requires %s to be set", EnvAuthMode, EnvUpstreamKey)
		}
	case "":
		return fmt.Errorf("%s is required: set it to subscription (CLI login) or api (API key)", EnvAuthMode)
	default:
		return fmt.Errorf("%s=%q is not valid: use subscription or api", EnvAuthMode, string(c.AuthMode))
	}
	return nil
}

// ValidateNetwork refuses a non-loopback listen address unless remote access
// is explicitly allowed and a client API key is set.
func (c *Config) ValidateNetwork() error {
	if IsLoopback(c.Listen) {
		return nil
	}
	if !c.AllowRemote {
		return fmt.Errorf("listen address %s is not loopback: set allow_remote: true (or -allow-remote) to expose the server", c.Listen)
	}
	if c.APIKey == "" {
		return fmt.Errorf("listen address %s is not loopback: %s must be set when remote access is allowed", c.Listen, EnvAPIKey)
	}
	return nil
}

// IsLoopback reports whether the host part of addr is a loopback address.
func IsLoopback(addr string) bool {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return false
	}
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func canonicalDir(p string) (string, error) {
	p, err := filepath.Abs(p)
	if err != nil {
		return "", err
	}
	p, err = filepath.EvalSymlinks(p)
	if err != nil {
		return "", err
	}
	st, err := os.Stat(p)
	if err != nil {
		return "", err
	}
	if !st.IsDir() {
		return "", fmt.Errorf("%s is not a directory", p)
	}
	return p, nil
}

// CanonicalDir returns the absolute, symlink-free form of an existing
// directory.
func CanonicalDir(p string) (string, error) { return canonicalDir(p) }

func oneOf(v string, allowed ...string) bool {
	for _, a := range allowed {
		if v == a {
			return true
		}
	}
	return false
}
