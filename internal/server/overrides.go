package server

import (
	"encoding/base64"
	"fmt"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/r3n3r0/agent-bridge/internal/config"
	"github.com/r3n3r0/agent-bridge/internal/runner"
)

const maxSystemPromptOverride = 16 << 10

// applyOverrides applies the X-Agent-* headers within the limits of the
// overrides section and returns the effective spec and working directory.
func applyOverrides(ov config.OverridesSection, h http.Header, a *config.Agent) (runner.Spec, string, *apiError) {
	spec := runner.SpecFromAgent(a)
	cwd := a.Cwd
	hCwd, hTools, hPrompt := h.Get(headerAgentCwd), h.Get(headerAgentTools), h.Get(headerAgentPrompt)
	if hCwd == "" && hTools == "" && hPrompt == "" {
		return spec, cwd, nil
	}
	forbid := func(msg string) (runner.Spec, string, *apiError) {
		return runner.Spec{}, "", newError(http.StatusForbidden, errOverride, msg)
	}
	if a.Builtin {
		return forbid("per-request overrides are not available for built-in models")
	}

	if hCwd != "" {
		if len(ov.AllowedRoots) == 0 {
			return forbid(headerAgentCwd + " is not enabled")
		}
		if !filepath.IsAbs(hCwd) {
			return forbid(headerAgentCwd + " must be an absolute path")
		}
		p, err := config.CanonicalDir(hCwd)
		if err != nil || !insideAny(p, ov.AllowedRoots) {
			return forbid(headerAgentCwd + " is not an existing directory inside an allowed root")
		}
		cwd = p
	}

	if hTools != "" {
		if !ov.AllowTools {
			return forbid(headerAgentTools + " is not enabled")
		}
		agentTools := map[string]bool{}
		for _, t := range a.AllowedTools {
			agentTools[t] = true
		}
		var tools []string
		for _, t := range strings.Split(hTools, ",") {
			t = strings.TrimSpace(t)
			if t == "" {
				continue
			}
			if !agentTools[t] {
				return forbid(headerAgentTools + " can only restrict the agent's allowed_tools; " + strconv.Quote(t) + " is not among them")
			}
			tools = append(tools, t)
		}
		if len(tools) == 0 {
			return forbid(headerAgentTools + " must list at least one tool")
		}
		spec.AllowedTools = tools
	}

	if hPrompt != "" {
		if !ov.AllowSystemPrompt {
			return forbid(headerAgentPrompt + " is not enabled")
		}
		dec, err := base64.StdEncoding.DecodeString(hPrompt)
		if err != nil {
			dec, err = base64.RawStdEncoding.DecodeString(hPrompt)
		}
		if err != nil {
			return runner.Spec{}, "", newError(http.StatusBadRequest, errInvalidRequest, headerAgentPrompt+" must be base64 encoded")
		}
		if len(dec) > maxSystemPromptOverride {
			return forbid(fmt.Sprintf("%s exceeds %d bytes", headerAgentPrompt, maxSystemPromptOverride))
		}
		if spec.SystemPrompt == "" {
			spec.SystemPrompt = string(dec)
		} else {
			spec.SystemPrompt += "\n\n" + string(dec)
		}
	}
	return spec, cwd, nil
}

func insideAny(p string, roots []string) bool {
	for _, root := range roots {
		rel, err := filepath.Rel(root, p)
		if err != nil {
			continue
		}
		if rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel)) {
			return true
		}
	}
	return false
}
