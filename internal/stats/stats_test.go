package stats

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type gauges struct{ a, q int }

func (g gauges) Active() int { return g.a }
func (g gauges) Queued() int { return g.q }

func TestRecordSnapshotReset(t *testing.T) {
	s := NewCollector("api", gauges{1, 2}, nil)
	_ = s.Record(Turn{Agent: "a", Outcome: OutcomeSuccess, DurationMs: 100, CostUSD: 0.5, InputTokens: 1, OutputTokens: 2, CacheReadTokens: 3, CacheCreationTokens: 4})
	_ = s.Record(Turn{Agent: "a", Outcome: OutcomeError, DurationMs: 300})
	_ = s.Record(Turn{Agent: "b", Outcome: OutcomeTimeout})
	snap := s.Snapshot(true)
	if snap.AuthMode != "api" || snap.Active != 1 || snap.Queued != 2 {
		t.Fatalf("snapshot %+v", snap)
	}
	a := snap.Agents["a"]
	if a.Requests != 2 || a.Errors != 1 || a.Timeouts != 0 || a.CostUSD != 0.5 || a.AvgDurationMs != 200 ||
		a.InputTokens != 1 || a.OutputTokens != 2 || a.CacheReadTokens != 3 || a.CacheCreationTokens != 4 {
		t.Fatalf("agent a %+v", a)
	}
	if snap.Agents["b"].Timeouts != 1 {
		t.Fatal("timeouts")
	}
	if len(s.Snapshot(false).Agents) != 0 {
		t.Fatal("reset failed")
	}
	var b strings.Builder
	s.WriteMetrics(&b)
	out := b.String()
	for _, want := range []string{
		`agent_bridge_requests_total{agent="a",outcome="error"} 1`,
		`agent_bridge_requests_total{agent="a",outcome="success"} 1`,
		`agent_bridge_requests_total{agent="b",outcome="timeout"} 1`,
		`agent_bridge_cost_usd_total{agent="a"} 0.5`,
		`agent_bridge_tokens_total{agent="a",kind="cache_creation"} 4`,
		"agent_bridge_turn_duration_seconds_sum 0.4",
		"agent_bridge_turn_duration_seconds_count 3",
		"agent_bridge_active 1", "agent_bridge_queued 2",
		"# TYPE agent_bridge_active gauge",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %q in\n%s", want, out)
		}
	}
}

func TestUsageFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "usage.jsonl")
	sink, err := OpenJSONLUsageLog(path)
	if err != nil {
		t.Fatal(err)
	}
	s := NewCollector("subscription", nil, sink)
	if err := s.Record(Turn{Agent: "a", Outcome: OutcomeSuccess, RequestID: "r1"}); err != nil {
		t.Fatal(err)
	}
	if err := sink.Close(); err != nil {
		t.Fatal(err)
	}
	if err := sink.Close(); err != nil {
		t.Fatal(err)
	}
	if err := s.Record(Turn{Agent: "a"}); err != nil {
		t.Fatal("record after close must not fail")
	}
	data, _ := os.ReadFile(path)
	line := strings.TrimSpace(string(data))
	if !strings.Contains(line, `"auth_mode":"subscription"`) || !strings.Contains(line, `"request_id":"r1"`) ||
		!strings.Contains(line, `"ts":"`) || strings.Count(string(data), "\n") != 1 {
		t.Fatalf("usage line %s", data)
	}
	if _, err := OpenJSONLUsageLog(filepath.Join(t.TempDir(), "missing", "u.jsonl")); err == nil {
		t.Fatal("unwritable path accepted")
	}
	if snap := s.Snapshot(false); snap.Active != 0 {
		t.Fatal("nil gauges")
	}
}

type failingSink struct{ DiscardUsage }

func (failingSink) Write(Turn) error { return os.ErrClosed }

func TestSinkErrorKeepsCounters(t *testing.T) {
	s := NewCollector("api", nil, failingSink{})
	if err := s.Record(Turn{Agent: "a", Outcome: OutcomeSuccess}); err == nil {
		t.Fatal("sink error not returned")
	}
	if s.Snapshot(false).Agents["a"].Requests != 1 {
		t.Fatal("counters not updated")
	}
}
