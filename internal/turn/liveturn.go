package turn

import (
	"context"
	"log/slog"
	"os"
	"sync"
	"time"

	"github.com/r3n3r0/agent-bridge/internal/config"
	"github.com/r3n3r0/agent-bridge/internal/runner"
	"github.com/r3n3r0/agent-bridge/internal/session"
	"github.com/r3n3r0/agent-bridge/internal/stats"
	"github.com/r3n3r0/agent-bridge/internal/tools"
)

// stopPoint is where a waiting consumer gets control back: a batch of
// function calls, or the end of the turn.
type stopPoint struct {
	res   *Result
	err   *Error
	final bool
}

type pendingCall struct {
	call   tools.Call
	result chan tools.Result
}

// liveTurn is a running CLI process and its function-call state.
type liveTurn struct {
	svc    *service
	req    *Request
	log    *slog.Logger
	rec    stats.Turn // owned by the run goroutine after start
	ctx    context.Context
	cancel context.CancelFunc
	stops  chan stopPoint // closed after the final stop

	toolsDir   string
	unregister func()

	mu         sync.Mutex
	progress   ProgressFunc
	texts      []string      // assistant texts since the last stop
	lastUsage  *runner.Usage // usage of the latest assistant message
	batch      []*pendingCall
	batchTimer *time.Timer
	waiting    []*pendingCall // returned to the client, awaiting results
	parkTimer  *time.Timer
	parkedIDs  []string
	finished   bool
	reason     *Error // why the turn was stopped from inside
	// stoppedForCalls is set when the process is stopped right after
	// returning function calls (stateless or deprecated functions mode).
	stoppedForCalls bool
}

// wait gives control to the consumer until the next stop point. If ctx ends
// first, the turn is stopped.
func (lt *liveTurn) wait(ctx context.Context, progress ProgressFunc) (*Result, *Error) {
	lt.mu.Lock()
	lt.progress = progress
	lt.mu.Unlock()
	defer func() {
		lt.mu.Lock()
		lt.progress = nil
		lt.mu.Unlock()
	}()
	select {
	case sp, ok := <-lt.stops:
		if ok {
			return sp.res, sp.err
		}
	case <-ctx.Done():
		lt.abort(nil)
	}
	var last stopPoint
	for sp := range lt.stops {
		last = sp
	}
	if last.err == nil {
		last.err = newError(KindCanceled, "request canceled")
	}
	return nil, last.err
}

// abort stops the process; reason, if set, becomes the turn's error.
func (lt *liveTurn) abort(reason *Error) {
	lt.mu.Lock()
	if reason != nil && lt.reason == nil {
		lt.reason = reason
	}
	lt.mu.Unlock()
	lt.cancel()
}

func (lt *liveTurn) canceled(what string) *Error {
	if lt.svc.HardStop.Err() != nil {
		return newError(KindShutdown, "server shutting down")
	}
	lt.mu.Lock()
	reason := lt.reason
	lt.mu.Unlock()
	if reason != nil {
		return reason
	}
	return newError(KindCanceled, "request canceled %s", what)
}

