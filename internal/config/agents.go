package config

import (
	"fmt"
	"os"
	"regexp"
	"sort"
	"strings"
	"time"
)

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
		Sandbox:          f.Sandbox,
		MaxTurns:         c.Defaults.MaxTurns,
		MCPConfig:        c.abs(f.MCPConfig),
		Timeout:          time.Duration(c.Defaults.TimeoutSeconds) * time.Second,
	}
	if a.Model == "" {
		a.Model = c.defaultAgentModel()
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
	c.validateCodexAgent(a, f, add)
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
	for _, a := range c.BuiltinNames() {
		if name == c.BuiltinPrefix+a {
			return true
		}
	}
	return false
}

// builtinAgent returns a chat-only agent running model.
func (c *Config) builtinAgent(name, model string) *Agent {
	description := "Built-in chat-only model (" + model + ")"
	if c.Backend == BackendCodex {
		if model == CodexDefaultAlias {
			model, description = "", "Built-in chat-only model (the model configured in the CLI)"
		}
	}
	return &Agent{
		Name:             name,
		Model:            model,
		Description:      description,
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
