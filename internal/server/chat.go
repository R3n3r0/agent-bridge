package server

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/r3n3r0/agent-bridge/internal/config"
	"github.com/r3n3r0/agent-bridge/internal/prompt"
	"github.com/r3n3r0/agent-bridge/internal/redact"
	"github.com/r3n3r0/agent-bridge/internal/runner"
	"github.com/r3n3r0/agent-bridge/internal/session"
	"github.com/r3n3r0/agent-bridge/internal/stats"
)

const (
	maxSystemPromptOverride = 16 << 10
	maxStderrInError        = 2000
	maxLoggedText           = 500
)

// turn carries everything needed to execute one chat completion.
type turn struct {
	requestID  string
	agent      *config.Agent
	spec       runner.Spec
	cwd        string
	msgs       []prompt.Message
	sessionKey string
	internal   string
	reset      bool
	unlock     func() // session lock already held (on_busy: reject)
}

// turnResult is a successful (HTTP 200) turn.
type turnResult struct {
	content      string
	finishReason string
	usage        usage
	turns        int
	costUSD      float64
	durationMs   int64
}

func (s *Server) handleChat(w http.ResponseWriter, r *http.Request) {
	t, apiErr := s.prepare(w, r)
	if apiErr != nil {
		writeError(w, apiErr)
		return
	}
	stream, includeUsage := t.stream, t.includeUsage
	tt := t.turn

	if s.cfg.Sessions.OnBusy == config.OnBusyReject {
		unlock, err := s.reg.TryLock(tt.internal)
		if err != nil {
			writeError(w, newError(http.StatusConflict, errSessionBusy, "session "+tt.sessionKey+" is busy"))
			return
		}
		tt.unlock = unlock
	}
	if s.limiter.Full() {
		if tt.unlock != nil {
			tt.unlock()
		}
		writeError(w, queueFullError())
		return
	}

	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	stop := context.AfterFunc(s.hardStop, cancel)
	defer stop()

	w.Header().Set(headerSessionID, tt.sessionKey)
	if stream {
		s.streamTurn(ctx, w, tt, t.model, includeUsage)
		return
	}
	res, apiErr := s.execute(ctx, tt, nil)
	if apiErr != nil {
		if r.Context().Err() != nil {
			return // client gone, nobody to answer
		}
		writeError(w, apiErr)
		return
	}
	setTurnHeaders(w.Header(), res)
	writeJSON(w, http.StatusOK, chatCompletion{
		ID:      completionID(),
		Object:  "chat.completion",
		Created: time.Now().Unix(),
		Model:   t.model,
		Choices: []choice{{
			Index:        0,
			Message:      chatMessage{Role: prompt.RoleAssistant, Content: res.content},
			FinishReason: res.finishReason,
		}},
		Usage: res.usage,
	})
}

func setTurnHeaders(h http.Header, res *turnResult) {
	h.Set(headerTurns, strconv.Itoa(res.turns))
	h.Set(headerCost, strconv.FormatFloat(res.costUSD, 'f', 6, 64))
	h.Set(headerDuration, strconv.FormatInt(res.durationMs, 10))
}

func queueFullError() *apiError {
	e := newError(http.StatusTooManyRequests, errQueueFull, "too many requests waiting, retry later")
	e.RetryAfter = retryAfterQueueFull
	return e
}

type prepared struct {
	turn         *turn
	model        string
	stream       bool
	includeUsage bool
}

