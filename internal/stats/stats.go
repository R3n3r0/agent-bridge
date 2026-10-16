// Package stats keeps per-agent counters, writes the optional usage log and
// renders Prometheus metrics.
//
// Recorder is what turn execution needs, Reporter what the HTTP endpoints
// need; Collector implements both. UsageSink abstracts where per-turn usage
// records go (JSONLUsageLog or DiscardUsage).
package stats

import (
	"io"
	"time"
)

// Outcomes of a turn.
const (
	OutcomeSuccess  = "success"
	OutcomeMaxTurns = "max_turns"
	OutcomeError    = "error"
	OutcomeTimeout  = "timeout"
	OutcomeCanceled = "canceled"
	// OutcomeToolCalls is a process stopped on purpose after returning
	// function calls (stateless mode).
	OutcomeToolCalls = "tool_calls"
)

// Turn is the record of one completed turn. It never contains prompt or
// response text.
type Turn struct {
	TS                  string  `json:"ts,omitempty"`
	AuthMode            string  `json:"auth_mode,omitempty"`
	RequestID           string  `json:"request_id"`
	Agent               string  `json:"agent"`
	Model               string  `json:"model"`
	SessionKey          string  `json:"session_key"`
	CLISessionID        string  `json:"claude_session_id"`
	Resumed             bool    `json:"resumed"`
	Turns               int     `json:"turns"`
	DurationMs          int64   `json:"duration_ms"`
	QueueMs             int64   `json:"queue_ms"`
	CostUSD             float64 `json:"cost_usd"`
	InputTokens         int64   `json:"input_tokens"`
	OutputTokens        int64   `json:"output_tokens"`
	CacheReadTokens     int64   `json:"cache_read_tokens"`
	CacheCreationTokens int64   `json:"cache_creation_tokens"`
	Outcome             string  `json:"outcome"`
}

// AgentStats are cumulative counters for one agent.
type AgentStats struct {
	Requests            int64   `json:"requests"`
	Errors              int64   `json:"errors"`
	Timeouts            int64   `json:"timeouts"`
	CostUSD             float64 `json:"cost_usd"`
	InputTokens         int64   `json:"input_tokens"`
	OutputTokens        int64   `json:"output_tokens"`
	CacheReadTokens     int64   `json:"cache_read_tokens"`
	CacheCreationTokens int64   `json:"cache_creation_tokens"`
	AvgDurationMs       float64 `json:"avg_duration_ms"`
	totalDurationMs     int64
}

// Snapshot is the /stats response body.
type Snapshot struct {
	AuthMode      string                 `json:"auth_mode"`
	StartedAt     time.Time              `json:"started_at"`
	UptimeSeconds int64                  `json:"uptime_seconds"`
	Active        int                    `json:"active"`
	Queued        int                    `json:"queued"`
	Agents        map[string]*AgentStats `json:"agents"`
}

// Gauges reports the current number of running and queued turns.
type Gauges interface {
	Active() int
	Queued() int
}

// Recorder receives the record of every finished turn.
type Recorder interface {
	// Record updates the counters and forwards t to the usage sink. A sink
	// error is returned for logging; the counters are updated regardless.
	Record(t Turn) error
}

// Reporter exposes the collected statistics.
type Reporter interface {
	// Snapshot returns the current counters; with reset they are zeroed
	// after being read.
	Snapshot(reset bool) Snapshot
	// WriteMetrics renders the Prometheus text exposition format.
	WriteMetrics(w io.Writer)
}
