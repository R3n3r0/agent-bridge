//go:build windows

package runner

import (
	"os/exec"
	"strconv"
)

// procControl terminates the whole process tree with taskkill, so commands
// started by the CLI's shell tool die with it.
type procControl struct{}

func setupProcess(cmd *exec.Cmd) *procControl {
	cmd.Cancel = func() error {
		kill := exec.Command("taskkill", "/T", "/F", "/PID", strconv.Itoa(cmd.Process.Pid))
		if err := kill.Run(); err != nil {
			return cmd.Process.Kill()
		}
		return nil
	}
	return &procControl{}
}

func (pc *procControl) cleanup(cmd *exec.Cmd) {}

func (pc *procControl) stop() {}