// prepare validates the request and resolves agent, overrides and session
// key. Nothing is locked or started yet.
func (s *Server) prepare(w http.ResponseWriter, r *http.Request) (*prepared, *apiError) {
	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
	var req chatRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		var mbe *http.MaxBytesError
		if errors.As(err, &mbe) {
			return nil, newError(http.StatusBadRequest, errInvalidRequest, fmt.Sprintf("request body exceeds %d bytes", maxBodyBytes))
		}
		return nil, newError(http.StatusBadRequest, errInvalidRequest, "invalid JSON body: "+err.Error())
	}
	if req.Model == "" {
		return nil, newError(http.StatusBadRequest, errInvalidRequest, "model is required")
	}
	msgs, err := prompt.Parse(req.Messages)
	if err != nil {
		if errors.Is(err, prompt.ErrUnsupportedContent) {
			return nil, newError(http.StatusBadRequest, errUnsupported, err.Error())
		}
		return nil, newError(http.StatusBadRequest, errInvalidRequest, err.Error())
	}
	if len(prompt.CurrentRequest(msgs)) == 0 {
		return nil, newError(http.StatusBadRequest, errInvalidRequest, prompt.ErrNoCurrentRequest.Error())
	}
	agent, ok := s.cfg.Agents[req.Model]
	if !ok {
		return nil, newError(http.StatusNotFound, errModelNotFound, "model "+req.Model+" does not exist")
	}
	spec, cwd, apiErr := s.applyOverrides(r.Header, agent)
	if apiErr != nil {
		return nil, apiErr
	}

	key := r.Header.Get(headerSessionID)
	switch {
	case key != "":
		if !session.ValidKey(key) {
			return nil, newError(http.StatusBadRequest, errInvalidRequest, headerSessionID+" must be 1-128 characters from [a-zA-Z0-9._:-]")
		}
	case req.User != "":
		key = req.User
		if !session.ValidKey(key) {
			sum := sha256.Sum256([]byte(req.User))
			key = "user_" + hex.EncodeToString(sum[:16])
		}
	default:
		key = session.NewKey()
	}
	var reset bool
	switch strings.ToLower(r.Header.Get(headerSessionReset)) {
	case "":
	case "true":
		reset = true
	case "false":
	default:
		return nil, newError(http.StatusBadRequest, errInvalidRequest, headerSessionReset+" must be true or false")
	}

	p := &prepared{
		model:  req.Model,
		stream: req.Stream,
		turn: &turn{
			requestID:  requestIDFrom(r.Context()),
			agent:      agent,
			spec:       spec,
			cwd:        cwd,
			msgs:       msgs,
			sessionKey: key,
			internal:   session.InternalKey(agent.Name, key),
			reset:      reset,
		},
	}
	if req.StreamOptions != nil {
		p.includeUsage = req.StreamOptions.IncludeUsage
	}
	return p, nil
}

// applyOverrides applies the X-Agent-* headers within the limits set in the
// overrides section.
func (s *Server) applyOverrides(h http.Header, a *config.Agent) (runner.Spec, string, *apiError) {
	spec := runner.SpecFromAgent(a)
	cwd := a.Cwd
	hCwd, hTools, hPrompt := h.Get(headerAgentCwd), h.Get(headerAgentTools), h.Get(headerAgentPrompt)
	if hCwd == "" && hTools == "" && hPrompt == "" {
		return spec, cwd, nil
	}
	forbid := func(msg string) (runner.Spec, string, *apiError) {
		return runner.Spec{}, "", newError(http.StatusForbidden, errOverride, msg)
	}
	if a.Builtin {
		return forbid("per-request overrides are not available for built-in models")
	}
	ov := s.cfg.Overrides

	if hCwd != "" {
		if len(ov.AllowedRoots) == 0 {
			return forbid(headerAgentCwd + " is not enabled")
		}
		if !filepath.IsAbs(hCwd) {
			return forbid(headerAgentCwd + " must be an absolute path")
		}
		p, err := config.CanonicalDir(hCwd)
		if err != nil || !insideAny(p, ov.AllowedRoots) {
			return forbid(headerAgentCwd + " is not an existing directory inside an allowed root")
		}
		cwd = p
	}

	if hTools != "" {
		if !ov.AllowTools {
			return forbid(headerAgentTools + " is not enabled")
		}
		agentTools := map[string]bool{}
		for _, t := range a.AllowedTools {
			agentTools[t] = true
		}
		var tools []string
		for _, t := range strings.Split(hTools, ",") {
			t = strings.TrimSpace(t)
			if t == "" {
				continue
			}
			if !agentTools[t] {
				return forbid(headerAgentTools + " can only restrict the agent's allowed_tools; " + strconv.Quote(t) + " is not among them")
			}
			tools = append(tools, t)
		}
		if len(tools) == 0 {
			return forbid(headerAgentTools + " must list at least one tool")
		}
		spec.AllowedTools = tools
	}

	if hPrompt != "" {
		if !ov.AllowSystemPrompt {
			return forbid(headerAgentPrompt + " is not enabled")
		}
		dec, err := base64.StdEncoding.DecodeString(hPrompt)
		if err != nil {
			dec, err = base64.RawStdEncoding.DecodeString(hPrompt)
		}
		if err != nil {
			return runner.Spec{}, "", newError(http.StatusBadRequest, errInvalidRequest, headerAgentPrompt+" must be base64 encoded")
		}
		if len(dec) > maxSystemPromptOverride {
			return forbid(fmt.Sprintf("%s exceeds %d bytes", headerAgentPrompt, maxSystemPromptOverride))
		}
		if spec.SystemPrompt == "" {
			spec.SystemPrompt = string(dec)
		} else {
			spec.SystemPrompt += "\n\n" + string(dec)
		}
	}
	return spec, cwd, nil
}

