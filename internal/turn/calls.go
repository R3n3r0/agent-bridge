package turn

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/r3n3r0/agent-bridge/internal/prompt"
	"github.com/r3n3r0/agent-bridge/internal/tools"
)

var _ tools.Handler = (*liveTurn)(nil)

// setupClientTools registers the turn with the broker and writes the MCP
// configuration exposing the request's functions.
func (lt *liveTurn) setupClientTools() error {
	req, s := lt.req, lt.svc
	if len(req.Tools) == 0 || req.ToolChoice.Mode == ToolChoiceNone || s.Broker == nil {
		return nil
	}
	reg, err := s.Broker.Register(lt)
	if err != nil {
		return err
	}
	dir, err := os.MkdirTemp("", "agent-bridge-functions-")
	if err != nil {
		reg.Unregister()
		return err
	}
	cfg, err := tools.WriteFiles(dir, tools.Spec{Endpoint: reg.Endpoint, Token: reg.Token, Tools: req.Tools}, s.ToolHelper)
	if err != nil {
		reg.Unregister()
		os.RemoveAll(dir)
		return err
	}
	lt.unregister, lt.toolsDir = reg.Unregister, dir
	req.Spec.ClientTools = cfg
	if extra := toolInstructions(req); extra != "" {
		if req.Spec.SystemPrompt == "" {
			req.Spec.SystemPrompt = extra
		} else {
			req.Spec.SystemPrompt += "\n\n" + extra
		}
	}
	return nil
}

// toolInstructions turns tool_choice and parallel_tool_calls into
// instructions, the only way to express them to the model.
func toolInstructions(req *Request) string {
	server := tools.ServerName
	var out []string
	switch req.ToolChoice.Mode {
	case ToolChoiceRequired:
		out = append(out, fmt.Sprintf("Before answering you must call at least one tool of the %q MCP server.", server))
	case ToolChoiceFunction:
		out = append(out, fmt.Sprintf("Before answering you must call the tool %q of the %q MCP server.", req.ToolChoice.Function, server))
	}
	if !req.ParallelToolCalls {
		out = append(out, fmt.Sprintf("Call at most one tool of the %q MCP server at a time.", server))
	}
	return strings.Join(out, " ")
}

// resume delivers the function results in req and waits for the next stop.
func (lt *liveTurn) resume(ctx context.Context, req *Request, progress ProgressFunc) (*Result, *Error) {
	results := prompt.ToolResults(req.Messages)
	lt.mu.Lock()
	if lt.finished {
		lt.mu.Unlock()
		req.attach = nil
		return lt.svc.start(ctx, req, progress)
	}
	if lt.parkTimer != nil {
		lt.parkTimer.Stop()
	}
	lt.svc.unpark(lt.parkedIDs)
	lt.parkedIDs = nil
	waiting := lt.waiting
	lt.waiting = nil
	lt.progress = progress
	lt.mu.Unlock()

	for _, pc := range waiting {
		r, ok := results[pc.call.ID]
		res := tools.Result{Content: r}
		if !ok {
			res = tools.Result{Content: "The caller did not return a result for this call.", IsError: true}
		}
		pc.result <- res
	}
	lt.mu.Lock()
	if len(lt.batch) > 0 && lt.batchTimer == nil {
		lt.batchTimer = time.AfterFunc(lt.svc.ToolBatchWindow, lt.flush)
	}
	lt.mu.Unlock()
	return lt.wait(ctx, progress)
}

// Call implements tools.Handler: it queues a call made by the model and
// blocks until the client sends its result.
func (lt *liveTurn) Call(ctx context.Context, name string, arguments []byte) (tools.Result, error) {
	pc := &pendingCall{
		call:   tools.Call{ID: "call_" + randomHex(12), Name: name, Arguments: string(arguments)},
		result: make(chan tools.Result, 1),
	}
	lt.mu.Lock()
	if lt.finished {
		lt.mu.Unlock()
		return tools.Result{}, tools.ErrClosed
	}
	lt.batch = append(lt.batch, pc)
	if lt.batchTimer == nil && len(lt.waiting) == 0 {
		lt.batchTimer = time.AfterFunc(lt.svc.ToolBatchWindow, lt.flush)
	}
	lt.mu.Unlock()
	lt.log.Debug("client function called", "function", name, "tool_call_id", pc.call.ID)

	select {
	case r := <-pc.result:
		return r, nil
	case <-ctx.Done():
		return tools.Result{}, ctx.Err()
	case <-lt.ctx.Done():
		return tools.Result{}, tools.ErrClosed
	}
}

// flush returns the collected calls to the consumer and parks the turn.
func (lt *liveTurn) flush() {
	lt.mu.Lock()
	defer lt.mu.Unlock()
	lt.batchTimer = nil
	if lt.finished || len(lt.batch) == 0 || len(lt.waiting) > 0 {
		return
	}
	n := len(lt.batch)
	if !lt.req.ParallelToolCalls || lt.req.LegacyFunctions {
		// With parallel calls disabled, later calls of the batch wait for
		// the next round; in stateless mode the model makes them again.
		n = 1
	}
	lt.waiting = lt.batch[:n:n]
	lt.batch = append([]*pendingCall(nil), lt.batch[n:]...)

	res := &Result{FinishReason: FinishToolCalls, Content: strings.Join(lt.texts, "\n\n")}
	lt.texts = nil
	if u := lt.lastUsage; u != nil {
		res.PromptTokens = u.InputTokens + u.CacheReadTokens + u.CacheCreationTokens
		res.CompletionTokens = u.OutputTokens
	}
	ids := make([]string, len(lt.waiting))
	for i, pc := range lt.waiting {
		ids[i] = pc.call.ID
		res.ToolCalls = append(res.ToolCalls, pc.call)
	}
	lt.parkedIDs = ids
	lt.svc.park(ids, lt)
	timeout := lt.svc.ToolWaitTimeout
	if lt.req.LegacyFunctions || lt.req.StatelessTools {
		lt.stoppedForCalls = true
		lt.stops <- stopPoint{res: res}
		go lt.abort(newError(KindCanceled, "function calls returned to the caller"))
		return
	}
	lt.parkTimer = time.AfterFunc(timeout, func() {
		lt.log.Warn("no function results received in time, stopping the turn", "timeout", timeout.String())
		lt.abort(newError(KindTimeout, "no function results received within %s", timeout))
	})
	lt.stops <- stopPoint{res: res}
}

func randomHex(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b)
}
