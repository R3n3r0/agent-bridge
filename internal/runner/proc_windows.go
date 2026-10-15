//go:build windows

package runner

import (
	"os/exec"
	"strconv"
)

// treeController terminates the whole process tree with taskkill.
type treeController struct{}

func newProcessController() processController { return treeController{} }

func (treeController) prepare(cmd *exec.Cmd) {
	cmd.Cancel = func() error {
		kill := exec.Command("taskkill", "/T", "/F", "/PID", strconv.Itoa(cmd.Process.Pid))
		if err := kill.Run(); err != nil {
			return cmd.Process.Kill()
		}
		return nil
	}
}

func (treeController) cleanup(*exec.Cmd) {}

func (treeController) stop() {}