func insideAny(p string, roots []string) bool {
	for _, root := range roots {
		rel, err := filepath.Rel(root, p)
		if err != nil {
			continue
		}
		if rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel)) {
			return true
		}
	}
	return false
}

func instructionsHash(msgs []prompt.Message) string {
	text := prompt.Instructions(msgs)
	if text == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(text))
	return hex.EncodeToString(sum[:8])
}

// execute runs one turn: session lock, prompt, queue, process, registry
// update, statistics. progress, if set, receives intermediate texts.
func (s *Server) execute(ctx context.Context, t *turn, progress func(string)) (*turnResult, *apiError) {
	log := s.log.With("request_id", t.requestID, "agent", t.agent.Name, "session_key", t.sessionKey)
	rec := stats.Turn{RequestID: t.requestID, Agent: t.agent.Name, SessionKey: t.sessionKey}

	unlock := t.unlock
	if unlock == nil {
		var err error
		unlock, err = s.reg.Lock(ctx, t.internal)
		if err != nil {
			return nil, s.finish(log, rec, stats.OutcomeCanceled, newError(http.StatusServiceUnavailable, errServerError, "request canceled while waiting for the session"))
		}
	}
	defer unlock()

	if t.reset {
		if err := s.reg.Delete(t.internal); err != nil {
			log.Warn("session registry write failed", "error", err)
		}
	}
	entry, found := s.reg.Get(t.internal)
	resumed := found && entry.CLISessionID != "" && entry.Cwd == t.cwd
	if found && !resumed {
		log.Debug("working directory changed, starting a new session", "old_cwd", entry.Cwd, "cwd", t.cwd)
	}
	hash := instructionsHash(t.msgs)
	if resumed && hash != "" && entry.InstructionsHash != "" && hash != entry.InstructionsHash {
		log.Debug("system messages differ from the first turn and are ignored on resumed sessions")
	}
	text, err := prompt.Build(t.msgs, resumed)
	if err != nil {
		return nil, s.finish(log, rec, stats.OutcomeError, newError(http.StatusBadRequest, errInvalidRequest, err.Error()))
	}

	queueStart := time.Now()
	release, err := s.limiter.Acquire(ctx)
	rec.QueueMs = time.Since(queueStart).Milliseconds()
	switch {
	case errors.Is(err, runner.ErrQueueFull), errors.Is(err, runner.ErrQueueTimeout):
		return nil, s.finish(log, rec, stats.OutcomeError, queueFullError())
	case err != nil:
		return nil, s.finish(log, rec, stats.OutcomeCanceled, newError(http.StatusServiceUnavailable, errServerError, "request canceled while queued"))
	}
	defer release()

	if !resumed {
		entry = session.Entry{Agent: t.agent.Name, Cwd: t.cwd, CreatedAt: time.Now().UTC(), InstructionsHash: hash}
	}
	out, runErr := s.runOnce(ctx, log, t, text, entryResume(entry, resumed), progress)
	if resumed && resumeMissing(out, runErr) {
		log.Warn("stored session no longer exists, retrying once as a new session", "claude_session_id", entry.CLISessionID)
		if err := s.reg.Delete(t.internal); err != nil {
			log.Warn("session registry write failed", "error", err)
		}
		resumed = false
		entry = session.Entry{Agent: t.agent.Name, Cwd: t.cwd, CreatedAt: time.Now().UTC(), InstructionsHash: hash}
		text, err = prompt.Build(t.msgs, false)
		if err != nil {
			return nil, s.finish(log, rec, stats.OutcomeError, newError(http.StatusBadRequest, errInvalidRequest, err.Error()))
		}
		out, runErr = s.runOnce(ctx, log, t, text, "", progress)
	}

	rec.Resumed = resumed
	rec.Model = out.Model
	rec.CLISessionID = out.SessionID
	if out.SessionID != "" {
		entry.CLISessionID = out.SessionID
		entry.LastUsedAt = time.Now().UTC()
		entry.Turns++
		if err := s.reg.Put(t.internal, entry); err != nil {
			log.Warn("session registry write failed", "error", err)
		}
	}
	if s.cfg.AuthMode == config.AuthSubscription && out.APIKeySource != "" && out.APIKeySource != "none" {
		log.Error("subscription mode but the CLI reports an API key as credential source; check the environment of the server",
			"api_key_source", out.APIKeySource)
	}
	if out.Result != nil {
		res := out.Result
		rec.Turns = res.NumTurns
		rec.DurationMs = res.DurationMs
		rec.CostUSD = res.CostUSD
		rec.InputTokens = res.Usage.InputTokens
		rec.OutputTokens = res.Usage.OutputTokens
		rec.CacheReadTokens = res.Usage.CacheReadTokens
		rec.CacheCreationTokens = res.Usage.CacheCreationTokens
	}
	if rec.DurationMs == 0 {
		rec.DurationMs = out.Duration.Milliseconds()
	}

	switch {
	case runErr == nil:
		res := out.Result
		var finish, outcome string
		switch {
		case res.Subtype == runner.SubtypeSuccess && !res.IsError:
			finish, outcome = "stop", stats.OutcomeSuccess
		case res.Subtype == runner.SubtypeMaxTurns:
			finish, outcome = "length", stats.OutcomeMaxTurns
		default:
			msg := fmt.Sprintf("CLI turn failed (subtype %q)", res.Subtype)
			if res.Result != "" {
				msg += ": " + redact.Tail(res.Result, maxStderrInError)
			}
			return nil, s.finish(log, rec, stats.OutcomeError, newError(http.StatusBadGateway, errUpstream, msg))
		}
		s.finish(log, rec, outcome, nil)
		promptTokens := res.Usage.InputTokens + res.Usage.CacheCreationTokens + res.Usage.CacheReadTokens
		return &turnResult{
			content:      res.Result,
			finishReason: finish,
			usage:        usage{PromptTokens: promptTokens, CompletionTokens: res.Usage.OutputTokens, TotalTokens: promptTokens + res.Usage.OutputTokens},
			turns:        res.NumTurns,
			costUSD:      res.CostUSD,
			durationMs:   rec.DurationMs,
		}, nil
	case errors.Is(runErr, runner.ErrTimeout):
		return nil, s.finish(log, rec, stats.OutcomeTimeout,
			newError(http.StatusGatewayTimeout, errTimeout, fmt.Sprintf("turn exceeded the agent timeout of %s", t.agent.Timeout)))
	case errors.Is(runErr, runner.ErrCanceled):
		msg := "request canceled"
		if s.hardStop.Err() != nil {
			msg = "server shutting down"
		}
		return nil, s.finish(log, rec, stats.OutcomeCanceled, newError(http.StatusBadGateway, errUpstream, msg))
	default:
		msg := runErr.Error()
		if tail := strings.TrimSpace(redact.Tail(out.Stderr, maxStderrInError)); tail != "" {
			msg += ": " + tail
		}
		return nil, s.finish(log, rec, stats.OutcomeError, newError(http.StatusBadGateway, errUpstream, msg))
	}
}

