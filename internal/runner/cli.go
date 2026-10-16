package runner

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"sync"
	"time"
)

// CLIExecutor starts the CLI as a child process, without a shell, and
// terminates its whole process tree on timeout or cancellation. The zero
// value is ready to use.
type CLIExecutor struct{}

var _ Executor = CLIExecutor{}

// Run implements Executor: it starts the CLI, feeds the prompt on stdin and
// reads events until the process exits.
func (CLIExecutor) Run(ctx context.Context, req Request) (*Outcome, error) {
	out := &Outcome{}
	start := time.Now()
	defer func() { out.Duration = time.Since(start) }()

	runCtx := ctx
	if req.Timeout > 0 {
		var cancel context.CancelFunc
		runCtx, cancel = context.WithTimeout(ctx, req.Timeout)
		defer cancel()
	}

	var mu sync.Mutex
	handle := func(line []byte) {
		for _, ev := range ParseLine(line) {
			mu.Lock()
			switch ev.Kind {
			case KindInit:
				if ev.SessionID != "" {
					out.SessionID = ev.SessionID
				}
				out.Model = ev.Model
				out.APIKeySource = ev.APIKeySource
			case KindResult:
				if ev.SessionID != "" {
					out.SessionID = ev.SessionID
				}
				out.Result = ev.Result
			}
			mu.Unlock()
			if req.OnEvent != nil {
				req.OnEvent(ev)
			}
		}
	}

	cmd := exec.CommandContext(runCtx, req.Binary, req.Args...)
	cmd.Dir = req.Dir
	cmd.Env = req.Env
	cmd.Stdin = strings.NewReader(req.Stdin)
	lw := &lineWriter{max: MaxLineBytes, fn: handle}
	cmd.Stdout = lw
	stderr := &tailBuffer{max: stderrKeep}
	cmd.Stderr = stderr
	cmd.WaitDelay = killGrace
	pc := newProcessController()
	pc.prepare(cmd)

	err := cmd.Start()
	if err != nil {
		return out, fmt.Errorf("start %s: %w", req.Binary, err)
	}
	err = cmd.Wait()
	if runCtx.Err() != nil {
		pc.cleanup(cmd)
	} else {
		pc.stop()
	}
	lw.flush()

	mu.Lock()
	defer mu.Unlock()
	out.ExitErr = err
	out.Stderr = stderr.String()
	if out.Result != nil {
		return out, nil
	}
	switch {
	case ctx.Err() != nil:
		return out, ErrCanceled
	case runCtx.Err() != nil:
		return out, ErrTimeout
	}
	return out, ErrNoResult
}

// lineWriter splits written bytes into lines and calls fn for each complete
