//go:build !windows

package login

import (
	"os/exec"
	"syscall"
)

// killTree makes cmd the leader of a new process group and kills the whole
// group when its context ends, so wrappers do not leave children behind.
func killTree(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
}
