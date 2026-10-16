package turn

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/r3n3r0/agent-bridge/internal/redact"
	"github.com/r3n3r0/agent-bridge/internal/runner"
	"github.com/r3n3r0/agent-bridge/internal/stats"
)

// classify maps the process outcome to a Result or an Error.
func (lt *liveTurn) classify(out *runner.Outcome, runErr error) (*Result, *Error) {
	switch {
	case runErr == nil:
		res := out.Result
		content := res.Result
		var finish, outcome string
		switch {
		case res.Subtype == runner.SubtypeStructuredOutputFail:
			return nil, lt.finish(stats.OutcomeError, newError(KindUpstream, "the model could not produce output matching the JSON schema"))
		case res.Subtype == runner.SubtypeSuccess && !res.IsError && lt.req.Spec.JSONSchema != "":
			structured, err := compactJSON(res.StructuredOutput)
			if err != nil {
				return nil, lt.finish(stats.OutcomeError, newError(KindUpstream, "the turn ended without structured output"))
			}
			content = structured
			finish, outcome = FinishStop, stats.OutcomeSuccess
		case res.Subtype == runner.SubtypeSuccess && !res.IsError:
			finish, outcome = FinishStop, stats.OutcomeSuccess
		case res.Subtype == runner.SubtypeMaxTurns:
			finish, outcome = FinishLength, stats.OutcomeMaxTurns
		default:
			msg := fmt.Sprintf("CLI turn failed (subtype %q)", res.Subtype)
			if res.Result != "" {
				msg += ": " + redact.Tail(res.Result, maxErrorDetail)
			}
			return nil, lt.finish(stats.OutcomeError, newError(KindUpstream, "%s", msg))
		}
		lt.finish(outcome, nil)
		promptTokens := res.Usage.InputTokens + res.Usage.CacheCreationTokens + res.Usage.CacheReadTokens
		return &Result{
			Content:          content,
			FinishReason:     finish,
			PromptTokens:     promptTokens,
			CompletionTokens: res.Usage.OutputTokens,
			Turns:            res.NumTurns,
			CostUSD:          res.CostUSD,
			DurationMs:       lt.rec.DurationMs,
		}, nil
	case errors.Is(runErr, runner.ErrTimeout):
		return nil, lt.finish(stats.OutcomeTimeout,
			newError(KindTimeout, "turn exceeded the agent timeout of %s", lt.req.Agent.Timeout))
	case errors.Is(runErr, runner.ErrCanceled) && lt.stoppedAfterCalls():
		lt.finish(stats.OutcomeToolCalls, nil)
		return nil, newError(KindCanceled, "process stopped after returning function calls")
	case errors.Is(runErr, runner.ErrCanceled):
		e := lt.canceled("while running")
		outcome := stats.OutcomeCanceled
		if e.Kind == KindTimeout {
			outcome = stats.OutcomeTimeout
		}
		return nil, lt.finish(outcome, e)
	default:
		msg := runErr.Error()
		if tail := strings.TrimSpace(redact.Tail(out.Stderr, maxErrorDetail)); tail != "" {
			msg += ": " + tail
		}
		return nil, lt.finish(stats.OutcomeError, newError(KindUpstream, "%s", msg))
	}
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

// compactJSON returns raw as compact JSON; empty or null is an error.
func compactJSON(raw json.RawMessage) (string, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return "", errors.New("no structured output")
	}
	var b bytes.Buffer
	if err := json.Compact(&b, raw); err != nil {
		return "", err
	}
	return b.String(), nil
}
