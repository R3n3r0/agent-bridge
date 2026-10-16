// Package config loads the bridge configuration from command-line flags,
// environment variables and an optional JSON file, applies defaults and
// validates the result.
//
// Precedence, highest first: flags, environment, file, defaults.
package config

import (
	"regexp"
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
	EnvAdmin       = "AGENT_BRIDGE_ADMIN"
	EnvAllowRemote = "AGENT_BRIDGE_ALLOW_REMOTE"
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

// Config is the complete bridge configuration.
type Config struct {
	Listen                 string               `json:"listen"`
	AllowRemote            bool                 `json:"allow_remote"`
	Backend                string               `json:"backend"`
	Binary                 string               `json:"claude_binary"`
	CodexBinary            string               `json:"codex_binary"`
	KimiBinary             string               `json:"kimi_binary"`
	MaxConcurrent          int                  `json:"max_concurrent"`
	MaxQueue               int                  `json:"max_queue"`
	QueueTimeoutSeconds    int                  `json:"queue_timeout_seconds"`
	ShutdownGraceSeconds   int                  `json:"shutdown_grace_seconds"`
	StreamProgress         string               `json:"stream_progress"`
	UsageLog               string               `json:"usage_log"`
	Metrics                bool                 `json:"metrics"`
	Admin                  bool                 `json:"admin"`
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
	// DefaultCLIBinary is the binary of the default CLI, kept when Binary is
	// the binary of another backend.
	DefaultCLIBinary string `json:"-"`
	// ScratchDir is the working directory of chat-only agents without cwd.
	ScratchDir string `json:"-"`
}