func entryResume(e session.Entry, resumed bool) string {
	if !resumed {
		return ""
	}
	return e.CLISessionID
}

// resumeMissing reports whether a resumed turn failed because the stored
// session does not exist any more.
func resumeMissing(out *runner.Outcome, err error) bool {
	if errors.Is(err, runner.ErrTimeout) || errors.Is(err, runner.ErrCanceled) {
		return false
	}
	text := strings.ToLower(out.Stderr)
	if out.Result != nil {
		if !out.Result.IsError && out.Result.Subtype == runner.SubtypeSuccess {
			return false
		}
		text += "\n" + strings.ToLower(out.Result.Result)
	}
	return strings.Contains(text, "no conversation found")
}

func (s *Server) runOnce(ctx context.Context, log *slog.Logger, t *turn, text, resumeID string, progress func(string)) (*runner.Outcome, error) {
	spec := t.spec
	spec.ResumeID = resumeID
	req := runner.Request{
		Binary:  s.cfg.Binary,
		Args:    runner.Args(spec),
		Env:     runner.Env(s.baseEnv, s.cfg.AuthMode),
		Dir:     t.cwd,
		Stdin:   text,
		Timeout: t.agent.Timeout,
		OnEvent: func(ev runner.Event) {
			switch ev.Kind {
			case runner.KindInit:
				log.Debug("CLI session started", "claude_session_id", ev.SessionID, "model", ev.Model, "api_key_source", ev.APIKeySource)
			case runner.KindText:
				log.Debug("assistant text", "text", redact.Truncate(ev.Text, maxLoggedText))
				if progress != nil {
					progress(ev.Text)
				}
			case runner.KindToolUse:
				log.Debug("tool use", "tool", ev.ToolName)
			case runner.KindMalformed:
				log.Debug("ignored non-JSON output line")
			case runner.KindIgnored:
				log.Debug("ignored event", "type", ev.Type)
			}
		},
	}
	log.Debug("starting CLI", "resume", resumeID != "", "prompt", redact.Truncate(text, maxLoggedText))
	return runner.Run(ctx, req)
}

// finish records statistics and the end-of-turn log line, and passes e
// through.
func (s *Server) finish(log *slog.Logger, rec stats.Turn, outcome string, e *apiError) *apiError {
	rec.Outcome = outcome
	if err := s.stats.Record(rec); err != nil {
		log.Warn("usage log write failed", "error", err)
	}
	attrs := []any{
		"model", rec.Model, "claude_session_id", rec.CLISessionID, "resumed", rec.Resumed,
		"turns", rec.Turns, "duration_ms", rec.DurationMs, "queue_ms", rec.QueueMs, "cost_usd", rec.CostUSD,
		"input_tokens", rec.InputTokens, "output_tokens", rec.OutputTokens,
		"cache_read_tokens", rec.CacheReadTokens, "cache_creation_tokens", rec.CacheCreationTokens,
		"outcome", outcome,
	}
	if e != nil {
		attrs = append(attrs, "error", e.Message)
		log.Warn("turn finished", attrs...)
	} else {
		log.Info("turn finished", attrs...)
	}
	return e
}
