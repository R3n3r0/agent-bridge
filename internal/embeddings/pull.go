package embeddings

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"sort"
	"time"
)

// Download limits: a download stops after pullTimeout; a finished one stays
// visible for pullKeep so the page can show how it ended.
const (
	pullTimeout = 2 * time.Hour
	pullKeep    = 10 * time.Minute
)

// pullJob is one download; its fields are guarded by OllamaManager.mu.
type pullJob struct {
	view   Pull
	cancel context.CancelFunc
	ended  time.Time
}

// Pull implements Manager.
func (m *OllamaManager) Pull(model string) (Pull, error) {
	if !ValidModel(model) {
		return Pull{}, ErrBadModel
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if j, ok := m.pulls[model]; ok && j.view.State == PullRunning {
		return j.view, nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), pullTimeout)
	j := &pullJob{cancel: cancel, view: Pull{Model: model, State: PullRunning, Status: "starting", StartedAt: time.Now()}}
	m.pulls[model] = j
	m.log.Info("embedding model download started", "model", model)
	go m.runPull(ctx, j)
	return j.view, nil
}

// CancelPull implements Manager.
func (m *OllamaManager) CancelPull(model string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	j, ok := m.pulls[model]
	if !ok || j.view.State != PullRunning {
		return ErrNoPull
	}
	j.cancel()
	return nil
}

// runPull follows Ollama's streamed progress until the download ends.
func (m *OllamaManager) runPull(ctx context.Context, j *pullJob) {
	defer j.cancel()
	err := m.streamPull(ctx, j)
	m.mu.Lock()
	defer m.mu.Unlock()
	j.ended = time.Now()
	switch {
	case err == nil:
		j.view.State, j.view.Status = PullSucceeded, "success"
	case errors.Is(err, context.Canceled):
		j.view.State, j.view.Status = PullCanceled, "canceled"
	default:
		j.view.State, j.view.Error = PullFailed, err.Error()
	}
	m.log.Info("embedding model download ended", "model", j.view.Model, "state", j.view.State)
}

// streamPull reads the NDJSON progress of /api/pull.
func (m *OllamaManager) streamPull(ctx context.Context, j *pullJob) error {
	resp, err := m.send(ctx, http.MethodPost, "/api/pull", map[string]any{"model": j.view.Model, "stream": true})
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	sc := bufio.NewScanner(resp.Body)
	sc.Buffer(make([]byte, 64<<10), 1<<20)
	done := false
	for sc.Scan() {
		var p struct {
			Status    string `json:"status"`
			Total     int64  `json:"total"`
			Completed int64  `json:"completed"`
			Error     string `json:"error"`
		}
		if json.Unmarshal(sc.Bytes(), &p) != nil {
			continue
		}
		if p.Error != "" {
			return errors.New(p.Error)
		}
		m.mu.Lock()
		j.view.Status, j.view.Total, j.view.Completed = p.Status, p.Total, p.Completed
		m.mu.Unlock()
		done = p.Status == "success"
	}
	if err := sc.Err(); err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return err
	}
	if !done {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return errors.New("download interrupted")
	}
	return nil
}

// pullViews returns the downloads, running first, and forgets the ones
// that ended more than pullKeep ago.
func (m *OllamaManager) pullViews() []Pull {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := []Pull{}
	for name, j := range m.pulls {
		if j.view.State != PullRunning && time.Since(j.ended) > pullKeep {
			delete(m.pulls, name)
			continue
		}
		out = append(out, j.view)
	}
	sort.Slice(out, func(a, b int) bool {
		ra, rb := out[a].State == PullRunning, out[b].State == PullRunning
		if ra != rb {
			return ra
		}
		return out[a].StartedAt.After(out[b].StartedAt)
	})
	return out
}
