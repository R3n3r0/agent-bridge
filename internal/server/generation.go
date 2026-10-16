package server

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"strings"

	"github.com/r3n3r0/agent-bridge/internal/runner"
)

const (
	maxChoices      = 8
	maxStopSeqs     = 4
	maxSchemaBytes  = 64 << 10
	jsonObjectShape = `{"type":"object"}`
)

// generation holds the OpenAI generation parameters the bridge supports.
type generation struct {
	n          int
	stop       []string
	maxTokens  int
	effort     string
	jsonSchema string
}

// parseGeneration validates n, stop, max_tokens, max_completion_tokens,
// reasoning_effort and response_format.
func parseGeneration(req *chatRequest) (generation, *apiError) {
	invalid := func(format string, a ...any) (generation, *apiError) {
		return generation{}, newError(http.StatusBadRequest, errInvalidRequest, fmt.Sprintf(format, a...))
	}
	g := generation{n: 1}
	if req.N != nil {
		if *req.N < 1 || *req.N > maxChoices {
			return invalid("n must be between 1 and %d", maxChoices)
		}
		g.n = *req.N
	}

	if stop := bytes.TrimSpace(req.Stop); len(stop) > 0 && !bytes.Equal(stop, []byte("null")) {
		var one string
		if json.Unmarshal(stop, &one) == nil {
			g.stop = []string{one}
		} else if err := json.Unmarshal(stop, &g.stop); err != nil {
			return invalid("stop must be a string or an array of strings")
		}
		if len(g.stop) > maxStopSeqs {
			return invalid("stop accepts at most %d sequences", maxStopSeqs)
		}
		for _, s := range g.stop {
			if s == "" {
				return invalid("stop sequences must not be empty")
			}
		}
	}

	for _, v := range []*int{req.MaxTokens, req.MaxCompletionTokens} {
		if v != nil && *v < 1 {
			return invalid("max_tokens and max_completion_tokens must be positive")
		}
	}
	if req.MaxCompletionTokens != nil {
		g.maxTokens = *req.MaxCompletionTokens
	} else if req.MaxTokens != nil {
		g.maxTokens = *req.MaxTokens
	}

	if req.ReasoningEffort != "" {
		if !slices.Contains(runner.EffortLevels, req.ReasoningEffort) {
			return invalid("reasoning_effort must be one of %s", strings.Join(runner.EffortLevels, ", "))
		}
		// The backend maps the value to the levels of its CLI.
		g.effort = req.ReasoningEffort
	}

	if rf := bytes.TrimSpace(req.ResponseFormat); len(rf) > 0 && !bytes.Equal(rf, []byte("null")) {
		var f struct {
			Type       string `json:"type"`
			JSONSchema *struct {
				Name   string          `json:"name"`
				Schema json.RawMessage `json:"schema"`
			} `json:"json_schema"`
		}
		if err := json.Unmarshal(rf, &f); err != nil {
			return invalid("response_format is not valid")
		}
		switch f.Type {
		case "text":
		case "json_object":
			g.jsonSchema = jsonObjectShape
		case "json_schema":
			if f.JSONSchema == nil || len(bytes.TrimSpace(f.JSONSchema.Schema)) == 0 {
				return invalid("response_format.json_schema.schema is required")
			}
			var schema map[string]any
			if err := json.Unmarshal(f.JSONSchema.Schema, &schema); err != nil {
				return invalid("response_format.json_schema.schema must be a JSON object")
			}
			var b bytes.Buffer
			if err := json.Compact(&b, f.JSONSchema.Schema); err != nil {
				return invalid("response_format.json_schema.schema is not valid JSON")
			}
			if b.Len() > maxSchemaBytes {
				return invalid("response_format.json_schema.schema exceeds %d bytes", maxSchemaBytes)
			}
			g.jsonSchema = b.String()
		default:
			return invalid("response_format.type must be text, json_object or json_schema")
		}
	}
	return g, nil
}

// applyStop cuts text at the first occurrence of any stop sequence.
func applyStop(text string, stops []string) string {
	cut := len(text)
	for _, s := range stops {
		if i := strings.Index(text, s); i >= 0 && i < cut {
			cut = i
		}
	}
	return text[:cut]
}
