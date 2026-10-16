package server

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/r3n3r0/agent-bridge/internal/config"
	"github.com/r3n3r0/agent-bridge/internal/prompt"
	"github.com/r3n3r0/agent-bridge/internal/turn"
)

const maxProgressComment = 300

// sseWriter writes server-sent events and flushes after each one.
type sseWriter struct {
	w       io.Writer
	flusher http.Flusher
	id      string
	model   string
	created int64
}

func (s *sseWriter) flush() {
	if s.flusher != nil {
		s.flusher.Flush()
	}
}

func (s *sseWriter) data(v any) {
	b, _ := json.Marshal(v)
	_, _ = io.WriteString(s.w, "data: ")
	_, _ = s.w.Write(b)
	_, _ = io.WriteString(s.w, "\n\n")
	s.flush()
}

// comment writes an SSE comment line. No blank line follows it: some clients
// dispatch an empty event on a blank line and fail to decode it.
func (s *sseWriter) comment(text string) {
	_, _ = io.WriteString(s.w, ": "+text+"\n")
	s.flush()
}

func (s *sseWriter) chunk(d delta, finish *string) { s.chunkAt(0, d, finish) }

func (s *sseWriter) chunkAt(index int, d delta, finish *string) {
	s.data(chatChunk{
		ID: s.id, Object: "chat.completion.chunk", Created: s.created, Model: s.model,
		Choices: []chunkChoice{{Index: index, Delta: d, FinishReason: finish}},
	})
}

// result writes the final chunks of one choice: content, tool or function
// calls, and the chunk carrying finish_reason.
func (s *sseWriter) result(index int, res *turn.Result, legacy bool) {
	finish := res.FinishReason
	if res.Content != "" || len(res.ToolCalls) == 0 {
		s.chunkAt(index, delta{Content: res.Content}, nil)
	}
	switch {
	case len(res.ToolCalls) > 0 && legacy:
		c := res.ToolCalls[0]
		s.chunkAt(index, delta{FunctionCall: &functionCall{Name: c.Name, Arguments: c.Arguments}}, nil)
		finish = "function_call"
	case len(res.ToolCalls) > 0:
		for i, c := range res.ToolCalls {
			s.chunkAt(index, delta{ToolCalls: []streamToolCall{{
				Index: i, ID: c.ID, Type: "function",
				Function: functionCall{Name: c.Name, Arguments: c.Arguments},
			}}}, nil)
		}
	}
	s.chunkAt(index, delta{}, &finish)
}

func (s *sseWriter) usage(u usage) {
	s.data(chatChunk{
		ID: s.id, Object: "chat.completion.chunk", Created: s.created, Model: s.model,
		Choices: []chunkChoice{}, Usage: &u,
	})
}

// start sets the SSE headers and sends the role chunk of every choice.
func startSSE(w http.ResponseWriter, model string, choices int) *sseWriter {
	h := w.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-cache")
	h.Set("Connection", "keep-alive")
	h.Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	flusher, _ := w.(http.Flusher)
	sse := &sseWriter{w: w, flusher: flusher, id: completionID(), model: model, created: time.Now().Unix()}
	for i := 0; i < choices; i++ {
		sse.chunkAt(i, delta{Role: prompt.RoleAssistant}, nil)
	}
	return sse
}

func (s *sseWriter) done() {
	_, _ = io.WriteString(s.w, "data: [DONE]\n\n")
	s.flush()
}

// oneLine collapses whitespace so the text fits in a single SSE comment line,
// and truncates it.
func oneLine(text string, max int) string {
	return truncateRunes(strings.Join(strings.Fields(text), " "), max)
}

func truncateRunes(s string, max int) string {
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	return string(r[:max])
}

type execResult struct {
	res *turn.Result
	err error
}

// streamTurn runs the turn and streams the response as SSE: role chunk,
// progress (comments or content), final content, finish chunk, optional
// usage chunk, [DONE].
func (s *Server) streamTurn(ctx context.Context, w http.ResponseWriter, call *chatCall) {
	sse := startSSE(w, call.model, 1)

	progress := make(chan string, 16)
	done := make(chan execResult, 1)
	go func() {
		res, err := s.turns.Execute(ctx, call.turn, func(text string) {
			select {
			case progress <- text:
			case <-ctx.Done():
			}
		})
		done <- execResult{res, err}
	}()

	mode := s.cfg.StreamProgress
	emit := func(text string) bool {
		switch mode {
		case config.StreamComments:
			if line := oneLine(text, maxProgressComment); line != "" {
				sse.comment(line)
				return true
			}
		case config.StreamContent:
			sse.chunk(delta{Content: text + "\n\n"}, nil)
			return true
		}
		return false
	}
	keepalive := time.NewTimer(s.keepalive)
	defer keepalive.Stop()

	for {
		select {
		case text := <-progress:
			if emit(text) {
				if !keepalive.Stop() {
					select {
					case <-keepalive.C:
					default:
					}
				}
				keepalive.Reset(s.keepalive)
			}
		case <-keepalive.C:
			sse.comment("keepalive")
			keepalive.Reset(s.keepalive)
		case r := <-done:
		drain:
			for {
				select {
				case text := <-progress:
					emit(text)
				default:
					break drain
				}
			}
			if r.err != nil {
				sse.data(toAPIError(r.err).body())
				sse.done()
				return
			}
			sse.result(0, call.finalize(r.res), call.legacy)
			if call.includeUsage {
				sse.usage(usageOf(r.res))
			}
			sse.done()
			return
		}
	}
}

// streamChoices streams n > 1 independent choices. Each choice's final
// chunks are sent as soon as it completes; there are no progress texts,
// only keepalive comments. An error in any choice cancels the others and
// ends the stream with the error event.
func (s *Server) streamChoices(ctx context.Context, w http.ResponseWriter, call *chatCall) {
	sse := startSSE(w, call.model, len(call.turns))
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	type indexed struct {
		i   int
		res *turn.Result
		err error
	}
	done := make(chan indexed, len(call.turns))
	for i, t := range call.turns {
		go func(i int, t *turn.Request) {
			res, err := s.turns.Execute(ctx, t, nil)
			done <- indexed{i, res, err}
		}(i, t)
	}
	keepalive := time.NewTicker(s.keepalive)
	defer keepalive.Stop()

	results := make([]*turn.Result, 0, len(call.turns))
	for remaining := len(call.turns); remaining > 0; {
		select {
		case <-keepalive.C:
			sse.comment("keepalive")
		case r := <-done:
			remaining--
			if r.err != nil {
				cancel()
				for ; remaining > 0; remaining-- {
					<-done
				}
				sse.data(toAPIError(r.err).body())
				sse.done()
				return
			}
			results = append(results, r.res)
			sse.result(r.i, call.finalize(r.res), call.legacy)
		}
	}
	if call.includeUsage {
		sse.usage(usageOf(sumResults(results)))
	}
	sse.done()
}
