package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/r3n3r0/agent-bridge/internal/config"
	"github.com/r3n3r0/agent-bridge/internal/prompt"
	"github.com/r3n3r0/agent-bridge/internal/session"
	"github.com/r3n3r0/agent-bridge/internal/turn"
)

// chatCall is a parsed chat completion request.
type chatCall struct {
	turn         *turn.Request   // first (or only) choice
	turns        []*turn.Request // one per choice (n), turn included
	gen          generation
	model        string
	stream       bool
	includeUsage bool
	legacy       bool // deprecated functions API: answer with function_call
}

func (s *Server) handleChat(w http.ResponseWriter, r *http.Request) {
	call, apiErr := s.parseChat(w, r)
	if apiErr != nil {
		writeError(w, apiErr)
		return
	}
	for i, t := range call.turns {
		if err := s.turns.Admit(t); err != nil {
			for _, prev := range call.turns[:i] {
				s.turns.Release(prev)
			}
			writeError(w, toAPIError(err))
			return
		}
	}
	w.Header().Set(headerSessionID, call.turn.SessionKey)
	if call.stream {
		if len(call.turns) == 1 {
			s.streamTurn(r.Context(), w, call)
		} else {
			s.streamChoices(r.Context(), w, call)
		}
		return
	}
	results, err := s.executeAll(r.Context(), call)
	if err != nil {
		if r.Context().Err() != nil {
			return // client gone, nobody to answer
		}
		writeError(w, toAPIError(err))
		return
	}
	total := sumResults(results)
	setTurnHeaders(w.Header(), total)
	choices := make([]choice, len(results))
	for i, res := range results {
		msg, finish := responseMessage(call.finalize(res), call.legacy)
		choices[i] = choice{Index: i, Message: msg, FinishReason: finish}
	}
	writeJSON(w, http.StatusOK, chatCompletion{
		ID:      completionID(),
		Object:  "chat.completion",
		Created: time.Now().Unix(),
		Model:   call.model,
		Choices: choices,
		Usage:   usageOf(total),
	})
}

