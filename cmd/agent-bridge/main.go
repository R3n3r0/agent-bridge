// Command agent-bridge serves an OpenAI-compatible chat completions API and
// fulfils each request by running the official coding-agent CLI headless.
package main

import (
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"

	"github.com/r3n3r0/agent-bridge/internal/config"
	"github.com/r3n3r0/agent-bridge/internal/redact"
	"github.com/r3n3r0/agent-bridge/internal/version"
)

const usage = `agent-bridge: OpenAI-compatible API in front of the coding-agent CLI

Usage:
  agent-bridge [serve] [flags]          start the server (default)
  agent-bridge check-config -config FILE validate a configuration file
  agent-bridge doctor [-live] [flags]   check the local setup
  agent-bridge version                  print version information

Run "agent-bridge <command> -h" for the flags of a command.
`

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	cmd := "serve"
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		cmd, args = args[0], args[1:]
	}
	switch cmd {
	case "serve":
		return serve(args, stderr)
	case "check-config":
		return checkConfig(args, stdout, stderr)
	case "doctor":
		return doctor(args, stdout, stderr)
	case "mcp-tools":
		return mcpTools(args, os.Stdin, stdout, stderr)
	case "version":
		fmt.Fprintln(stdout, version.String())
		return 0
	case "help":
		fmt.Fprint(stdout, usage)
		return 0
	default:
		fmt.Fprintf(stderr, "unknown command %q\n\n%s", cmd, usage)
		return 2
	}
}

// parseFlags parses the shared flags plus extra ones registered by setup.
func parseFlags(name string, args []string, stderr io.Writer, setup func(*flag.FlagSet)) (*config.Flags, error) {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(stderr)
	fl := config.RegisterFlags(fs)
	if setup != nil {
		setup(fs)
	}
	if err := fs.Parse(args); err != nil {
		return nil, err
	}
	if fs.NArg() > 0 {
		return nil, fmt.Errorf("unexpected arguments: %s", strings.Join(fs.Args(), " "))
	}
	fl.MarkSet(fs)
	return fl, nil
}

func newLogger(w io.Writer, format, level string) *slog.Logger {
	var lvl slog.Level
	_ = lvl.UnmarshalText([]byte(level))
	opts := &slog.HandlerOptions{Level: lvl}
	var h slog.Handler
	if format == "json" {
		h = slog.NewJSONHandler(w, opts)
	} else {
		h = slog.NewTextHandler(w, opts)
	}
	return slog.New(redact.NewHandler(h))
}

func checkConfig(args []string, stdout, stderr io.Writer) int {
	fl, err := parseFlags("check-config", args, stderr, nil)
	if err != nil {
		return 2
	}
	cfg, err := config.Load(fl, os.Getenv)
	if err != nil {
		fmt.Fprintf(stderr, "configuration is not valid:\n%v\n", redact.String(err.Error()))
		return 1
	}
	if cfg.Path != "" {
		fmt.Fprintf(stdout, "configuration %s is valid\n\n", cfg.Path)
	} else {
		fmt.Fprintf(stdout, "no configuration file, defaults are valid\n\n")
	}
	fmt.Fprintf(stdout, "backend: %s (binary %s)\n\n", cfg.Backend, cfg.Binary)
	for _, name := range cfg.AgentNames() {
		a := cfg.Agents[name]
		kind := "agent"
		if a.Builtin {
			kind = "built-in"
		}
		cwd := a.Cwd
		if cwd == "" {
			cwd = "(temporary directory)"
		}
		model := a.Model
		if model == "" {
			model = "(configured in the CLI)"
		}
		fmt.Fprintf(stdout, "%s (%s)\n  model: %s\n  tools: %s\n", name, kind, model, toolsSummary(a))
		if cfg.Backend == config.BackendCodex {
			fmt.Fprintf(stdout, "  sandbox: %s\n", sandboxSummary(a))
		} else {
			fmt.Fprintf(stdout, "  permission_mode: %s\n  max_turns: %d\n", a.PermissionMode, a.MaxTurns)
		}
		fmt.Fprintf(stdout, "  cwd: %s\n  timeout: %s\n", cwd, a.Timeout)
		if a.SystemPrompt != "" {
			fmt.Fprintf(stdout, "  system_prompt: %s (%d chars)\n", a.SystemPromptMode, len(a.SystemPrompt))
		}
		if len(a.AddDirs) > 0 {
			fmt.Fprintf(stdout, "  add_dirs: %s\n", strings.Join(a.AddDirs, ", "))
		}
		if a.MCPConfig != "" {
			fmt.Fprintf(stdout, "  mcp_config: %s\n", a.MCPConfig)
		}
	}
	return 0
}

func sandboxSummary(a *config.Agent) string {
	if !a.ToolsEnabled {
		return config.CodexSandboxRO
	}
	return a.Sandbox
}

func toolsSummary(a *config.Agent) string {
	if !a.ToolsEnabled {
		return "disabled (chat only)"
	}
	s := "enabled"
	if len(a.AllowedTools) > 0 {
		s += ", allowed: " + strings.Join(a.AllowedTools, ", ")
	}
	if len(a.DisallowedTools) > 0 {
		s += ", disallowed: " + strings.Join(a.DisallowedTools, ", ")
	}
	return s
}