// run executes the process (with the resume fallback), records the outcome,
// releases resources and delivers the final stop.
func (lt *liveTurn) run(release func(), entry session.Entry, resumed bool, in input, hash string) {
	s, req := lt.svc, lt.req
	key := req.InternalKey()
	resumeID := ""
	if resumed {
		resumeID = entry.CLISessionID
	}
	out, runErr := lt.exec(in, resumeID)
	if resumed && resumeMissing(out, runErr) {
		lt.log.Warn("stored session no longer exists, retrying once as a new session", "claude_session_id", entry.CLISessionID)
		lt.storeErr(s.Store.Delete(key))
		resumed = false
		entry = session.Entry{Agent: req.Agent.Name, Cwd: req.Cwd, CreatedAt: time.Now().UTC(), InstructionsHash: hash}
		if full, err := s.prepareInput(lt.ctx, req, false); err == nil {
			out, runErr = lt.exec(full, "")
		}
	}

	lt.rec.Resumed = resumed
	lt.rec.Model = out.Model
	lt.rec.CLISessionID = out.SessionID
	lt.mu.Lock()
	stopped, lastUsage := lt.stoppedForCalls, lt.lastUsage
	lt.mu.Unlock()
	switch {
	case stopped:
		// The CLI session ends with calls that never got their results:
		// it must not be resumed. The next request starts from history.
		lt.storeErr(s.Store.Delete(key))
		if u := lastUsage; u != nil && out.Result == nil {
			lt.rec.InputTokens = u.InputTokens
			lt.rec.OutputTokens = u.OutputTokens
			lt.rec.CacheReadTokens = u.CacheReadTokens
			lt.rec.CacheCreationTokens = u.CacheCreationTokens
		}
	case out.SessionID != "":
		entry.CLISessionID = out.SessionID
		entry.LastUsedAt = time.Now().UTC()
		entry.Turns++
		lt.storeErr(s.Store.Put(key, entry))
	}
	if s.AuthMode == config.AuthSubscription && out.APIKeySource != "" && out.APIKeySource != "none" {
		lt.log.Error("subscription mode but the CLI reports an API key as credential source; check the environment of the server",
			"api_key_source", out.APIKeySource)
	}
	if res := out.Result; res != nil {
		lt.rec.Turns = res.NumTurns
		lt.rec.DurationMs = res.DurationMs
		lt.rec.CostUSD = res.CostUSD
		lt.rec.InputTokens = res.Usage.InputTokens
		lt.rec.OutputTokens = res.Usage.OutputTokens
		lt.rec.CacheReadTokens = res.Usage.CacheReadTokens
		lt.rec.CacheCreationTokens = res.Usage.CacheCreationTokens
	}
	if lt.rec.DurationMs == 0 {
		lt.rec.DurationMs = out.Duration.Milliseconds()
	}
	res, err := lt.classify(out, runErr)

	// Release before answering, so a follow-up request never finds the
	// session or the slot still taken.
	lt.mu.Lock()
	lt.finished = true
	if lt.batchTimer != nil {
		lt.batchTimer.Stop()
	}
	if lt.parkTimer != nil {
		lt.parkTimer.Stop()
	}
	s.unpark(lt.parkedIDs)
	lt.mu.Unlock()
	lt.cancel()
	if lt.unregister != nil {
		lt.unregister()
	}
	if lt.toolsDir != "" {
		os.RemoveAll(lt.toolsDir)
	}
	release()

	lt.mu.Lock()
	lt.stops <- stopPoint{res: res, err: err, final: true}
	close(lt.stops)
	lt.mu.Unlock()
}

func (lt *liveTurn) stoppedAfterCalls() bool {
	lt.mu.Lock()
	defer lt.mu.Unlock()
	return lt.stoppedForCalls
}

func (lt *liveTurn) storeErr(err error) {
	if err != nil {
		lt.log.Warn("session store write failed", "error", err)
	}
}

// finish records statistics and the end-of-turn log line, and passes e
// through.
func (lt *liveTurn) finish(outcome string, e *Error) *Error {
	rec := lt.rec
	rec.Outcome = outcome
	if err := lt.svc.Recorder.Record(rec); err != nil {
		lt.log.Warn("usage log write failed", "error", err)
	}
	attrs := []any{
		"model", rec.Model, "claude_session_id", rec.CLISessionID, "resumed", rec.Resumed,
		"turns", rec.Turns, "duration_ms", rec.DurationMs, "queue_ms", rec.QueueMs, "cost_usd", rec.CostUSD,
		"input_tokens", rec.InputTokens, "output_tokens", rec.OutputTokens,
		"cache_read_tokens", rec.CacheReadTokens, "cache_creation_tokens", rec.CacheCreationTokens,
		"outcome", outcome,
	}
	if e != nil {
		lt.log.Warn("turn finished", append(attrs, "error", e.Message)...)
	} else {
		lt.log.Info("turn finished", attrs...)
	}
	return e
}
