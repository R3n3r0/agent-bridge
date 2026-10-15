package turn

import (
	"context"
	"crypto/sha256"
	"encoding/hex"

	"github.com/r3n3r0/agent-bridge/internal/config"
	"github.com/r3n3r0/agent-bridge/internal/prompt"
	"github.com/r3n3r0/agent-bridge/internal/redact"
	"github.com/r3n3r0/agent-bridge/internal/runner"
)

func (r *Request) omitInstructions() bool {
	return r.SystemMode == config.SystemAppend || r.SystemMode == config.SystemReplace
}

// applySystemMode moves the request's system and developer messages into
// the CLI system prompt when SystemMode asks for it. They are combined
// with the agent's own system prompt, which always comes first.
func applySystemMode(req *Request) {
	if !req.omitInstructions() {
		return
	}
	instructions := prompt.Instructions(req.Messages)
	if req.SystemMode == config.SystemReplace {
		req.Spec.SystemPromptMode = config.PromptModeReplace
	}
	if instructions == "" {
		return
	}
	if req.Spec.SystemPrompt == "" {
		req.Spec.SystemPrompt = instructions
	} else {
		req.Spec.SystemPrompt += "\n\n" + instructions
	}
}

// input is what is written to the CLI's stdin for one process.
type input struct {
	text   string
	images []runner.ImageBlock
}

// prepareInput builds the prompt and loads its images.
func (s *service) prepareInput(ctx context.Context, req *Request, resumed bool) (input, *Error) {
	p, err := prompt.BuildPrompt(req.Messages, prompt.Options{Resumed: resumed, OmitInstructions: req.omitInstructions()})
	if err != nil {
		return input{}, newError(KindInvalid, "%v", err)
	}
	in := input{text: p.Text}
	if len(p.Images) == 0 {
		return in, nil
	}
	if s.Images == nil {
		return input{}, newError(KindInvalid, "images are not enabled on this server")
	}
	if s.MaxImages > 0 && len(p.Images) > s.MaxImages {
		return input{}, newError(KindInvalid, "too many images: %d, the limit is %d", len(p.Images), s.MaxImages)
	}
	for i, url := range p.Images {
		img, err := s.Images.Resolve(ctx, url)
		if err != nil {
			if ctx.Err() != nil {
				return input{}, newError(KindCanceled, "request canceled while loading images")
			}
			return input{}, newError(KindInvalid, "image %d: %v", i+1, err)
		}
		in.images = append(in.images, runner.ImageBlock{MediaType: img.MediaType, Data: img.Data})
	}
	if n := len(runner.UserMessage(in.text, in.images)); n > prompt.MaxBytes {
		return input{}, newError(KindInvalid, "request too large: %d bytes with images, the limit is %d", n, prompt.MaxBytes)
	}
	return in, nil
}

func (lt *liveTurn) exec(in input, resumeID string) (*runner.Outcome, error) {
	s := lt.svc
	spec := lt.req.Spec
	spec.ResumeID = resumeID
	inv, err := s.Backend.Prepare(runner.Turn{
		Spec:            spec,
		Prompt:          in.text,
		Images:          in.images,
		BaseEnv:         s.BaseEnv,
		AuthMode:        s.AuthMode,
		MaxOutputTokens: lt.req.MaxOutputTokens,
		ToolTimeout:     lt.req.Agent.Timeout,
	})
	if err != nil {
		return &runner.Outcome{}, &prepareError{err}
	}
	defer inv.Cleanup()
	req := runner.Request{
		Binary:  s.Binary,
		Args:    inv.Args,
		Env:     inv.Env,
		Dir:     lt.req.Cwd,
		Stdin:   inv.Stdin,
		Timeout: lt.req.Agent.Timeout,
		Parser:  s.Backend.NewParser(),
		OnEvent: lt.onEvent,
	}
	lt.log.Debug("starting CLI", "backend", s.Backend.Name(), "resume", resumeID != "", "images", len(in.images),
		"prompt", redact.Truncate(in.text, maxLoggedText))
	return s.Executor.Run(lt.ctx, req)
}

// prepareError is a turn the backend cannot express on its command line.
type prepareError struct{ err error }

func (e *prepareError) Error() string { return e.err.Error() }

func (lt *liveTurn) onEvent(ev runner.Event) {
	switch ev.Kind {
	case runner.KindInit:
		lt.log.Debug("CLI session started", "cli_session_id", ev.SessionID, "model", ev.Model, "api_key_source", ev.APIKeySource)
	case runner.KindText:
		lt.log.Debug("assistant text", "text", redact.Truncate(ev.Text, maxLoggedText))
		lt.mu.Lock()
		lt.texts = append(lt.texts, ev.Text)
		progress := lt.progress
		lt.mu.Unlock()
		if progress != nil {
			progress(ev.Text)
		}
	case runner.KindUsage:
		lt.mu.Lock()
		lt.lastUsage = ev.Usage
		lt.mu.Unlock()
	case runner.KindToolUse:
		lt.log.Debug("tool use", "tool", ev.ToolName)
	case runner.KindMalformed:
		lt.log.Debug("ignored non-JSON output line")
	case runner.KindIgnored:
		if ev.Text != "" {
			lt.log.Debug("CLI notice", "type", ev.Type, "text", redact.Truncate(ev.Text, maxLoggedText))
		} else {
			lt.log.Debug("ignored event", "type", ev.Type)
		}
	}
}

func instructionsHash(msgs []prompt.Message) string {
	text := prompt.Instructions(msgs)
	if text == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(text))
	return hex.EncodeToString(sum[:8])
}
