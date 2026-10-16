// Package login runs the official login flows of the supported CLIs for the
// operator, from the bridge's admin page. The bridge only relays what the
// CLI prints (links and one-time codes) and what the operator types back
// (a code to paste); the CLI stores the resulting credentials in its own
// home directory, which the bridge never reads.
package login

import (
	"context"
	"errors"
	"time"
)

// Flow states.
const (
	StateRunning   = "running"
	StateSucceeded = "succeeded"
	StateFailed    = "failed"
	StateCanceled  = "canceled"
)

// Errors returned by Manager.
var (
	ErrUnknownCLI = errors.New("unknown CLI")
	ErrNoFlow     = errors.New("no login in progress")
	ErrBadInput   = errors.New("input must be a single line of at most 4096 bytes")
)

// Status is the login state of one CLI.
type Status struct {
	CLI       string `json:"cli"`
	Binary    string `json:"binary"`
	Installed bool   `json:"installed"`
	// LoggedIn is nil when the state could not be determined.
	LoggedIn *bool     `json:"logged_in"`
	Detail   string    `json:"detail,omitempty"`
	Flow     *FlowView `json:"flow,omitempty"`
}

// FlowView is a snapshot of a login flow.
type FlowView struct {
	State  string   `json:"state"`
	Output string   `json:"output"`
	URLs   []string `json:"urls"`
	// Codes are the one-time codes the CLI printed for the operator.
	Codes     []string  `json:"codes"`
	StartedAt time.Time `json:"started_at"`
	// AcceptsInput reports whether the CLI may ask for a pasted code.
	AcceptsInput bool `json:"accepts_input"`
}

// Manager starts and follows login flows. Implementations are safe for
// concurrent use; there is at most one flow per CLI.
type Manager interface {
	// Statuses reports every known CLI, with its current or last flow.
	Statuses(ctx context.Context) []Status
	// Start begins the login flow of cli, or returns the one in progress.
	Start(cli string) (FlowView, error)
	// Input sends one line (for example a pasted code) to the flow of cli.
	Input(cli, text string) error
	// Cancel stops the flow of cli.
	Cancel(cli string) error
}
