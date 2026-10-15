//go:build !windows

package runner

import (
	"os/exec"
	"sync"
	"syscall"
	"time"
)

// procControl terminates the whole process group, so commands started by
// the CLI's shell tool die with it.
type procControl struct {
	mu    sync.Mutex
	timer *time.Timer
}

func setupProcess(cmd *exec.Cmd) *procControl {
	pc := &procControl{}
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		pid := cmd.Process.Pid
		_ = syscall.Kill(-pid, syscall.SIGTERM)
		pc.mu.Lock()
		pc.timer = time.AfterFunc(killGrace, func() { _ = syscall.Kill(-pid, syscall.SIGKILL) })
		pc.mu.Unlock()
		return nil
	}
	return pc
}

// cleanup kills whatever is left in the group after a cancellation.
func (pc *procControl) cleanup(cmd *exec.Cmd) {
	pc.stop()
	if cmd.Process != nil {
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
}

func (pc *procControl) stop() {
	pc.mu.Lock()
	defer pc.mu.Unlock()
	if pc.timer != nil {
		pc.timer.Stop()
	}
}
