package stats

import (
	"fmt"
	"io"
	"sort"
	"strings"
	"sync"
	"time"
)

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
