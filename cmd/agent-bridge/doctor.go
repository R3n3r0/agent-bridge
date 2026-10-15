package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/r3n3r0/agent-bridge/internal/config"
	"github.com/r3n3r0/agent-bridge/internal/redact"
	"github.com/r3n3r0/agent-bridge/internal/runner"
)

var versionRe = regexp.MustCompile(`(\d+)\.(\d+)\.(\d+)`)

type doctorReport struct {
	w      io.Writer
	failed bool
}

func (d *doctorReport) ok(format string, a ...any) {
	fmt.Fprintf(d.w, "[ok]   %s\n", fmt.Sprintf(format, a...))
}

func (d *doctorReport) warn(format string, a ...any) {
	fmt.Fprintf(d.w, "[warn] %s\n", fmt.Sprintf(format, a...))
}

func (d *doctorReport) fail(format string, a ...any) {
	d.failed = true
	fmt.Fprintf(d.w, "[fail] %s\n", redact.String(fmt.Sprintf(format, a...)))
}

func doctor(args []string, stdout, stderr io.Writer) int {
	var live bool
	fl, err := parseFlags("doctor", args, stderr, func(fs *flag.FlagSet) {
		fs.BoolVar(&live, "live", false, "run a one-turn test request (haiku model, or the codex default model)")
	})
	if err != nil {
		return 2
	}
	d := &doctorReport{w: stdout}

	cfg, err := config.Load(fl, os.Getenv)
	if err != nil {
		d.fail("configuration: %v", err)
		return 1
	}
	d.ok("configuration is valid (%d models)", len(cfg.Agents))
	backend, err := runner.NewBackend(cfg.Backend)
	if err != nil {
		d.fail("backend: %v", err)
		return 1
	}
	d.ok("backend: %s", backend.Name())

	bin, err := exec.LookPath(cfg.Binary)
	if err != nil {
		d.fail("CLI binary %q not found in PATH: install it and log in once interactively", cfg.Binary)
	} else {
		d.ok("CLI binary: %s", bin)
		checkVersion(d, bin, backend.MinVersion())
		if cfg.Backend == config.BackendCodex && cfg.AuthMode == config.AuthSubscription {
			checkCodexLogin(d, bin)
		}
	}

	if err := cfg.ValidateAuth(os.Getenv); err != nil {
		d.fail("authentication mode: %v", err)
	} else {
		d.ok("authentication mode: %s", cfg.AuthMode)
	}
	if err := cfg.ValidateNetwork(); err != nil {
		d.fail("network: %v", err)
	} else {
		d.ok("listen address: %s", cfg.Listen)
	}

	for _, name := range cfg.AgentNames() {
		a := cfg.Agents[name]
		if a.Cwd == "" {
			continue
		}
		if err := checkDirWritable(a.Cwd); err != nil {
			d.warn("agent %s: cwd %s: %v", name, a.Cwd, err)
		} else {
			d.ok("agent %s: cwd %s is accessible", name, a.Cwd)
		}
	}
	if cfg.Sessions.Store == config.SessionStoreFile {
		if err := checkDirWritable(filepath.Dir(cfg.Sessions.Path)); err != nil {
			d.fail("sessions.path %s: %v", cfg.Sessions.Path, err)
		} else {
			d.ok("sessions file directory is writable")
		}
	}
	if cfg.UsageLog != "" {
		if err := checkDirWritable(filepath.Dir(cfg.UsageLog)); err != nil {
			d.fail("usage_log %s: %v", cfg.UsageLog, err)
		} else {
			d.ok("usage log directory is writable")
		}
	}

	if live && bin != "" && !d.failed {
		liveCheck(d, cfg, backend)
	} else if live {
		d.warn("live check skipped because of earlier failures")
	}
	if d.failed {
		return 1
	}
	return 0
}

