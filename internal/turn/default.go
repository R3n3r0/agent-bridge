package turn

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"time"

	"github.com/r3n3r0/agent-bridge/internal/config"
	"github.com/r3n3r0/agent-bridge/internal/media"
	"github.com/r3n3r0/agent-bridge/internal/prompt"
	"github.com/r3n3r0/agent-bridge/internal/runner"
	"github.com/r3n3r0/agent-bridge/internal/session"
	"github.com/r3n3r0/agent-bridge/internal/stats"
	"github.com/r3n3r0/agent-bridge/internal/tools"
)

const (
	maxErrorDetail = 2000
	maxLoggedText  = 500

	defaultToolWaitTimeout = 10 * time.Minute
	defaultToolBatchWindow = 300 * time.Millisecond
)

// Deps are the collaborators of the default Service.
type Deps struct {
	Store    session.Store
	Locker   session.Locker
	Executor runner.Executor
	Limiter  runner.Limiter
	Recorder stats.Recorder
	// Broker routes client function calls; nil disables client functions
	// (the tools field of requests is then ignored).
	Broker tools.Broker
	// Images loads the images referenced by requests; nil rejects requests
	// that contain images.
	Images media.Resolver
	Logger *slog.Logger
}

// Settings configure the default Service.
type Settings struct {
	Binary   string
	AuthMode config.AuthMode
	OnBusy   string
	// BaseEnv is the environment the child environment is derived from.
	BaseEnv []string
	// HardStop, when done, terminates every running turn.
	HardStop context.Context
	// ToolHelper is the command (and leading arguments) that serves client
	// functions over MCP, e.g. the bridge binary with "mcp-tools".
	ToolHelper []string
	// ToolWaitTimeout bounds the wait for function results (default 10m).
	ToolWaitTimeout time.Duration
	// ToolBatchWindow is how long calls are collected before being returned
	// together as parallel tool calls (default 300ms).
	ToolBatchWindow time.Duration
	// MaxImages bounds the images of one turn (0 = no limit).
	MaxImages int
}

// service is the default Service: one CLI process per turn, kept alive while
// it waits for client function results.
type service struct {
	Deps
	Settings

	parkMu sync.Mutex
	parked map[string]*liveTurn // tool call id -> turn waiting for it
}

var _ Service = (*service)(nil)

// NewService returns the default Service.
func NewService(d Deps, s Settings) Service {
	if d.Logger == nil {
		d.Logger = slog.Default()
	}
	if s.HardStop == nil {
		s.HardStop = context.Background()
	}
	if s.ToolWaitTimeout <= 0 {
		s.ToolWaitTimeout = defaultToolWaitTimeout
	}
	if s.ToolBatchWindow <= 0 {
		s.ToolBatchWindow = defaultToolBatchWindow
	}
	return &service{Deps: d, Settings: s, parked: map[string]*liveTurn{}}
}

func (s *service) Admit(req *Request) error {
	if lt := s.waitingTurn(req); lt != nil {
		req.attach = lt // resuming a turn that already holds lock and slot
		return nil
	}
	if s.OnBusy == config.OnBusyReject {
		unlock, err := s.Locker.TryLock(req.InternalKey())
		if err != nil {
			return newError(KindSessionBusy, "session %s is busy", req.SessionKey)
		}
		req.unlock = unlock
	}
	if s.Limiter.Full() {
		if req.unlock != nil {
			req.unlock()
			req.unlock = nil
		}
		return newError(KindQueueFull, "too many requests waiting, retry later")
	}
	return nil
}

func (s *service) Release(req *Request) {
	if req.unlock != nil {
		req.unlock()
		req.unlock = nil
	}
	req.attach = nil
}

func (s *service) Execute(ctx context.Context, req *Request, progress ProgressFunc) (*Result, error) {
	lt := req.attach
	if lt == nil {
		lt = s.waitingTurn(req)
	}
	var res *Result
	var err *Error
	if lt != nil {
		res, err = lt.resume(ctx, req, progress)
	} else {
		res, err = s.start(ctx, req, progress)
	}
	if err != nil {
		return nil, err // keep the interface nil when there is no error
	}
	return res, nil
}

