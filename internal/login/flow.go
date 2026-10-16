package login

import (
	"context"
	"errors"
	"io"
	"os/exec"
	"sync"
	"time"
)

const maxOutput = 64 << 10

// flow is one running or finished login process.
type flow struct {
	started      time.Time
	acceptsInput bool
	stop         context.CancelFunc

	mu       sync.Mutex
	stdin    io.WriteCloser
	output   []byte
	state    string
	canceled bool
}

func startFlow(args, env []string, acceptsInput bool, timeout time.Duration, done func(state string)) (*flow, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	cmd := exec.CommandContext(ctx, args[0], args[1:]...)
	cmd.Env = env
	cmd.WaitDelay = 5 * time.Second
	killTree(cmd)
	f := &flow{started: time.Now().UTC(), acceptsInput: acceptsInput, stop: cancel, state: StateRunning}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		cancel()
		return nil, err
	}
	f.stdin = stdin
	cmd.Stdout = f
	cmd.Stderr = f
	if err := cmd.Start(); err != nil {
		cancel()
		return nil, err
	}
	go func() {
		err := cmd.Wait()
		cancel()
		f.mu.Lock()
		switch {
		case f.canceled:
			f.state = StateCanceled
		case err == nil:
			f.state = StateSucceeded
		default:
			f.state = StateFailed
		}
		state := f.state
		f.mu.Unlock()
		done(state)
	}()
	return f, nil
}

// Write collects the process output, keeping the last maxOutput bytes.
func (f *flow) Write(p []byte) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.output = append(f.output, p...)
	if len(f.output) > maxOutput {
		f.output = append([]byte(nil), f.output[len(f.output)-maxOutput:]...)
	}
	return len(p), nil
}

func (f *flow) running() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.state == StateRunning
}

func (f *flow) view() FlowView {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := cleanOutput(string(f.output))
	return FlowView{State: f.state, Output: out, URLs: findURLs(out), Codes: findCodes(out), StartedAt: f.started,
		AcceptsInput: f.acceptsInput && f.state == StateRunning}
}

func (f *flow) input(text string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.state != StateRunning {
		return ErrNoFlow
	}
	_, err := io.WriteString(f.stdin, text+"\n")
	if errors.Is(err, io.ErrClosedPipe) {
		return ErrNoFlow
	}
	return err
}

func (f *flow) cancel() {
	f.mu.Lock()
	f.canceled = true
	f.mu.Unlock()
	f.stop()
}
