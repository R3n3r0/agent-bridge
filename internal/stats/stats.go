// Package stats keeps per-agent counters, writes the optional usage log and
// renders Prometheus metrics.
//
// Recorder is what turn execution needs, Reporter what the HTTP endpoints
// need; Collector implements both. UsageSink abstracts where per-turn usage
// records go (JSONLUsageLog or DiscardUsage).
package stats

import (
	"fmt"
	"io"
	"sort"
	"strings"
	"sync"
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

// Collector keeps statistics in memory. It is safe for concurrent use.
type Collector struct {
	authMode  string
	startedAt time.Time
	gauges    Gauges
	sink      UsageSink
	now       func() time.Time

	mu       sync.Mutex
	agents   map[string]*AgentStats
	outcomes map[[2]string]int64 // never reset: Prometheus counters
	cost     map[string]float64
	tokens   map[[2]string]int64
	durSum   float64
	durCount int64
}

var (
	_ Recorder = (*Collector)(nil)
	_ Reporter = (*Collector)(nil)
)

// NewCollector creates a collector. gauges may be nil; a nil sink discards
// usage records.
func NewCollector(authMode string, gauges Gauges, sink UsageSink) *Collector {
	if sink == nil {
		sink = DiscardUsage{}
	}
	return &Collector{
		authMode:  authMode,
		startedAt: time.Now(),
		gauges:    gauges,
		sink:      sink,
		now:       time.Now,
		agents:    map[string]*AgentStats{},
		outcomes:  map[[2]string]int64{},
		cost:      map[string]float64{},
		tokens:    map[[2]string]int64{},
	}
}

// Record implements Recorder.
func (s *Collector) Record(t Turn) error {
	s.mu.Lock()
	a := s.agents[t.Agent]
	if a == nil {
		a = &AgentStats{}
		s.agents[t.Agent] = a
	}
	a.Requests++
	switch t.Outcome {
	case OutcomeError:
		a.Errors++
	case OutcomeTimeout:
		a.Timeouts++
	}
	a.CostUSD += t.CostUSD
	a.InputTokens += t.InputTokens
	a.OutputTokens += t.OutputTokens
	a.CacheReadTokens += t.CacheReadTokens
	a.CacheCreationTokens += t.CacheCreationTokens
	a.totalDurationMs += t.DurationMs

	s.outcomes[[2]string{t.Agent, t.Outcome}]++
	s.cost[t.Agent] += t.CostUSD
	s.tokens[[2]string{t.Agent, "input"}] += t.InputTokens
	s.tokens[[2]string{t.Agent, "output"}] += t.OutputTokens
	s.tokens[[2]string{t.Agent, "cache_read"}] += t.CacheReadTokens
	s.tokens[[2]string{t.Agent, "cache_creation"}] += t.CacheCreationTokens
	s.durSum += float64(t.DurationMs) / 1000
	s.durCount++
	s.mu.Unlock()

	t.TS = s.now().UTC().Format(time.RFC3339)
	t.AuthMode = s.authMode
	return s.sink.Write(t)
}

// Snapshot implements Reporter. Prometheus counters are never reset.
func (s *Collector) Snapshot(reset bool) Snapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now()
	snap := Snapshot{
		AuthMode:      s.authMode,
		StartedAt:     s.startedAt.UTC(),
		UptimeSeconds: int64(now.Sub(s.startedAt).Seconds()),
		Agents:        map[string]*AgentStats{},
	}
	if s.gauges != nil {
		snap.Active = s.gauges.Active()
		snap.Queued = s.gauges.Queued()
	}
	for name, a := range s.agents {
		c := *a
		if c.Requests > 0 {
			c.AvgDurationMs = float64(c.totalDurationMs) / float64(c.Requests)
		}
		snap.Agents[name] = &c
	}
	if reset {
		s.agents = map[string]*AgentStats{}
	}
	return snap
}

// WriteMetrics implements Reporter.
func (s *Collector) WriteMetrics(w io.Writer) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var b strings.Builder

	b.WriteString("# HELP agent_bridge_requests_total Completed turns by agent and outcome.\n")
	b.WriteString("# TYPE agent_bridge_requests_total counter\n")
	okeys := make([][2]string, 0, len(s.outcomes))
	for k := range s.outcomes {
		okeys = append(okeys, k)
	}
	sortPairs(okeys)
	for _, k := range okeys {
		fmt.Fprintf(&b, "agent_bridge_requests_total{agent=%q,outcome=%q} %d\n", label(k[0]), label(k[1]), s.outcomes[k])
	}

	b.WriteString("# HELP agent_bridge_cost_usd_total Estimated cost in USD by agent.\n")
	b.WriteString("# TYPE agent_bridge_cost_usd_total counter\n")
	ckeys := make([]string, 0, len(s.cost))
	for k := range s.cost {
		ckeys = append(ckeys, k)
	}
	sort.Strings(ckeys)
	for _, k := range ckeys {
		fmt.Fprintf(&b, "agent_bridge_cost_usd_total{agent=%q} %g\n", label(k), s.cost[k])
	}

	b.WriteString("# HELP agent_bridge_tokens_total Tokens by agent and kind.\n")
	b.WriteString("# TYPE agent_bridge_tokens_total counter\n")
	tkeys := make([][2]string, 0, len(s.tokens))
	for k := range s.tokens {
		tkeys = append(tkeys, k)
	}
	sortPairs(tkeys)
	for _, k := range tkeys {
		fmt.Fprintf(&b, "agent_bridge_tokens_total{agent=%q,kind=%q} %d\n", label(k[0]), label(k[1]), s.tokens[k])
	}

	b.WriteString("# HELP agent_bridge_turn_duration_seconds Turn duration.\n")
	b.WriteString("# TYPE agent_bridge_turn_duration_seconds summary\n")
	fmt.Fprintf(&b, "agent_bridge_turn_duration_seconds_sum %g\n", s.durSum)
	fmt.Fprintf(&b, "agent_bridge_turn_duration_seconds_count %d\n", s.durCount)

	active, queued := 0, 0
	if s.gauges != nil {
		active, queued = s.gauges.Active(), s.gauges.Queued()
	}
	b.WriteString("# HELP agent_bridge_active Turns currently running.\n")
	b.WriteString("# TYPE agent_bridge_active gauge\n")
	fmt.Fprintf(&b, "agent_bridge_active %d\n", active)
	b.WriteString("# HELP agent_bridge_queued Turns waiting for a slot.\n")
	b.WriteString("# TYPE agent_bridge_queued gauge\n")
	fmt.Fprintf(&b, "agent_bridge_queued %d\n", queued)

	_, _ = io.WriteString(w, b.String())
}

func sortPairs(p [][2]string) {
	sort.Slice(p, func(i, j int) bool {
		if p[i][0] != p[j][0] {
			return p[i][0] < p[j][0]
		}
		return p[i][1] < p[j][1]
	})
}

// label keeps label values printable; %q handles quoting and escaping.
func label(s string) string { return strings.ToValidUTF8(s, "?") }