func checkVersion(d *doctorReport, bin, minVersion string) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, bin, "--version").Output()
	if err != nil {
		d.fail("CLI --version failed: %v", err)
		return
	}
	got := versionRe.FindString(string(out))
	if got == "" {
		d.warn("cannot parse CLI version from %q", strings.TrimSpace(string(out)))
		return
	}
	if compareVersions(got, minVersion) < 0 {
		d.fail("CLI version %s is older than the minimum supported %s: update it", got, minVersion)
		return
	}
	d.ok("CLI version %s (minimum %s)", got, minVersion)
}

// checkCodexLogin verifies that the codex CLI has a stored login.
func checkCodexLogin(d *doctorReport, bin string) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, "login", "status")
	var stderr strings.Builder
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	status := strings.TrimSpace(string(out))
	if status == "" {
		lines := strings.Split(strings.TrimSpace(stderr.String()), "\n")
		status = lines[len(lines)-1]
	}
	if err != nil {
		d.fail("codex login status: %s: run \"codex login\" once interactively", redact.Truncate(status, 200))
		return
	}
	d.ok("codex login: %s", redact.Truncate(status, 200))
}

// compareVersions compares two x.y.z strings.
func compareVersions(a, b string) int {
	pa, pb := versionRe.FindStringSubmatch(a), versionRe.FindStringSubmatch(b)
	if pa == nil || pb == nil {
		return strings.Compare(a, b)
	}
	for i := 1; i <= 3; i++ {
		x, _ := strconv.Atoi(pa[i])
		y, _ := strconv.Atoi(pb[i])
		if x != y {
			if x < y {
				return -1
			}
			return 1
		}
	}
	return 0
}

func checkDirWritable(dir string) error {
	st, err := os.Stat(dir)
	if err != nil {
		return err
	}
	if !st.IsDir() {
		return fmt.Errorf("not a directory")
	}
	f, err := os.CreateTemp(dir, ".agent-bridge-doctor-*")
	if err != nil {
		return fmt.Errorf("not writable: %w", err)
	}
	name := f.Name()
	f.Close()
	return os.Remove(name)
}

func liveCheck(d *doctorReport, cfg *config.Config, backend runner.Backend) {
	dir, err := os.MkdirTemp("", "agent-bridge-doctor-")
	if err != nil {
		d.fail("live check: %v", err)
		return
	}
	defer os.RemoveAll(dir)
	spec := runner.Spec{Model: "haiku", ToolsEnabled: false, MaxTurns: 1}
	if cfg.Backend == config.BackendCodex {
		spec = runner.Spec{ToolsEnabled: false}
	}
	inv, err := backend.Prepare(runner.Turn{
		Spec:     spec,
		Prompt:   "Reply with the single word OK.",
		BaseEnv:  os.Environ(),
		AuthMode: cfg.AuthMode,
	})
	if err != nil {
		d.fail("live check: %v", err)
		return
	}
	defer inv.Cleanup()
	out, err := runner.CLIExecutor{}.Run(context.Background(), runner.Request{
		Binary:  cfg.Binary,
		Args:    inv.Args,
		Env:     inv.Env,
		Dir:     dir,
		Stdin:   inv.Stdin,
		Timeout: 2 * time.Minute,
		Parser:  backend.NewParser(),
	})
	if err != nil {
		d.fail("live check: %v: %s", err, redact.Tail(strings.TrimSpace(out.Stderr), 2000))
		return
	}
	res := out.Result
	if res.IsError || res.Subtype != runner.SubtypeSuccess {
		d.fail("live check: CLI returned %q: %s", res.Subtype, redact.Truncate(res.Result, 300))
		return
	}
	if cfg.AuthMode == config.AuthSubscription && out.APIKeySource != "" && out.APIKeySource != "none" {
		d.fail("live check: subscription mode but the CLI used an API key (source %q)", out.APIKeySource)
		return
	}
	model, ms := out.Model, res.DurationMs
	if model == "" {
		model = "(configured in the CLI)"
	}
	if ms == 0 {
		ms = out.Duration.Milliseconds()
	}
	d.ok("live check: model %s answered %q in %d ms (estimated cost $%.6f)",
		model, redact.Truncate(strings.TrimSpace(res.Result), 40), ms, res.CostUSD)
}
