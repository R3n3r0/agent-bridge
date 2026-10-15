package runner

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"sync"
	"time"
)

// Process management constants.
const (
	// killGrace is the time between SIGTERM and SIGKILL, and the exec
	// WaitDelay used so a stuck pipe never blocks a turn forever.
	killGrace = 5 * time.Second
	// MaxLineBytes is the largest stdout line accepted.
	MaxLineBytes = 64 << 20
	// stderrKeep is how much stderr is retained for error messages.
	stderrKeep = 64 << 10
)

// Errors returned by Run when no result event was received.
var (
	ErrTimeout  = errors.New("turn timed out")
	ErrCanceled = errors.New("turn canceled")
	ErrNoResult = errors.New("process ended without a result event")
)

// Request describes one CLI process.
type Request struct {
	Binary  string
	Args    []string
	Env     []string
	Dir     string
	Stdin   string
	Timeout time.Duration
	// OnEvent, if set, receives every parsed event in order.
	OnEvent func(Event)
}

// Outcome collects what was observed while the process ran.
type Outcome struct {
	// SessionID is the last session id seen in init or result events.
	SessionID    string
	Model        string
	APIKeySource string
	Result       *ResultEvent
	Stderr       string
	ExitErr      error
	Duration     time.Duration
}

// Run starts the CLI, feeds the prompt on stdin and reads events until the
// process exits. The returned error is nil whenever a result event arrived;
// otherwise it is ErrTimeout, ErrCanceled, ErrNoResult or a start error. The
// Outcome is never nil.
func Run(ctx context.Context, req Request) (*Outcome, error) {
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
	pc := setupProcess(cmd)

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
// one. Lines longer than max are dropped.
type lineWriter struct {
	max      int
	fn       func([]byte)
	buf      []byte
	overflow bool
}

func (w *lineWriter) Write(p []byte) (int, error) {
	n := len(p)
	for len(p) > 0 {
		i := bytes.IndexByte(p, '\n')
		if i < 0 {
			w.append(p)
			break
		}
		w.append(p[:i])
		w.emit()
		p = p[i+1:]
	}
	return n, nil
}

func (w *lineWriter) append(p []byte) {
	if w.overflow {
		return
	}
	if len(w.buf)+len(p) > w.max {
		w.overflow = true
		w.buf = nil
		return
	}
	w.buf = append(w.buf, p...)
}

func (w *lineWriter) emit() {
	line := bytes.TrimSpace(w.buf)
	if w.overflow {
		w.fn([]byte("\x00overflow"))
	} else if len(line) > 0 {
		w.fn(line)
	}
	w.buf = w.buf[:0]
	w.overflow = false
}

func (w *lineWriter) flush() {
	if len(w.buf) > 0 || w.overflow {
		w.emit()
	}
}

// tailBuffer keeps only the last max bytes written.
type tailBuffer struct {
	mu  sync.Mutex
	max int
	buf []byte
}

func (b *tailBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.buf = append(b.buf, p...)
	if len(b.buf) > b.max {
		b.buf = append([]byte(nil), b.buf[len(b.buf)-b.max:]...)
	}
	return len(p), nil
}

func (b *tailBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return string(b.buf)
}
