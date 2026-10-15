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

func (s *sseWriter) chunk(d delta, finish *string) {
	s.data(chatChunk{
		ID: s.id, Object: "chat.completion.chunk", Created: s.created, Model: s.model,
		Choices: []chunkChoice{{Index: 0, Delta: d, FinishReason: finish}},
	})
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
	res *turnResult
	err *apiError
}

// streamTurn runs the turn and streams the response as SSE: role chunk,
// progress (comments or content), final content, finish chunk, optional
// usage chunk, [DONE].
func (s *Server) streamTurn(ctx context.Context, w http.ResponseWriter, t *turn, model string, includeUsage bool) {
	h := w.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-cache")
	h.Set("Connection", "keep-alive")
	h.Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)

	flusher, _ := w.(http.Flusher)
	sse := &sseWriter{w: w, flusher: flusher, id: completionID(), model: model, created: time.Now().Unix()}
	sse.chunk(delta{Role: prompt.RoleAssistant}, nil)

	progress := make(chan string, 16)
	done := make(chan execResult, 1)
	go func() {
		res, err := s.execute(ctx, t, func(text string) {
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
				sse.data(r.err.body())
				sse.done()
				return
			}
			sse.chunk(delta{Content: r.res.content}, nil)
			finish := r.res.finishReason
			sse.chunk(delta{}, &finish)
			if includeUsage {
				u := r.res.usage
				sse.data(chatChunk{
					ID: sse.id, Object: "chat.completion.chunk", Created: sse.created, Model: model,
					Choices: []chunkChoice{}, Usage: &u,
				})
			}
			sse.done()
			return
		}
	}
}
