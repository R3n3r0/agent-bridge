package login

import (
	"context"
	"log/slog"
	"os/exec"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/r3n3r0/agent-bridge/internal/config"
	"github.com/r3n3r0/agent-bridge/internal/runner"
)

// Limits of a login flow.
const (
	flowTimeout   = 20 * time.Minute
	statusTimeout = 30 * time.Second
	maxInput      = 4096
)

// ProcessManager runs each login flow as a child process of the CLI.
type ProcessManager struct {
	clis    []CLI
	baseEnv []string
	log     *slog.Logger
	// wrap, if set, returns the command running args in a pseudo-terminal.
	wrap func(args []string) []string

	mu    sync.Mutex
	flows map[string]*flow
}

var _ Manager = (*ProcessManager)(nil)

// Options configure a ProcessManager.
type Options struct {
	CLIs []CLI
	// BaseEnv is the environment of the CLI processes; AGENT_BRIDGE_*
	// variables and upstream API credentials are removed, so the status
	// reflects the stored login.
	BaseEnv []string
	Logger  *slog.Logger
	// NoTerminal disables the pseudo-terminal even for CLIs that need one.
	NoTerminal bool
}

// NewProcessManager returns a manager for the CLIs of opts.
func NewProcessManager(opts Options) *ProcessManager {
	m := &ProcessManager{clis: opts.CLIs, baseEnv: opts.BaseEnv, log: opts.Logger, flows: map[string]*flow{}}
	if m.log == nil {
		m.log = slog.Default()
	}
	if !opts.NoTerminal {
		m.wrap = scriptWrapper()
	}
	return m
}

// scriptWrapper uses script(1) from util-linux to provide a terminal; nil
// where it is not available.
func scriptWrapper() func([]string) []string {
	if runtime.GOOS != "linux" {
		return nil
	}
	path, err := exec.LookPath("script")
	if err != nil {
		return nil
	}
	return func(args []string) []string {
		quoted := make([]string, len(args))
		for i, a := range args {
			quoted[i] = "'" + strings.ReplaceAll(a, "'", `'\''`) + "'"
		}
		return []string{path, "-qfec", strings.Join(quoted, " "), "/dev/null"}
	}
}

func (m *ProcessManager) find(name string) (CLI, bool) {
	for _, c := range m.clis {
		if c.Name == name {
			return c, true
		}
	}
	return CLI{}, false
}

func (m *ProcessManager) env(c CLI) []string {
	env := runner.FilterEnv(m.baseEnv, config.AuthSubscription, config.CredentialEnv(c.Name))
	return append(env, "TERM=dumb", "NO_COLOR=1")
}

// Statuses implements Manager.
func (m *ProcessManager) Statuses(ctx context.Context) []Status {
	out := make([]Status, len(m.clis))
	var wg sync.WaitGroup
	for i, c := range m.clis {
		wg.Add(1)
		go func(i int, c CLI) {
			defer wg.Done()
			out[i] = m.status(ctx, c)
		}(i, c)
	}
	wg.Wait()
	return out
}

func (m *ProcessManager) status(ctx context.Context, c CLI) Status {
	st := Status{CLI: c.Name, Binary: c.Binary}
	m.mu.Lock()
	if f := m.flows[c.Name]; f != nil {
		v := f.view()
		st.Flow = &v
	}
	m.mu.Unlock()
	bin, err := exec.LookPath(c.Binary)
	if err != nil {
		st.Detail = "not installed"
		return st
	}
	st.Installed = true
	ctx, cancel := context.WithTimeout(ctx, statusTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, c.StatusArgs...)
	cmd.Env = m.env(c)
	b, err := cmd.CombinedOutput()
	st.LoggedIn, st.Detail = c.parseStatus(string(b), err)
	return st
}

// Start implements Manager.
func (m *ProcessManager) Start(name string) (FlowView, error) {
	c, ok := m.find(name)
	if !ok {
		return FlowView{}, ErrUnknownCLI
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if f := m.flows[name]; f != nil && f.running() {
		return f.view(), nil
	}
	bin, err := exec.LookPath(c.Binary)
	if err != nil {
		return FlowView{}, err
	}
	args := append([]string{bin}, c.LoginArgs...)
	if c.NeedsTerminal && m.wrap != nil {
		args = m.wrap(args)
	}
	f, err := startFlow(args, m.env(c), c.AcceptsInput, flowTimeout, func(state string) {
		m.log.Info("login flow finished", "cli", name, "state", state)
	})
	if err != nil {
		return FlowView{}, err
	}
	m.flows[name] = f
	m.log.Info("login flow started", "cli", name)
	return f.view(), nil
}

// Input implements Manager.
func (m *ProcessManager) Input(name, text string) error {
	if len(text) > maxInput || strings.ContainsAny(text, "\r\n") {
		return ErrBadInput
	}
	m.mu.Lock()
	f := m.flows[name]
	m.mu.Unlock()
	if f == nil || !f.running() {
		return ErrNoFlow
	}
	return f.input(text)
}

// Cancel implements Manager.
func (m *ProcessManager) Cancel(name string) error {
	m.mu.Lock()
	f := m.flows[name]
	m.mu.Unlock()
	if f == nil || !f.running() {
		return ErrNoFlow
	}
	f.cancel()
	return nil
}