// executeAll runs every choice concurrently; the first error cancels the
// others and is returned.
func (s *Server) executeAll(ctx context.Context, call *chatCall) ([]*turn.Result, error) {
	if len(call.turns) == 1 {
		res, err := s.turns.Execute(ctx, call.turn, nil)
		if err != nil {
			return nil, err
		}
		return []*turn.Result{res}, nil
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	results := make([]*turn.Result, len(call.turns))
	errs := make([]error, len(call.turns))
	var wg sync.WaitGroup
	for i, t := range call.turns {
		wg.Add(1)
		go func(i int, t *turn.Request) {
			defer wg.Done()
			results[i], errs[i] = s.turns.Execute(ctx, t, nil)
			if errs[i] != nil {
				cancel()
			}
		}(i, t)
	}
	wg.Wait()
	// Prefer the error that caused the cancellation over the cancellations.
	var first error
	for _, err := range errs {
		var te *turn.Error
		if err != nil && (first == nil || (errors.As(first, &te) && te.Kind == turn.KindCanceled)) {
			first = err
		}
	}
	if first != nil {
		return nil, first
	}
	return results, nil
}

// finalize applies the stop sequences to a result's text. Structured
// (JSON schema) output is never cut.
func (c *chatCall) finalize(res *turn.Result) *turn.Result {
	if len(c.gen.stop) == 0 || c.gen.jsonSchema != "" {
		return res
	}
	out := *res
	out.Content = applyStop(res.Content, c.gen.stop)
	return &out
}

// sumResults adds up usage, turns and cost of several choices; the
// duration is the longest one.
func sumResults(results []*turn.Result) *turn.Result {
	total := &turn.Result{}
	for _, r := range results {
		total.PromptTokens += r.PromptTokens
		total.CompletionTokens += r.CompletionTokens
		total.Turns += r.Turns
		total.CostUSD += r.CostUSD
		if r.DurationMs > total.DurationMs {
			total.DurationMs = r.DurationMs
		}
	}
	return total
}

func usageOf(res *turn.Result) usage {
	return usage{
		PromptTokens:     res.PromptTokens,
		CompletionTokens: res.CompletionTokens,
		TotalTokens:      res.PromptTokens + res.CompletionTokens,
	}
}

func setTurnHeaders(h http.Header, res *turn.Result) {
	h.Set(headerTurns, strconv.Itoa(res.Turns))
	h.Set(headerCost, strconv.FormatFloat(res.CostUSD, 'f', 6, 64))
	h.Set(headerDuration, strconv.FormatInt(res.DurationMs, 10))
}

// toAPIError maps domain errors to HTTP errors.
func toAPIError(err error) *apiError {
	var te *turn.Error
	if !errors.As(err, &te) {
		return newError(http.StatusInternalServerError, errServerError, err.Error())
	}
	switch te.Kind {
	case turn.KindInvalid:
		return newError(http.StatusBadRequest, errInvalidRequest, te.Message)
	case turn.KindSessionBusy:
		return newError(http.StatusConflict, errSessionBusy, te.Message)
	case turn.KindQueueFull:
		e := newError(http.StatusTooManyRequests, errQueueFull, te.Message)
		e.RetryAfter = retryAfterQueueFull
		return e
	case turn.KindTimeout:
		return newError(http.StatusGatewayTimeout, errTimeout, te.Message)
	case turn.KindUpstream:
		return newError(http.StatusBadGateway, errUpstream, te.Message)
	case turn.KindCanceled, turn.KindShutdown:
		return newError(http.StatusServiceUnavailable, errServerError, te.Message)
	default:
		return newError(http.StatusInternalServerError, errServerError, te.Message)
	}
}

// parseChat validates the request and resolves agent, overrides and session
// key. Nothing is locked or started yet.
func (s *Server) parseChat(w http.ResponseWriter, r *http.Request) (*chatCall, *apiError) {
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
	if !s.cfg.Images.Enabled && prompt.CountImages(msgs) > 0 {
		return nil, newError(http.StatusBadRequest, errUnsupported, "images are disabled on this server")
	}
	if len(prompt.Current(msgs)) == 0 {
		return nil, newError(http.StatusBadRequest, errInvalidRequest, prompt.ErrNoCurrentRequest.Error())
	}
	agent, ok := s.cfg.Agent(req.Model)
	if !ok {
		return nil, newError(http.StatusNotFound, errModelNotFound, "model "+req.Model+" does not exist")
	}
	spec, cwd, apiErr := applyOverrides(s.cfg.Overrides, r.Header, agent)
	if apiErr != nil {
		return nil, apiErr
	}
	fns, apiErr := parseFunctions(&req)
	if apiErr != nil {
		return nil, apiErr
	}
	gen, apiErr := parseGeneration(&req)
	if apiErr != nil {
		return nil, apiErr
	}
	spec.Effort = gen.effort
	spec.JSONSchema = gen.jsonSchema
	key, apiErr := sessionKey(r.Header.Get(headerSessionID), req.User)
	if apiErr != nil {
		return nil, apiErr
	}
	var reset bool
	switch strings.ToLower(r.Header.Get(headerSessionReset)) {
	case "", "false":
	case "true":
		reset = true
	default:
		return nil, newError(http.StatusBadRequest, errInvalidRequest, headerSessionReset+" must be true or false")
	}

	toolMode, apiErr := headerChoice(r.Header, headerToolMode, s.cfg.ClientTools.Mode, config.ToolModeStateful, config.ToolModeStateless)
	if apiErr != nil {
		return nil, apiErr
	}
	systemMode, apiErr := headerChoice(r.Header, headerSystemMode, s.cfg.SystemMessages, config.SystemAsPrompt, config.SystemAppend, config.SystemReplace)
	if apiErr != nil {
		return nil, apiErr
	}

	call := &chatCall{
		model:  req.Model,
		stream: req.Stream,
		legacy: fns.legacy,
		gen:    gen,
		turn: &turn.Request{
			RequestID:         requestIDFrom(r.Context()),
			Agent:             agent,
			Spec:              spec,
			Cwd:               cwd,
			Messages:          msgs,
			SessionKey:        key,
			Reset:             reset,
			Tools:             fns.defs,
			ToolChoice:        fns.choice,
			ParallelToolCalls: fns.parallel,
			LegacyFunctions:   fns.legacy,
			StatelessTools:    toolMode == config.ToolModeStateless,
			SystemMode:        systemMode,
			MaxOutputTokens:   gen.maxTokens,
		},
	}
	call.turns = []*turn.Request{call.turn}
	if gen.n > 1 {
		// Choices are independent conversations: each gets a fresh session
		// with the full history, and client functions never keep a process
		// waiting (the client continues with only one of the choices).
		call.turn.SessionKey = session.NewKey()
		call.turn.Reset = false
		call.turn.StatelessTools = true
		for i := 1; i < gen.n; i++ {
			c := *call.turn
			c.SessionKey = session.NewKey()
			call.turns = append(call.turns, &c)
		}
	}
	if req.StreamOptions != nil {
		call.includeUsage = req.StreamOptions.IncludeUsage
	}
	return call, nil
}

// headerChoice returns the value of header h, one of allowed, or def when
// the header is absent.
func headerChoice(hdr http.Header, h, def string, allowed ...string) (string, *apiError) {
	v := strings.ToLower(strings.TrimSpace(hdr.Get(h)))
	if v == "" {
		return def, nil
	}
	for _, a := range allowed {
		if v == a {
			return v, nil
		}
	}
	return "", newError(http.StatusBadRequest, errInvalidRequest, h+" must be one of "+strings.Join(allowed, ", "))
}

// sessionKey picks the client session key: header, then user field, then a
// generated one.
func sessionKey(header, user string) (string, *apiError) {
	switch {
	case header != "":
		if !session.ValidKey(header) {
			return "", newError(http.StatusBadRequest, errInvalidRequest, headerSessionID+" must be 1-128 characters from [a-zA-Z0-9._:-]")
		}
		return header, nil
	case user != "":
		if session.ValidKey(user) {
			return user, nil
		}
		sum := sha256.Sum256([]byte(user))
		return "user_" + hex.EncodeToString(sum[:16]), nil
	default:
		return session.NewKey(), nil
	}
}
