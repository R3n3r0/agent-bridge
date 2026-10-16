package tools

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type echoHandler struct{ block chan struct{} }

func (h echoHandler) Call(ctx context.Context, name string, args []byte) (Result, error) {
	if h.block != nil {
		select {
		case <-h.block:
		case <-ctx.Done():
			return Result{}, ctx.Err()
		}
	}
	if name == "fail" {
		return Result{}, ErrClosed
	}
	return Result{Content: name + ":" + string(args), IsError: name == "bad"}, nil
}

func TestNamesAndSchema(t *testing.T) {
	if !ValidName("get_weather-2") || ValidName("bad name") || ValidName("") || ValidName(strings.Repeat("a", 65)) {
		t.Fatal("ValidName")
	}
	if string(Definition{}.InputSchema()) != `{"type":"object","properties":{}}` {
		t.Fatal("default schema")
	}
	if string(Definition{Parameters: json.RawMessage(`{"type":"object"}`)}.InputSchema()) != `{"type":"object"}` {
		t.Fatal("schema")
	}
	if ToolPrefix != "mcp__functions__" {
		t.Fatal(ToolPrefix)
	}
}

func TestWriteAndReadFiles(t *testing.T) {
	dir := t.TempDir()
	spec := Spec{Endpoint: "http://127.0.0.1:1", Token: "tok", Tools: []Definition{{Name: "f"}}}
	cfg, err := WriteFiles(dir, spec, []string{"/bin/bridge", "mcp-tools"})
	if err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(cfg)
	if !strings.Contains(string(data), `"functions":{"args":["mcp-tools","-spec","`) {
		t.Fatalf("config %s", data)
	}
	got, err := ReadSpec(filepath.Join(dir, "functions.json"))
	if err != nil || got.Token != "tok" || got.Tools[0].Name != "f" {
		t.Fatalf("spec %+v %v", got, err)
	}
	if fi, _ := os.Stat(filepath.Join(dir, "functions.json")); fi.Mode().Perm()&0o077 != 0 && filepath.Separator == '/' {
		t.Fatalf("spec file mode %v", fi.Mode())
	}
	if _, err := WriteFiles(dir, spec, nil); err == nil {
		t.Fatal("missing helper accepted")
	}
	if _, err := ReadSpec(filepath.Join(dir, "missing")); err == nil {
		t.Fatal("missing spec accepted")
	}
	_ = os.WriteFile(filepath.Join(dir, "bad.json"), []byte("{"), 0o600)
	if _, err := ReadSpec(filepath.Join(dir, "bad.json")); err == nil {
		t.Fatal("bad spec accepted")
	}
	if _, err := WriteFiles(filepath.Join(dir, "missing"), spec, []string{"x"}); err == nil {
		t.Fatal("missing dir accepted")
	}
}

func TestBroker(t *testing.T) {
	b, err := NewHTTPBroker()
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	reg, err := b.Register(echoHandler{})
	if err != nil {
		t.Fatal(err)
	}
	spec := Spec{Endpoint: reg.Endpoint, Token: reg.Token}
	ctx := context.Background()
	res, err := Forward(ctx, http.DefaultClient, spec, "f", json.RawMessage(`{"a":1}`))
	if err != nil || res.Content != `f:{"a":1}` || res.IsError {
		t.Fatalf("forward %+v %v", res, err)
	}
	if res, _ := Forward(ctx, http.DefaultClient, spec, "bad", json.RawMessage(`{}`)); !res.IsError {
		t.Fatal("error flag lost")
	}
	if _, err := Forward(ctx, http.DefaultClient, spec, "fail", json.RawMessage(`{}`)); err == nil || !strings.Contains(err.Error(), "ended") {
		t.Fatalf("handler error %v", err)
	}
	if _, err := Forward(ctx, http.DefaultClient, Spec{Endpoint: reg.Endpoint, Token: "wrong"}, "f", nil); err == nil {
		t.Fatal("wrong token accepted")
	}
	resp, _ := http.Post(reg.Endpoint+"/call", "application/json", strings.NewReader("{"))
	resp.Body.Close()
	reg.Unregister()
	reg.Unregister()
	if _, err := Forward(ctx, http.DefaultClient, spec, "f", nil); err == nil {
		t.Fatal("call after unregister accepted")
	}
	if _, err := Forward(ctx, http.DefaultClient, Spec{Endpoint: "http://127.0.0.1:1"}, "f", nil); err == nil {
		t.Fatal("unreachable broker")
	}
}

