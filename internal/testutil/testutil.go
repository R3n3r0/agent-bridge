// Package testutil builds the fake CLI used by tests.
package testutil

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

// BuildFakeCLI compiles testdata/fakecli into dir and returns the binary
// path.
func BuildFakeCLI(dir string) (string, error) {
	out, err := exec.Command("go", "env", "GOMOD").Output()
	if err != nil {
		return "", fmt.Errorf("go env GOMOD: %w", err)
	}
	root := filepath.Dir(strings.TrimSpace(string(out)))
	bin := filepath.Join(dir, "fakecli")
	if runtime.GOOS == "windows" {
		bin += ".exe"
	}
	cmd := exec.Command("go", "build", "-o", bin, "./testdata/fakecli")
	cmd.Dir = root
	cmd.Env = append(os.Environ(), "CGO_ENABLED=0")
	if out, err := cmd.CombinedOutput(); err != nil {
		return "", fmt.Errorf("build fake CLI: %v\n%s", err, out)
	}
	return bin, nil
}

// BuildBridge compiles the agent-bridge command into dir and returns the
// binary path; tests use it as the MCP helper for client functions.
func BuildBridge(dir string) (string, error) {
	out, err := exec.Command("go", "env", "GOMOD").Output()
	if err != nil {
		return "", fmt.Errorf("go env GOMOD: %w", err)
	}
	root := filepath.Dir(strings.TrimSpace(string(out)))
	bin := filepath.Join(dir, "agent-bridge")
	if runtime.GOOS == "windows" {
		bin += ".exe"
	}
	cmd := exec.Command("go", "build", "-o", bin, "./cmd/agent-bridge")
	cmd.Dir = root
	cmd.Env = append(os.Environ(), "CGO_ENABLED=0")
	if out, err := cmd.CombinedOutput(); err != nil {
		return "", fmt.Errorf("build agent-bridge: %v\n%s", err, out)
	}
	return bin, nil
}
