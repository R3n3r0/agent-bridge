//go:build !windows

package server

import (
	"errors"
	"os"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

func processGone(pid int) bool {
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if err := syscall.Kill(pid, 0); errors.Is(err, syscall.ESRCH) {
			return true
		}
		if stat, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat"); err == nil {
			if f := strings.Fields(string(stat)); len(f) > 2 && f[2] == "Z" {
				return true
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	return false
}

func TestTimeoutKillsProcessTree(t *testing.T) {
	pidFile := t.TempDir() + "/child.pid"
	h := newHarness(t, harnessOpts{
		env:   []string{"FAKE_SCENARIO=spawn_child", "FAKE_SLEEP_MS=60000", "FAKE_CHILD_PID=" + pidFile},
		extra: func(string) string { return `"quick": {"tools_enabled": false, "timeout_seconds": 1}` },
	})
	expectError(t, h.post(userMsg("quick", "x"), nil), 504, errTimeout)
	data, err := os.ReadFile(pidFile)
	if err != nil {
		t.Fatal(err)
	}
	child, _ := strconv.Atoi(strings.TrimSpace(string(data)))
	if !processGone(h.starts()[0].PID) {
		t.Fatal("CLI process still running")
	}
	if !processGone(child) {
		t.Fatalf("child process %d still running", child)
	}
}
