package config

import (
	"time"
)

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
	Sandbox          string   `json:"sandbox"`
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
	Sandbox          string
	MaxTurns         int
	Cwd              string
	AddDirs          []string
	MCPConfig        string
	Timeout          time.Duration
	Builtin          bool
}
