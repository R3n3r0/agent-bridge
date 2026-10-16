package turn

import (
	"context"
	"errors"
	"testing"

	"github.com/r3n3r0/agent-bridge/internal/config"
	"github.com/r3n3r0/agent-bridge/internal/runner"
	"github.com/r3n3r0/agent-bridge/internal/session"
)

// withBackend rebuilds the fixture's service with backend b.
func withBackend(f *fixture, b runner.Backend) {
	s := f.svc.(*service)
	settings := s.Settings
	settings.Backend = b
	f.svc = NewService(s.Deps, settings)
}

func TestCumulativeUsageBecomesTurnUsage(t *testing.T) {
	totals := []runner.Usage{
		{InputTokens: 10, OutputTokens: 5, CacheReadTokens: 3},
		{InputTokens: 25, OutputTokens: 9, CacheReadTokens: 8, CacheCreationTokens: 1},
	}
	f := newFixture(t, config.OnBusyWait, func(i int, req runner.Request) (*runner.Outcome, error) {
		out := success("thread", "ok")
		out.Result.Usage = totals[i-1]
		return out, nil
	})
	withBackend(f, runner.CodexBackend{})
	res, err := f.svc.Execute(context.Background(), request(user("one")), nil)
	if err != nil || res.PromptTokens != 13 || res.CompletionTokens != 5 {
		t.Fatalf("first %+v %v", res, err)
	}
	res, err = f.svc.Execute(context.Background(), request(user("one"), assistant("ok"), user("two")), nil)
	if err != nil || res.PromptTokens != 15+5+1 || res.CompletionTokens != 4 {
		t.Fatalf("second %+v %v", res, err)
	}
	e, _ := f.store.Get(session.InternalKey(agent.Name, "k"))
	if e.CLIUsage == nil || e.CLIUsage.Input != 25 || e.CLIUsage.CacheWrite != 1 {
		t.Fatalf("stored totals %+v", e.CLIUsage)
	}
	if f.exec.calls[0].Parser == nil || f.exec.calls[0].Args[0] != "exec" {
		t.Fatalf("request %+v", f.exec.calls[0])
	}
}

type failingBackend struct{ runner.StreamJSONBackend }

func (failingBackend) Prepare(runner.Turn) (*runner.Invocation, error) {
	return nil, errors.New("cannot express this turn")
}

func TestPrepareErrorIsInvalid(t *testing.T) {
	f := newFixture(t, config.OnBusyWait, func(int, runner.Request) (*runner.Outcome, error) {
		t.Fatal("the process must not start")
		return nil, nil
	})
	withBackend(f, failingBackend{})
	_, err := f.svc.Execute(context.Background(), request(user("x")), nil)
	if kindOf(t, err) != KindInvalid || err.Error() != "cannot express this turn" {
		t.Fatalf("err %v", err)
	}
}

func TestNonNegative(t *testing.T) {
	if nonNegative(-3) != 0 || nonNegative(4) != 4 {
		t.Fatal("nonNegative")
	}
}
