package server

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

// sseEvents splits a stream into comment lines and data events.
func sseEvents(t *testing.T, body io.Reader) []string {
	t.Helper()
	data, err := io.ReadAll(body)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	var block []string
	for _, line := range strings.Split(string(data), "\n") {
		switch {
		case strings.HasPrefix(line, ":"):
			if len(block) > 0 {
				t.Fatalf("comment inside an event: %q", data)
			}
			out = append(out, line)
		case line == "":
			if len(block) > 0 {
				out = append(out, strings.Join(block, "\n"))
				block = nil
			}
		default:
			block = append(block, line)
		}
	}
	if len(block) > 0 {
		t.Fatalf("unterminated event in %q", data)
	}
	return out
}

func chunkOf(t *testing.T, ev string) chatChunk {
	t.Helper()
	payload, ok := strings.CutPrefix(ev, "data: ")
	if !ok {
		t.Fatalf("not a data event: %q", ev)
	}
	var c chatChunk
	if err := json.Unmarshal([]byte(payload), &c); err != nil {
		t.Fatalf("%q: %v", payload, err)
	}
	return c
}

func streamBody(model, text string, includeUsage bool) map[string]any {
	b := userMsg(model, text)
	b["stream"] = true
	if includeUsage {
		b["stream_options"] = map[string]any{"include_usage": true}
	}
	return b
}

func TestStreamSequence(t *testing.T) {
	h := newHarness(t, harnessOpts{})
	resp := h.post(streamBody("coder", "q", true), nil)
	if ct := resp.Header.Get("Content-Type"); ct != "text/event-stream" {
		t.Fatalf("content type %q", ct)
	}
	if !strings.HasPrefix(resp.Header.Get(headerSessionID), "sess_") {
		t.Fatal("missing session header")
	}
	evs := sseEvents(t, resp.Body)
	resp.Body.Close()
	if len(evs) != 6 {
		t.Fatalf("events %q", evs)
	}
	first := chunkOf(t, evs[0])
	if first.Object != "chat.completion.chunk" || first.Choices[0].Delta.Role != "assistant" || first.Choices[0].FinishReason != nil {
		t.Fatalf("role chunk %+v", first)
	}
	if evs[1] != ": Looking at the request." {
		t.Fatalf("progress comment %q", evs[1])
	}
	content := chunkOf(t, evs[2])
	if content.Choices[0].Delta.Content != "Fake answer." || content.ID != first.ID {
		t.Fatalf("content chunk %+v", content)
	}
	fin := chunkOf(t, evs[3])
	if fin.Choices[0].FinishReason == nil || *fin.Choices[0].FinishReason != "stop" || fin.Choices[0].Delta.Content != "" || fin.Choices[0].Delta.Role != "" || len(fin.Choices[0].Delta.ToolCalls) != 0 {
		t.Fatalf("finish chunk %+v", fin)
	}
	u := chunkOf(t, evs[4])
	if len(u.Choices) != 0 || u.Usage == nil || u.Usage.TotalTokens != 20 {
		t.Fatalf("usage chunk %s", evs[4])
	}
	if evs[5] != "data: [DONE]" {
		t.Fatalf("last event %q", evs[5])
	}

	// Same final content as non-stream.
	c := decode[chatCompletion](t, h.post(userMsg("coder", "q"), nil))
	if c.Choices[0].Message.text() != content.Choices[0].Delta.Content {
		t.Fatal("stream and non-stream content differ")
	}
}

func TestStreamWithoutUsage(t *testing.T) {
	h := newHarness(t, harnessOpts{env: []string{"FAKE_SCENARIO=max_turns"}})
	resp := h.post(streamBody("coder", "q", false), nil)
	evs := sseEvents(t, resp.Body)
	resp.Body.Close()
	if len(evs) != 5 || evs[4] != "data: [DONE]" {
		t.Fatalf("events %q", evs)
	}
	if fr := chunkOf(t, evs[3]).Choices[0].FinishReason; fr == nil || *fr != "length" {
		t.Fatalf("finish %v", fr)
	}
}

