package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
)

type rpcResp struct {
	ID     int             `json:"id"`
	Result json.RawMessage `json:"result"`
	Error  *struct {
		Message string `json:"message"`
	} `json:"error"`
}

// runMCPConfig starts the "functions" MCP server of cfgPath and performs
// the calls; it returns the text of each result, in call order.
func runMCPConfig(cfgPath string) ([]string, error) {
	data, err := os.ReadFile(cfgPath)
	if err != nil {
		return nil, err
	}
	var cfg struct {
		MCPServers map[string]struct {
			Command string   `json:"command"`
			Args    []string `json:"args"`
		} `json:"mcpServers"`
	}
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, err
	}
	srv, ok := cfg.MCPServers["functions"]
	if !ok {
		return nil, fmt.Errorf("no functions server in %s", cfgPath)
	}
	return runMCP(srv.Command, srv.Args)
}

// runMCP starts an MCP server, lists its tools and performs the calls of
// FAKE_TOOL_CALLS (default: the first tool with {"city":"Rome"}).
func runMCP(command string, args []string) ([]string, error) {
	cmd := exec.Command(command, args...)
	cmd.Stderr = os.Stderr
	in, _ := cmd.StdinPipe()
	outPipe, _ := cmd.StdoutPipe()
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	defer func() {
		in.Close()
		_ = cmd.Wait()
	}()
	dec := json.NewDecoder(outPipe)
	send := func(v any) {
		b, _ := json.Marshal(v)
		in.Write(append(b, '\n'))
	}
	read := func() (rpcResp, error) {
		var r rpcResp
		err := dec.Decode(&r)
		return r, err
	}

	send(map[string]any{"jsonrpc": "2.0", "id": 1, "method": "initialize", "params": map[string]any{"protocolVersion": "2025-06-18"}})
	if _, err := read(); err != nil {
		return nil, err
	}
	send(map[string]any{"jsonrpc": "2.0", "method": "notifications/initialized"})
	send(map[string]any{"jsonrpc": "2.0", "id": 2, "method": "tools/list"})
	listed, err := read()
	if err != nil {
		return nil, err
	}
	var list struct {
		Tools []struct {
			Name string `json:"name"`
		} `json:"tools"`
	}
	_ = json.Unmarshal(listed.Result, &list)
	if len(list.Tools) == 0 {
		return nil, fmt.Errorf("no tools listed")
	}

	type call struct {
		Name      string          `json:"name"`
		Arguments json.RawMessage `json:"arguments"`
	}
	calls := []call{{Name: list.Tools[0].Name, Arguments: json.RawMessage(`{"city":"Rome"}`)}}
	if v := os.Getenv("FAKE_TOOL_CALLS"); v != "" {
		if err := json.Unmarshal([]byte(v), &calls); err != nil {
			return nil, err
		}
	}
	results := make([]string, len(calls))
	collect := func() error {
		r, err := read()
		if err != nil {
			return err
		}
		if r.Error != nil {
			return fmt.Errorf("call failed: %s", r.Error.Message)
		}
		var res struct {
			Content []struct {
				Text string `json:"text"`
			} `json:"content"`
			IsError bool `json:"isError"`
		}
		_ = json.Unmarshal(r.Result, &res)
		text := ""
		if len(res.Content) > 0 {
			text = res.Content[0].Text
		}
		if res.IsError {
			text = "error(" + text + ")"
		}
		results[r.ID-10] = text
		return nil
	}
	params := func(i int) map[string]any {
		return map[string]any{"jsonrpc": "2.0", "id": 10 + i, "method": "tools/call",
			"params": map[string]any{"name": calls[i].Name, "arguments": calls[i].Arguments}}
	}
	if os.Getenv("FAKE_TOOL_PARALLEL") == "1" {
		for i := range calls {
			send(params(i))
		}
		for range calls {
			if err := collect(); err != nil {
				return nil, err
			}
		}
		return results, nil
	}
	for i := range calls {
		send(params(i))
		if err := collect(); err != nil {
			return nil, err
		}
	}
	return results, nil
}
