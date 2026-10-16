package server

import (
	"bytes"
	"encoding/json"
	"net/http"

	"github.com/r3n3r0/agent-bridge/internal/tools"
	"github.com/r3n3r0/agent-bridge/internal/turn"
)

// functionSet is the parsed function-calling part of a request.
type functionSet struct {
	defs     []tools.Definition
	choice   turn.ToolChoice
	parallel bool
	legacy   bool // deprecated "functions" / "function_call" API
}

// parseFunctions validates tools, tool_choice and parallel_tool_calls, or
// the deprecated functions and function_call, as the OpenAI API does.
func parseFunctions(req *chatRequest) (functionSet, *apiError) {
	fs := functionSet{parallel: true}
	invalid := func(msg string) (functionSet, *apiError) {
		return functionSet{}, newError(http.StatusBadRequest, errInvalidRequest, msg)
	}
	choice := req.ToolChoice
	choiceField := "tool_choice"
	switch {
	case len(req.Tools) > 0:
		for _, t := range req.Tools {
			if t.Type != "function" {
				return invalid("tools: only tools of type \"function\" are supported")
			}
			fs.defs = append(fs.defs, tools.Definition(t.Function))
		}
	case len(req.Functions) > 0:
		fs.legacy = true
		fs.parallel = false
		for _, f := range req.Functions {
			fs.defs = append(fs.defs, tools.Definition(f))
		}
		choice, choiceField = req.FunctionCall, "function_call"
	}
	if req.ParallelToolCalls != nil {
		fs.parallel = *req.ParallelToolCalls && !fs.legacy
	}

	seen := map[string]bool{}
	for _, d := range fs.defs {
		if !tools.ValidName(d.Name) {
			return invalid("function names must be 1-64 characters from [a-zA-Z0-9_-], got " + quote(d.Name))
		}
		if seen[d.Name] {
			return invalid("duplicate function name " + quote(d.Name))
		}
		seen[d.Name] = true
		if p := bytes.TrimSpace(d.Parameters); len(p) > 0 && !bytes.Equal(p, []byte("null")) && p[0] != '{' {
			return invalid("parameters of function " + quote(d.Name) + " must be a JSON schema object")
		}
	}

	choice = bytes.TrimSpace(choice)
	if len(choice) == 0 || bytes.Equal(choice, []byte("null")) {
		return fs, nil
	}
	if len(fs.defs) == 0 {
		return invalid(choiceField + " is only allowed when functions are specified")
	}
	var mode string
	if json.Unmarshal(choice, &mode) == nil {
		switch mode {
		case turn.ToolChoiceAuto, turn.ToolChoiceNone:
			fs.choice.Mode = mode
		case turn.ToolChoiceRequired:
			if fs.legacy {
				return invalid("function_call must be none, auto or an object naming a function")
			}
			fs.choice.Mode = mode
		default:
			return invalid(choiceField + " must be none, auto, required or an object naming a function")
		}
		return fs, nil
	}
	var named struct {
		Type     string `json:"type"`
		Name     string `json:"name"` // function_call form
		Function struct {
			Name string `json:"name"`
		} `json:"function"`
	}
	if err := json.Unmarshal(choice, &named); err != nil {
		return invalid(choiceField + " is not valid")
	}
	name := named.Function.Name
	if fs.legacy {
		name = named.Name
	}
	if !seen[name] {
		return invalid(choiceField + " names an unknown function " + quote(name))
	}
	fs.choice = turn.ToolChoice{Mode: turn.ToolChoiceFunction, Function: name}
	return fs, nil
}

func quote(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

// responseMessage builds the assistant message of a non-stream response.
func responseMessage(res *turn.Result, legacy bool) (chatMessage, string) {
	msg := chatMessage{Role: "assistant"}
	if res.Content != "" || len(res.ToolCalls) == 0 {
		content := res.Content
		msg.Content = &content
	}
	finish := res.FinishReason
	if len(res.ToolCalls) == 0 {
		return msg, finish
	}
	if legacy {
		c := res.ToolCalls[0]
		msg.FunctionCall = &functionCall{Name: c.Name, Arguments: c.Arguments}
		return msg, "function_call"
	}
	for _, c := range res.ToolCalls {
		msg.ToolCalls = append(msg.ToolCalls, toolCall{ID: c.ID, Type: "function", Function: functionCall{Name: c.Name, Arguments: c.Arguments}})
	}
	return msg, finish
}
