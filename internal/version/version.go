// Package version holds build information injected with -ldflags.
package version

import "fmt"

// Values overridden at build time, e.g.
//
//	-ldflags "-X github.com/r3n3r0/agent-bridge/internal/version.Version=v1.0.0"
var (
	Version = "dev"
	Commit  = "none"
	Date    = "unknown"
)

// String returns a one-line description of the build.
func String() string {
	return fmt.Sprintf("agent-bridge %s (commit %s, built %s)", Version, Commit, Date)
}
