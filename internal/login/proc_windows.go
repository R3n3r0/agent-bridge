//go:build windows

package login

import (
	"os/exec"
	"strconv"
)

// killTree kills the process and its children with taskkill when the
// command's context ends.
func killTree(cmd *exec.Cmd) {
	cmd.Cancel = func() error {
		return exec.Command("taskkill", "/T", "/F", "/PID", strconv.Itoa(cmd.Process.Pid)).Run()
	}
}