// waitingTurn returns the turn waiting for the function results in req.
func (s *service) waitingTurn(req *Request) *liveTurn {
	s.parkMu.Lock()
	defer s.parkMu.Unlock()
	for id := range prompt.ToolResults(req.Messages) {
		if lt := s.parked[id]; lt != nil && lt.req.Agent.Name == req.Agent.Name {
			return lt
		}
	}
	return nil
}

func (s *service) park(ids []string, lt *liveTurn) {
	s.parkMu.Lock()
	defer s.parkMu.Unlock()
	for _, id := range ids {
		s.parked[id] = lt
	}
}

func (s *service) unpark(ids []string) {
	s.parkMu.Lock()
	defer s.parkMu.Unlock()
	for _, id := range ids {
		delete(s.parked, id)
	}
}

// start prepares and launches a new turn, then waits for its first stop.
func (s *service) start(ctx context.Context, req *Request, progress ProgressFunc) (*Result, *Error) {
	lt := &liveTurn{svc: s, req: req, stops: make(chan stopPoint, 4)}
	lt.log = s.Logger.With("request_id", req.RequestID, "agent", req.Agent.Name, "session_key", req.SessionKey)
	lt.rec = stats.Turn{RequestID: req.RequestID, Agent: req.Agent.Name, SessionKey: req.SessionKey}

	key := req.InternalKey()
	unlock := req.unlock
	if unlock == nil {
		var err error
		if unlock, err = s.Locker.Lock(ctx, key); err != nil {
			return nil, lt.finish(stats.OutcomeCanceled, lt.canceled("while waiting for the session"))
		}
	}
	held := true
	defer func() {
		if held {
			unlock()
		}
	}()

	if req.Reset {
		lt.storeErr(s.Store.Delete(key))
	}
	entry, found := s.Store.Get(key)
	resumed := found && entry.CLISessionID != "" && entry.Cwd == req.Cwd
	if found && !resumed {
		lt.log.Debug("working directory changed, starting a new session", "old_cwd", entry.Cwd, "cwd", req.Cwd)
	}
	hash := instructionsHash(req.Messages)
	if resumed && hash != "" && entry.InstructionsHash != "" && hash != entry.InstructionsHash {
		lt.log.Debug("system messages differ from the first turn and are ignored on resumed sessions")
	}
	applySystemMode(req)
	in, perr := s.prepareInput(ctx, req, resumed)
	if perr != nil {
		outcome := stats.OutcomeError
		if perr.Kind == KindCanceled {
			outcome = stats.OutcomeCanceled
		}
		return nil, lt.finish(outcome, perr)
	}

	queueStart := time.Now()
	release, err := s.Limiter.Acquire(ctx)
	lt.rec.QueueMs = time.Since(queueStart).Milliseconds()
	switch {
	case errors.Is(err, runner.ErrQueueFull), errors.Is(err, runner.ErrQueueTimeout):
		return nil, lt.finish(stats.OutcomeError, newError(KindQueueFull, "too many requests waiting, retry later"))
	case err != nil:
		return nil, lt.finish(stats.OutcomeCanceled, lt.canceled("while queued"))
	}

	// From here on the process may outlive this request (it can wait for
	// function results), so it gets its own context.
	lt.ctx, lt.cancel = context.WithCancel(context.Background())
	stopOnHard := context.AfterFunc(s.HardStop, lt.cancel)
	if err := lt.setupClientTools(); err != nil {
		stopOnHard()
		lt.cancel()
		release()
		return nil, lt.finish(stats.OutcomeError, newError(KindUpstream, "cannot prepare client functions: %v", err))
	}
	if !resumed {
		entry = session.Entry{Agent: req.Agent.Name, Cwd: req.Cwd, CreatedAt: time.Now().UTC(), InstructionsHash: hash}
	}
	held = false
	go lt.run(func() { stopOnHard(); release(); unlock() }, entry, resumed, in, hash)
	return lt.wait(ctx, progress)
}
