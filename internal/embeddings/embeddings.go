// Package embeddings manages the local embedding models served by Ollama,
// from the bridge's admin page: it lists the installed models, downloads
// and deletes them, and tests one. Clients call Ollama's own
// OpenAI-compatible endpoint for the embeddings; the bridge does not
// compute or proxy them.
package embeddings

import (
	"context"
	"errors"
	"regexp"
	"time"
)

// Pull states.
const (
	PullRunning   = "running"
	PullSucceeded = "succeeded"
	PullFailed    = "failed"
	PullCanceled  = "canceled"
)

// Errors returned by Manager.
var (
	ErrBadModel    = errors.New("invalid model name")
	ErrNotFound    = errors.New("model not found")
	ErrNoPull      = errors.New("no download in progress for this model")
	ErrUnavailable = errors.New("ollama is not reachable")
	// ErrRejected wraps a request Ollama refused, e.g. a model that does
	// not produce embeddings.
	ErrRejected = errors.New("ollama rejected the request")
)

// modelRe accepts Ollama model references: name[:tag], with an optional
// namespace or registry (library/x, hf.co/user/repo:q4).
var modelRe = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._/-]{0,199}(:[a-zA-Z0-9._-]{1,64})?$`)

// ValidModel reports whether name is an acceptable model reference.
func ValidModel(name string) bool { return modelRe.MatchString(name) }

// Model is an installed model.
type Model struct {
	Name          string    `json:"name"`
	Size          int64     `json:"size"`
	ModifiedAt    time.Time `json:"modified_at"`
	Family        string    `json:"family,omitempty"`
	ParameterSize string    `json:"parameter_size,omitempty"`
	// Embedding is nil when Ollama does not report the capabilities.
	Embedding *bool `json:"embedding"`
}

// Pull is the state of a model download.
type Pull struct {
	Model     string    `json:"model"`
	State     string    `json:"state"`
	Status    string    `json:"status"`
	Completed int64     `json:"completed"`
	Total     int64     `json:"total"`
	Error     string    `json:"error,omitempty"`
	StartedAt time.Time `json:"started_at"`
}

// Status is what the admin page shows.
type Status struct {
	Enabled bool `json:"enabled"`
	// ClientURL is the base_url clients use for embeddings.
	ClientURL string  `json:"client_url,omitempty"`
	Reachable bool    `json:"reachable"`
	Version   string  `json:"version,omitempty"`
	Error     string  `json:"error,omitempty"`
	Models    []Model `json:"models"`
	Pulls     []Pull  `json:"pulls"`
}

// TestResult is the outcome of a test embedding.
type TestResult struct {
	Model      string `json:"model"`
	Dimensions int    `json:"dimensions"`
	Millis     int64  `json:"millis"`
}

// Manager manages the embedding models. Implementations are safe for
// concurrent use; there is at most one download per model.
type Manager interface {
	// Status reports the server, the installed models and the downloads.
	Status(ctx context.Context) Status
	// Pull starts downloading model, or returns the download in progress.
	Pull(model string) (Pull, error)
	// CancelPull stops the download of model.
	CancelPull(model string) error
	// Delete removes an installed model.
	Delete(ctx context.Context, model string) error
	// Test computes the embedding of text with model.
	Test(ctx context.Context, model, text string) (TestResult, error)
}
