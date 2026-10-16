package tools

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"sync"
)

const (
	defaultProtocolVersion = "2025-06-18"
	maxMCPMessage          = 64 << 20
)

type rpcMessage struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  any             `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

type mcpTool struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	InputSchema json.RawMessage `json:"inputSchema"`
}

type textContent struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type callResult struct {
	Content []textContent `json:"content"`
	IsError bool          `json:"isError"`
}

// ServeMCP serves the client functions in spec as an MCP server over
// newline-delimited JSON-RPC on in/out, forwarding calls with client. It
// returns when in is closed or ctx is done. Calls are handled concurrently.
func ServeMCP(ctx context.Context, in io.Reader, out io.Writer, spec Spec, client *http.Client) error {
	var wmu sync.Mutex
	write := func(v any) {
		b, _ := json.Marshal(v)
		wmu.Lock()
		defer wmu.Unlock()
		_, _ = out.Write(append(b, '\n'))
	}
	reply := func(id json.RawMessage, result any) {
		write(rpcResponse{JSONRPC: "2.0", ID: id, Result: result})
	}
	fail := func(id json.RawMessage, code int, msg string) {
		write(rpcResponse{JSONRPC: "2.0", ID: id, Error: &rpcError{Code: code, Message: msg}})
	}

	tools := make([]mcpTool, 0, len(spec.Tools))
	known := map[string]bool{}
	for _, t := range spec.Tools {
		tools = append(tools, mcpTool{Name: t.Name, Description: t.Description, InputSchema: t.InputSchema()})
		known[t.Name] = true
	}

	ctx, cancel := context.WithCancel(ctx)
	var wg sync.WaitGroup
	defer wg.Wait()
	defer cancel() // runs first: pending forwards stop when input ends

	sc := bufio.NewScanner(in)
	sc.Buffer(make([]byte, 64<<10), maxMCPMessage)
	for sc.Scan() {
		var msg rpcMessage
		if err := json.Unmarshal(sc.Bytes(), &msg); err != nil {
			fail(json.RawMessage("null"), -32700, "parse error")
			continue
		}
		if len(msg.ID) == 0 {
			continue // notification
		}
		switch msg.Method {
		case "initialize":
			var p struct {
				ProtocolVersion string `json:"protocolVersion"`
			}
			_ = json.Unmarshal(msg.Params, &p)
			version := p.ProtocolVersion
			if version == "" {
				version = defaultProtocolVersion
			}
			reply(msg.ID, map[string]any{
				"protocolVersion": version,
				"capabilities":    map[string]any{"tools": map[string]any{"listChanged": false}},
				"serverInfo":      map[string]any{"name": "agent-bridge-functions", "version": "1"},
			})
		case "ping":
			reply(msg.ID, map[string]any{})
		case "tools/list":
			reply(msg.ID, map[string]any{"tools": tools})
		case "tools/call":
			var p struct {
				Name      string          `json:"name"`
				Arguments json.RawMessage `json:"arguments"`
			}
			if err := json.Unmarshal(msg.Params, &p); err != nil || !known[p.Name] {
				fail(msg.ID, -32602, "unknown tool")
				continue
			}
			if len(p.Arguments) == 0 || string(p.Arguments) == "null" {
				p.Arguments = json.RawMessage("{}")
			}
			wg.Add(1)
			go func(id json.RawMessage, name string, args json.RawMessage) {
				defer wg.Done()
				res, err := Forward(ctx, client, spec, name, args)
				if err != nil {
					res = Result{Content: err.Error(), IsError: true}
				}
				reply(id, callResult{Content: []textContent{{Type: "text", Text: res.Content}}, IsError: res.IsError})
			}(msg.ID, p.Name, p.Arguments)
		default:
			fail(msg.ID, -32601, "method not found")
		}
	}
	return sc.Err()
}
