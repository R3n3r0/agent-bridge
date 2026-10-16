//go:build !windows

package runner

import (
	"os/exec"
	"sync"
	"syscall"
	"time"
)

// groupController runs the CLI in its own process group and signals the
// whole group: SIGTERM first, SIGKILL after killGrace.
type groupController struct {
	mu    sync.Mutex
	timer *time.Timer
}

func newProcessController() processController { return &groupController{} }

func (g *groupController) prepare(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		pid := cmd.Process.Pid
		_ = syscall.Kill(-pid, syscall.SIGTERM)
		g.mu.Lock()
		g.timer = time.AfterFunc(killGrace, func() { _ = syscall.Kill(-pid, syscall.SIGKILL) })
		g.mu.Unlock()
		return nil
	}
}

func (g *groupController) cleanup(cmd *exec.Cmd) {
	g.stop()
	if cmd.Process != nil {
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
}

func (g *groupController) stop() {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.timer != nil {
		g.timer.Stop()
	}
}
