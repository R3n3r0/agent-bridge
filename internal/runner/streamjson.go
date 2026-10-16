package runner

import (
	"strings"

	"github.com/r3n3r0/agent-bridge/internal/config"
)

// StreamJSONBackend drives the default CLI: "-p" mode with stream-json
// output, the prompt on stdin, client functions and agent MCP servers as
// --mcp-config files.
type StreamJSONBackend struct{}

var _ Backend = StreamJSONBackend{}

// Name implements Backend.
func (StreamJSONBackend) Name() string { return config.BackendDefault }

// MinVersion implements Backend.
func (StreamJSONBackend) MinVersion() string { return MinCLIVersion }

// CumulativeUsage implements Backend: results report the turn's usage.
func (StreamJSONBackend) CumulativeUsage() bool { return false }

// NewParser implements Backend; the format is stateless.
func (StreamJSONBackend) NewParser() Parser { return streamJSONParser{} }

// Prepare implements Backend. Images switch the input to a stream-json
// user message.
func (StreamJSONBackend) Prepare(t Turn) (*Invocation, error) {
	spec := t.Spec
	stdin := t.Prompt
	if len(t.Images) > 0 {
		spec.StructuredInput = true
		stdin = UserMessage(t.Prompt, t.Images)
	}
	env := Env(t.BaseEnv, t.AuthMode)
	if spec.ClientTools != "" {
		env = WithClientToolsTimeout(env, t.ToolTimeout)
	}
	env = WithMaxOutputTokens(env, t.MaxOutputTokens)
	return &Invocation{Args: Args(spec), Env: env, Stdin: stdin, Cleanup: func() {}}, nil
}

// ResumeMissing implements Backend.
func (StreamJSONBackend) ResumeMissing(out *Outcome) bool {
	text := strings.ToLower(out.Stderr)
	if out.Result != nil {
		if !out.Result.IsError && out.Result.Subtype == SubtypeSuccess {
			return false
		}
		text += "\n" + strings.ToLower(out.Result.Result)
	}
	return strings.Contains(text, "no conversation found")
}

type streamJSONParser struct{}

func (streamJSONParser) Parse(line []byte) []Event { return ParseLine(line) }