func TestStreamKeepalive(t *testing.T) {
	h := newHarness(t, harnessOpts{
		env:       []string{"FAKE_SCENARIO=slow", "FAKE_SLEEP_MS=400"},
		keepalive: 50 * time.Millisecond,
	})
	resp := h.post(streamBody("coder", "q", false), nil)
	evs := sseEvents(t, resp.Body)
	resp.Body.Close()
	n := 0
	for _, e := range evs {
		if e == ": keepalive" {
			n++
		}
	}
	if n < 2 {
		t.Fatalf("keepalives %d in %q", n, evs)
	}
	if evs[len(evs)-1] != "data: [DONE]" {
		t.Fatalf("events %q", evs)
	}
}

func TestStreamProgressModes(t *testing.T) {
	h := newHarness(t, harnessOpts{configJSON: `"stream_progress": "content"`})
	resp := h.post(streamBody("coder", "q", false), nil)
	evs := sseEvents(t, resp.Body)
	resp.Body.Close()
	var text strings.Builder
	for _, e := range evs {
		if strings.HasPrefix(e, ":") {
			t.Fatalf("comment in content mode: %q", e)
		}
		if e == "data: [DONE]" {
			continue
		}
		text.WriteString(chunkOf(t, e).Choices[0].Delta.Content)
	}
	if text.String() != "Looking at the request.\n\nFake answer." {
		t.Fatalf("content %q", text.String())
	}

	h = newHarness(t, harnessOpts{configJSON: `"stream_progress": "none"`})
	resp = h.post(streamBody("coder", "q", false), nil)
	evs = sseEvents(t, resp.Body)
	resp.Body.Close()
	if len(evs) != 4 {
		t.Fatalf("events %q", evs)
	}
}

func TestStreamError(t *testing.T) {
	h := newHarness(t, harnessOpts{env: []string{"FAKE_SCENARIO=no_result"}})
	resp := h.post(streamBody("coder", "q", true), nil)
	if resp.StatusCode != 200 {
		t.Fatalf("status %d", resp.StatusCode)
	}
	evs := sseEvents(t, resp.Body)
	resp.Body.Close()
	if len(evs) != 3 || evs[2] != "data: [DONE]" {
		t.Fatalf("events %q", evs)
	}
	var e errorBody
	if err := json.Unmarshal([]byte(strings.TrimPrefix(evs[1], "data: ")), &e); err != nil || e.Error.Code != errUpstream {
		t.Fatalf("error event %q", evs[1])
	}
	if strings.Contains(evs[1], "sk-ant-") {
		t.Fatal("secret in stream error")
	}
}

func TestOneLine(t *testing.T) {
	if got := oneLine("a\n  b\r\n\tc", 300); got != "a b c" {
		t.Fatalf("got %q", got)
	}
	if got := oneLine(strings.Repeat("é", 400), 300); len([]rune(got)) != 300 {
		t.Fatalf("len %d", len([]rune(got)))
	}
}

func TestStreamClientDisconnectStopsProcess(t *testing.T) {
	h := newHarness(t, harnessOpts{env: []string{"FAKE_SCENARIO=slow", "FAKE_SLEEP_MS=60000"}})
	data, _ := json.Marshal(streamBody("coder", "q", false))
	ctx, cancel := context.WithCancel(context.Background())
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, h.ts.URL+"/v1/chat/completions", strings.NewReader(string(data)))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	// Read the role chunk, then go away.
	if _, err := bufio.NewReader(resp.Body).ReadString('\n'); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return len(h.starts()) == 1 })
	start := time.Now()
	cancel()
	resp.Body.Close()
	pid := h.starts()[0].PID
	waitFor(t, func() bool { return h.limiter.Active() == 0 })
	if time.Since(start) > 10*time.Second {
		t.Fatalf("process stopped after %s", time.Since(start))
	}
	if !processGone(pid) {
		t.Fatalf("process %d still running", pid)
	}
	waitFor(t, func() bool { return strings.Contains(h.logs.String(), "outcome=canceled") })
}