// mcpSession runs ServeMCP over pipes and returns a request/response helper.
func mcpSession(t *testing.T, spec Spec) (func(string) map[string]any, func()) {
	t.Helper()
	inR, inW := io.Pipe()
	outR, outW := io.Pipe()
	done := make(chan error, 1)
	go func() { done <- ServeMCP(context.Background(), inR, outW, spec, http.DefaultClient); outW.Close() }()
	sc := bufio.NewScanner(outR)
	send := func(line string) map[string]any {
		if _, err := io.WriteString(inW, line+"\n"); err != nil {
			t.Fatal(err)
		}
		if strings.Contains(line, `"id"`) || !strings.Contains(line, "jsonrpc") {
			if !sc.Scan() {
				t.Fatal("no response")
			}
			var m map[string]any
			_ = json.Unmarshal(sc.Bytes(), &m)
			return m
		}
		return nil
	}
	return send, func() {
		inW.Close()
		select {
		case err := <-done:
			if err != nil {
				t.Fatal(err)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("ServeMCP did not stop")
		}
	}
}

func TestServeMCP(t *testing.T) {
	b, _ := NewHTTPBroker()
	defer b.Close()
	reg, _ := b.Register(echoHandler{})
	spec := Spec{Endpoint: reg.Endpoint, Token: reg.Token, Tools: []Definition{{Name: "f", Description: "d"}, {Name: "bad"}}}
	send, stop := mcpSession(t, spec)
	defer stop()

	init := send(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-03-26"}}`)
	r := init["result"].(map[string]any)
	if r["protocolVersion"] != "2025-03-26" || r["capabilities"].(map[string]any)["tools"] == nil {
		t.Fatalf("initialize %v", init)
	}
	if init := send(`{"jsonrpc":"2.0","id":"x","method":"initialize","params":{}}`); init["result"].(map[string]any)["protocolVersion"] != defaultProtocolVersion {
		t.Fatalf("default version %v", init)
	}
	send(`{"jsonrpc":"2.0","method":"notifications/initialized"}`)
	list := send(`{"jsonrpc":"2.0","id":2,"method":"tools/list"}`)
	toolsList := list["result"].(map[string]any)["tools"].([]any)
	first := toolsList[0].(map[string]any)
	if len(toolsList) != 2 || first["name"] != "f" || first["inputSchema"] == nil || first["description"] != "d" {
		t.Fatalf("list %v", list)
	}
	call := send(`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"f","arguments":{"x":1}}}`)
	content := call["result"].(map[string]any)["content"].([]any)[0].(map[string]any)
	if content["text"] != `f:{"x":1}` || call["result"].(map[string]any)["isError"] != false {
		t.Fatalf("call %v", call)
	}
	call = send(`{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"f"}}`)
	if call["result"].(map[string]any)["content"].([]any)[0].(map[string]any)["text"] != "f:{}" {
		t.Fatalf("call without arguments %v", call)
	}
	if call := send(`{"jsonrpc":"2.0","id":5,"method":"tools/call","params":{"name":"bad"}}`); call["result"].(map[string]any)["isError"] != true {
		t.Fatalf("error result %v", call)
	}
	if e := send(`{"jsonrpc":"2.0","id":6,"method":"tools/call","params":{"name":"nope"}}`); e["error"] == nil {
		t.Fatalf("unknown tool %v", e)
	}
	if e := send(`{"jsonrpc":"2.0","id":7,"method":"resources/list"}`); e["error"].(map[string]any)["code"] != float64(-32601) {
		t.Fatalf("unknown method %v", e)
	}
	if p := send(`{"jsonrpc":"2.0","id":8,"method":"ping"}`); p["result"] == nil {
		t.Fatalf("ping %v", p)
	}
	if e := send(`not json`); e["error"].(map[string]any)["code"] != float64(-32700) {
		t.Fatalf("parse error %v", e)
	}
	reg.Unregister()
	call = send(`{"jsonrpc":"2.0","id":9,"method":"tools/call","params":{"name":"f"}}`)
	if call["result"].(map[string]any)["isError"] != true {
		t.Fatalf("call after turn end %v", call)
	}
}

func TestServeMCPStopsPendingCalls(t *testing.T) {
	b, _ := NewHTTPBroker()
	defer b.Close()
	reg, _ := b.Register(echoHandler{block: make(chan struct{})})
	inR, inW := io.Pipe()
	done := make(chan error, 1)
	go func() {
		done <- ServeMCP(context.Background(), inR, io.Discard, Spec{Endpoint: reg.Endpoint, Token: reg.Token, Tools: []Definition{{Name: "f"}}}, http.DefaultClient)
	}()
	_, _ = io.WriteString(inW, `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"f"}}`+"\n")
	time.Sleep(50 * time.Millisecond)
	inW.Close()
	select {
	case err := <-done:
		if err != nil && !errors.Is(err, io.EOF) {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("pending call kept ServeMCP alive after input closed")
	}
}
