package server

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/r3n3r0/agent-bridge/internal/prompt"
	"github.com/r3n3r0/agent-bridge/internal/session"
	"github.com/r3n3r0/agent-bridge/internal/turn"
)

// chatCall is a parsed chat completion request.
type chatCall struct {
	turn         *turn.Request
	model        string
	stream       bool
	includeUsage bool
}

func (s *Server) handleChat(w http.ResponseWriter, r *http.Request) {
	call, apiErr := s.parseChat(w, r)
	if apiErr != nil {
		writeError(w, apiErr)
		return
	}
	if err := s.turns.Admit(call.turn); err != nil {
		writeError(w, toAPIError(err))
		return
	}
	w.Header().Set(headerSessionID, call.turn.SessionKey)
	if call.stream {
		s.streamTurn(r.Context(), w, call)
		return
	}
	res, err := s.turns.Execute(r.Context(), call.turn, nil)
	if err != nil {
		if r.Context().Err() != nil {
			return // client gone, nobody to answer
		}
		writeError(w, toAPIError(err))
		return
	}
	setTurnHeaders(w.Header(), res)
	writeJSON(w, http.StatusOK, chatCompletion{
		ID:      completionID(),
		Object:  "chat.completion",
		Created: time.Now().Unix(),
		Model:   call.model,
		Choices: []choice{{
			Index:        0,
			Message:      chatMessage{Role: prompt.RoleAssistant, Content: res.Content},
			FinishReason: res.FinishReason,
		}},
		Usage: usageOf(res),
	})
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
	if len(prompt.CurrentRequest(msgs)) == 0 {
		return nil, newError(http.StatusBadRequest, errInvalidRequest, prompt.ErrNoCurrentRequest.Error())
	}
	agent, ok := s.cfg.Agents[req.Model]
	if !ok {
		return nil, newError(http.StatusNotFound, errModelNotFound, "model "+req.Model+" does not exist")
	}
	spec, cwd, apiErr := applyOverrides(s.cfg.Overrides, r.Header, agent)
	if apiErr != nil {
		return nil, apiErr
	}
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

	call := &chatCall{
		model:  req.Model,
		stream: req.Stream,
		turn: &turn.Request{
			RequestID:  requestIDFrom(r.Context()),
			Agent:      agent,
			Spec:       spec,
			Cwd:        cwd,
			Messages:   msgs,
			SessionKey: key,
			Reset:      reset,
		},
	}
	if req.StreamOptions != nil {
		call.includeUsage = req.StreamOptions.IncludeUsage
	}
	return call, nil
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
