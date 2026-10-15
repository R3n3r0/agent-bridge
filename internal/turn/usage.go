package turn

import (
	"github.com/r3n3r0/agent-bridge/internal/runner"
	"github.com/r3n3r0/agent-bridge/internal/session"
)

// turnUsage turns the session totals reported by CLIs with cumulative
// usage into the usage of this turn, and remembers the new totals in entry.
func (lt *liveTurn) turnUsage(out *runner.Outcome, entry *session.Entry, resumed bool) {
	if !lt.svc.Backend.CumulativeUsage() || out.Result == nil {
		return
	}
	u := &out.Result.Usage
	totals := session.UsageTotals{
		Input:      u.InputTokens,
		Output:     u.OutputTokens,
		CacheRead:  u.CacheReadTokens,
		CacheWrite: u.CacheCreationTokens,
	}
	if prev := entry.CLIUsage; resumed && prev != nil {
		u.InputTokens = nonNegative(totals.Input - prev.Input)
		u.OutputTokens = nonNegative(totals.Output - prev.Output)
		u.CacheReadTokens = nonNegative(totals.CacheRead - prev.CacheRead)
		u.CacheCreationTokens = nonNegative(totals.CacheWrite - prev.CacheWrite)
	}
	entry.CLIUsage = &totals
}

func nonNegative(n int64) int64 {
	if n < 0 {
		return 0
	}
	return n
}
