//go:build !windows

package runner

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestRunKillsProcessTree(t *testing.T) {
	pidFile := filepath.Join(t.TempDir(), "child.pid")
	req := fakeRequest(t, "spawn_child", "FAKE_SLEEP_MS=60000", "FAKE_CHILD_PID="+pidFile)
	req.Timeout = 500 * time.Millisecond
	if _, err := Run(context.Background(), req); !errors.Is(err, ErrTimeout) {
		t.Fatalf("got %v", err)
	}
	data, err := os.ReadFile(pidFile)
	if err != nil {
		t.Fatal(err)
	}
	pid, _ := strconv.Atoi(strings.TrimSpace(string(data)))
	if !waitDead(pid, 10*time.Second) {
		t.Fatalf("child process %d still alive", pid)
	}
}

// waitDead reports whether pid disappears (or becomes a zombie) in time.
func waitDead(pid int, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
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
