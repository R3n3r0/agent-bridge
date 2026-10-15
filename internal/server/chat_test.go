package server

import (
	"encoding/base64"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestChatNonStream(t *testing.T) {
	h := newHarness(t, harnessOpts{})
	resp := h.post(userMsg("coder", "hello"), nil)
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("status %d: %s", resp.StatusCode, body)
	}
	if !strings.HasPrefix(resp.Header.Get(headerSessionID), "sess_") || resp.Header.Get(headerTurns) != "2" ||
		resp.Header.Get(headerCost) != "0.012300" || resp.Header.Get(headerDuration) != "42" ||
		resp.Header.Get(headerRequestID) == "" {
		t.Fatalf("headers %v", resp.Header)
	}
	c := decode[chatCompletion](t, resp)
	if !strings.HasPrefix(c.ID, "chatcmpl-") || len(c.ID) != len("chatcmpl-")+24 || c.Object != "chat.completion" ||
		c.Model != "coder" || c.Created == 0 {
		t.Fatalf("completion %+v", c)
	}
	if len(c.Choices) != 1 || c.Choices[0].Message.Content != "Fake answer." || c.Choices[0].Message.Role != "assistant" ||
		c.Choices[0].FinishReason != "stop" {
		t.Fatalf("choices %+v", c.Choices)
	}
	if c.Usage != (usage{PromptTokens: 15, CompletionTokens: 5, TotalTokens: 20}) {
		t.Fatalf("usage %+v", c.Usage)
	}

	rec := h.starts()
	if len(rec) != 1 {
		t.Fatalf("records %d", len(rec))
	}
	r := rec[0]
	if r.Stdin != "hello" {
		t.Fatalf("stdin %q", r.Stdin)
	}
	if v, _ := argValue(r.Args, "--append-system-prompt"); v != "agent prompt" {
		t.Fatalf("args %q", r.Args)
	}
	if v, _ := argValue(r.Args, "--allowedTools"); v != "Read,Edit,Bash" {
		t.Fatalf("args %q", r.Args)
	}
	if _, ok := argValue(r.Args, "--resume"); ok {
		t.Fatal("first turn resumed")
	}
	if filepath.Base(r.Cwd) != "work" {
		t.Fatalf("cwd %s", r.Cwd)
	}
	for _, kv := range r.Env {
		if strings.HasPrefix(kv, "AGENT_BRIDGE_") || strings.HasPrefix(kv, "ANTHROPIC_API_KEY=") {
			t.Fatalf("child environment leaks %s", kv)
		}
	}
	if !strings.Contains(h.logs.String(), "outcome=success") {
		t.Fatalf("no end-of-turn log: %s", h.logs.String())
	}
	if strings.Contains(h.logs.String(), "sk-ant-should-not-leak") {
		t.Fatal("secret in logs")
	}
}

func TestChatAPIModeKeepsKey(t *testing.T) {
	h := newHarness(t, harnessOpts{authMode: "api"})
	resp := h.post(userMsg("chat", "hi"), nil)
	resp.Body.Close()
	found := false
	for _, kv := range h.starts()[0].Env {
		if strings.HasPrefix(kv, "AGENT_BRIDGE_") {
			t.Fatalf("leak %s", kv)
		}
		found = found || strings.HasPrefix(kv, "ANTHROPIC_API_KEY=")
	}
	if !found {
		t.Fatal("api mode must keep the upstream key")
	}
}

func TestChatOnlyAgentArgs(t *testing.T) {
	h := newHarness(t, harnessOpts{})
	resp := h.post(userMsg("claude-code/haiku", "read /etc/passwd"), nil)
	if resp.StatusCode != 200 {
		t.Fatalf("status %d", resp.StatusCode)
	}
	resp.Body.Close()
	r := h.starts()[0]
	if v, ok := argValue(r.Args, "--tools"); !ok || v != "" {
		t.Fatalf("args %q", r.Args)
	}
	if v, _ := argValue(r.Args, "--model"); v != "haiku" {
		t.Fatalf("args %q", r.Args)
	}
	if filepath.Base(r.Cwd) != "scratch" {
		t.Fatalf("cwd %s", r.Cwd)
	}
	if strings.Contains(h.logs.String(), "tool use") {
		t.Fatal("tool_use in chat-only mode")
	}
}

func TestRequestValidation(t *testing.T) {
	h := newHarness(t, harnessOpts{})
	cases := []struct {
		name    string
		body    any
		headers map[string]string
		status  int
		code    string
	}{
		{"bad json", "{", nil, 400, errInvalidRequest},
		{"no model", map[string]any{"messages": []map[string]any{{"role": "user", "content": "x"}}}, nil, 400, errInvalidRequest},
		{"empty messages", map[string]any{"model": "coder", "messages": []any{}}, nil, 400, errInvalidRequest},
		{"no user", map[string]any{"model": "coder", "messages": []map[string]any{{"role": "system", "content": "x"}}}, nil, 400, errInvalidRequest},
		{"ends with assistant", map[string]any{"model": "coder", "messages": []map[string]any{
			{"role": "user", "content": "x"}, {"role": "assistant", "content": "y"}}}, nil, 400, errInvalidRequest},
		{"bad role", map[string]any{"model": "coder", "messages": []map[string]any{{"role": "bot", "content": "x"}}}, nil, 400, errInvalidRequest},
		{"image", map[string]any{"model": "coder", "messages": []map[string]any{{"role": "user", "content": []map[string]any{
			{"type": "image_url", "image_url": map[string]string{"url": "data:"}}}}}}, nil, 400, errUnsupported},
		{"unknown model", userMsg("nope", "x"), nil, 404, errModelNotFound},
		{"bad session id", userMsg("coder", "x"), map[string]string{headerSessionID: "has space"}, 400, errInvalidRequest},
		{"bad reset", userMsg("coder", "x"), map[string]string{headerSessionReset: "yes"}, 400, errInvalidRequest},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			expectError(t, h.post(c.body, c.headers), c.status, c.code)
		})
	}
	big := `{"model":"coder","messages":[{"role":"user","content":"` + strings.Repeat("x", maxBodyBytes) + `"}]}`
	expectError(t, h.post(big, nil), 400, errInvalidRequest)
	if len(h.starts()) != 0 {
		t.Fatal("process started for an invalid request")
	}
}

func TestIgnoredOpenAIFields(t *testing.T) {
	h := newHarness(t, harnessOpts{})
	body := map[string]any{
		"model": "chat", "temperature": 0.2, "top_p": 1, "n": 1, "tools": []any{map[string]any{"type": "function"}},
		"tool_choice": "auto", "response_format": map[string]any{"type": "json_object"}, "max_tokens": 10,
		"max_completion_tokens": 10, "logprobs": true,
		"messages": []map[string]any{{"role": "user", "content": []map[string]any{{"type": "text", "text": "hi"}}}},
	}
	resp := h.post(body, nil)
	if resp.StatusCode != 200 {
		t.Fatalf("status %d", resp.StatusCode)
	}
	resp.Body.Close()
}

func TestOutcomes(t *testing.T) {
	cases := []struct {
		scenario string
		status   int
		code     string
	}{
		{"exec_error", 502, errUpstream},
		{"no_result", 502, errUpstream},
	}
	for _, c := range cases {
		t.Run(c.scenario, func(t *testing.T) {
			h := newHarness(t, harnessOpts{env: []string{"FAKE_SCENARIO=" + c.scenario}})
			e := expectError(t, h.post(userMsg("coder", "x"), nil), c.status, c.code)
			if strings.Contains(e.Message, "sk-ant-") {
				t.Fatalf("secret in error: %s", e.Message)
			}
			if c.scenario == "no_result" && !strings.Contains(e.Message, "something broke") {
				t.Fatalf("stderr missing: %s", e.Message)
			}
		})
	}

	h := newHarness(t, harnessOpts{env: []string{"FAKE_SCENARIO=max_turns"}})
	c := decode[chatCompletion](t, h.post(userMsg("coder", "x"), nil))
	if c.Choices[0].FinishReason != "length" || c.Choices[0].Message.Content != "Partial answer." {
		t.Fatalf("max_turns: %+v", c.Choices)
	}
}

func TestTimeout(t *testing.T) {
	h := newHarness(t, harnessOpts{
		env:   []string{"FAKE_SCENARIO=slow", "FAKE_SLEEP_MS=20000"},
		extra: func(string) string { return `"quick": {"tools_enabled": false, "timeout_seconds": 1}` },
	})
	start := time.Now()
	expectError(t, h.post(userMsg("quick", "x"), nil), 504, errTimeout)
	if time.Since(start) > 10*time.Second {
		t.Fatalf("took %s", time.Since(start))
	}
	snap := h.stats.Snapshot(false)
	if snap.Agents["quick"].Timeouts != 1 {
		t.Fatalf("stats %+v", snap.Agents["quick"])
	}
}

func TestSessionResume(t *testing.T) {
	h := newHarness(t, harnessOpts{})
	hdr := map[string]string{headerSessionID: "conv-1"}
	resp := h.post(userMsg("coder", "first question"), hdr)
	if resp.Header.Get(headerSessionID) != "conv-1" {
		t.Fatalf("session header %q", resp.Header.Get(headerSessionID))
	}
	resp.Body.Close()
	first := h.starts()[0]

	body := map[string]any{"model": "coder", "messages": []map[string]any{
		{"role": "system", "content": "rules"},
		{"role": "user", "content": "first question"},
		{"role": "assistant", "content": "Fake answer."},
		{"role": "user", "content": "second question"},
		{"role": "user", "content": "and a third"},
	}}
	resp = h.post(body, hdr)
	if resp.StatusCode != 200 {
		t.Fatalf("status %d", resp.StatusCode)
	}
	resp.Body.Close()
	starts := h.starts()
	second := starts[1]
	entry, _ := h.reg.Get("coder|conv-1")
	firstID := entry.CLISessionID
	if v, ok := argValue(second.Args, "--resume"); !ok || v != firstID || v == "" {
		t.Fatalf("resume arg %q, want %q (first args %q)", v, firstID, first.Args)
	}
	if second.Stdin != "second question\n\nand a third" {
		t.Fatalf("stdin %q", second.Stdin)
	}
	if entry.Turns != 2 {
		t.Fatalf("entry %+v", entry)
	}

	// Same key on another agent is a different session.
	resp = h.post(userMsg("chat", "hi"), hdr)
	resp.Body.Close()
	if _, ok := argValue(h.starts()[2].Args, "--resume"); ok {
		t.Fatal("session shared across agents")
	}

	// Reset starts over.
	resp = h.post(userMsg("coder", "again"), map[string]string{headerSessionID: "conv-1", headerSessionReset: "true"})
	resp.Body.Close()
	if _, ok := argValue(h.starts()[3].Args, "--resume"); ok {
		t.Fatal("reset session resumed")
	}
}

func TestSessionLatestIDAndUserKey(t *testing.T) {
	h := newHarness(t, harnessOpts{env: []string{"FAKE_SCENARIO=new_session_id"}})
	body := userMsg("coder", "q")
	body["user"] = "alice@example.com"
	resp := h.post(body, nil)
	key := resp.Header.Get(headerSessionID)
	resp.Body.Close()
	if !strings.HasPrefix(key, "user_") {
		t.Fatalf("key %q", key)
	}
	e1, _ := h.reg.Get("coder|" + key)
	body["messages"] = []map[string]any{{"role": "user", "content": "q"}, {"role": "assistant", "content": "a"}, {"role": "user", "content": "q2"}}
	resp = h.post(body, nil)
	resp.Body.Close()
	if v, _ := argValue(h.starts()[1].Args, "--resume"); v != e1.CLISessionID {
		t.Fatalf("resume %q want %q", v, e1.CLISessionID)
	}
	e2, _ := h.reg.Get("coder|" + key)
	if e2.CLISessionID == e1.CLISessionID {
		t.Fatal("new session id from result not stored")
	}

	body["user"] = "bob"
	resp = h.post(body, nil)
	if resp.Header.Get(headerSessionID) != "bob" {
		t.Fatalf("key %q", resp.Header.Get(headerSessionID))
	}
	resp.Body.Close()
}

func TestResumeMissingRetries(t *testing.T) {
	h := newHarness(t, harnessOpts{env: []string{"FAKE_SCENARIO=resume_missing"}})
	_ = h.reg.Put("coder|k", sessionEntry("gone-id", "coder", h.cfg.Agents["coder"].Cwd))
	body := map[string]any{"model": "coder", "messages": []map[string]any{
		{"role": "user", "content": "old"}, {"role": "assistant", "content": "reply"}, {"role": "user", "content": "new"},
	}}
	resp := h.post(body, map[string]string{headerSessionID: "k"})
	if resp.StatusCode != 200 {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("status %d: %s", resp.StatusCode, b)
	}
	resp.Body.Close()
	starts := h.starts()
	if len(starts) != 2 {
		t.Fatalf("want 2 runs, got %d", len(starts))
	}
	if v, _ := argValue(starts[0].Args, "--resume"); v != "gone-id" {
		t.Fatalf("first run %q", starts[0].Args)
	}
	if _, ok := argValue(starts[1].Args, "--resume"); ok {
		t.Fatal("retry resumed")
	}
	if !strings.Contains(starts[1].Stdin, "<conversation_history>") || !strings.Contains(starts[1].Stdin, "<current_request>\nnew") {
		t.Fatalf("retry prompt %q", starts[1].Stdin)
	}
	e, _ := h.reg.Get("coder|k")
	if e.CLISessionID == "gone-id" || e.CLISessionID == "" {
		t.Fatalf("entry %+v", e)
	}
	if !strings.Contains(h.logs.String(), "level=WARN") {
		t.Fatal("no warning logged")
	}
}

func TestCwdChangeStartsNewSession(t *testing.T) {
	h := newHarness(t, harnessOpts{
		configJSON: `"overrides": {"allowed_roots": ["work"]}`,
	})
	sub := filepath.Join(h.dir, "work", "sub")
	if err := os.Mkdir(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	hdr := map[string]string{headerSessionID: "k"}
	resp := h.post(userMsg("coder", "q"), hdr)
	resp.Body.Close()
	hdr[headerAgentCwd] = sub
	body := map[string]any{"model": "coder", "messages": []map[string]any{
		{"role": "user", "content": "q"}, {"role": "assistant", "content": "a"}, {"role": "user", "content": "q2"},
	}}
	resp = h.post(body, hdr)
	if resp.StatusCode != 200 {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("status %d: %s", resp.StatusCode, b)
	}
	resp.Body.Close()
	second := h.starts()[1]
	if _, ok := argValue(second.Args, "--resume"); ok {
		t.Fatal("resumed across working directories")
	}
	if want, _ := filepath.EvalSymlinks(sub); second.Cwd != want {
		t.Fatalf("cwd %s, want %s", second.Cwd, want)
	}
}

func TestSameSessionNeverParallel(t *testing.T) {
	h := newHarness(t, harnessOpts{
		env:        []string{"FAKE_SCENARIO=slow", "FAKE_SLEEP_MS=300"},
		configJSON: `"max_concurrent": 4`,
	})
	var wg sync.WaitGroup
	for i := 0; i < 3; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			resp := h.post(userMsg("coder", "q"), map[string]string{headerSessionID: "shared"})
			resp.Body.Close()
			if resp.StatusCode != 200 {
				t.Errorf("status %d", resp.StatusCode)
			}
		}()
	}
	wg.Wait()
	recs := h.records()
	running := 0
	for _, r := range recs {
		if r.Phase == "start" {
			running++
		} else {
			running--
		}
		if running > 1 {
			t.Fatalf("two processes on the same session at once: %+v", recs)
		}
	}
	if len(recs) != 6 {
		t.Fatalf("records %d", len(recs))
	}
}

func TestOnBusyReject(t *testing.T) {
	h := newHarness(t, harnessOpts{
		env:        []string{"FAKE_SCENARIO=slow", "FAKE_SLEEP_MS=1500"},
		configJSON: `"sessions": {"on_busy": "reject"}`,
	})
	done := make(chan struct{})
	go func() {
		resp := h.post(userMsg("coder", "q"), map[string]string{headerSessionID: "s"})
		resp.Body.Close()
		close(done)
	}()
	waitFor(t, func() bool { return len(h.starts()) == 1 })
	expectError(t, h.post(userMsg("coder", "q"), map[string]string{headerSessionID: "s"}), 409, errSessionBusy)
	<-done
}

func TestQueueFull(t *testing.T) {
	h := newHarness(t, harnessOpts{
		env:        []string{"FAKE_SCENARIO=slow", "FAKE_SLEEP_MS=1500"},
		configJSON: `"max_concurrent": 1, "max_queue": 1`,
	})
	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		i := i
		go func() {
			defer wg.Done()
			resp := h.post(userMsg("coder", "q"), map[string]string{headerSessionID: "s" + string(rune('a'+i))})
			resp.Body.Close()
		}()
		if i == 0 {
			waitFor(t, func() bool { return h.limiter.Active() == 1 })
		}
	}
	waitFor(t, func() bool { return h.limiter.Queued() == 1 })
	resp := h.post(userMsg("coder", "q"), map[string]string{headerSessionID: "sc"})
	if resp.Header.Get("Retry-After") != "30" {
		t.Fatalf("Retry-After %q", resp.Header.Get("Retry-After"))
	}
	expectError(t, resp, 429, errQueueFull)
	wg.Wait()
}

func TestOverrides(t *testing.T) {
	h := newHarness(t, harnessOpts{
		configJSON: `"overrides": {"allowed_roots": ["work"], "allow_tools": true, "allow_system_prompt": true}`,
	})
	outside := t.TempDir()
	escape := filepath.Join(h.dir, "work", "escape")
	symlinkOK := os.Symlink(outside, escape) == nil
	b64 := func(s string) string { return base64.StdEncoding.EncodeToString([]byte(s)) }

	forbidden := []map[string]string{
		{headerAgentCwd: outside},
		{headerAgentCwd: "work"},
		{headerAgentCwd: filepath.Join(h.dir, "work", "missing")},
		{headerAgentTools: "Read,Write"},
		{headerAgentTools: " , "},
		{headerAgentPrompt: b64(strings.Repeat("x", maxSystemPromptOverride+1))},
	}
	if symlinkOK {
		forbidden = append(forbidden, map[string]string{headerAgentCwd: escape})
	}
	for _, hdr := range forbidden {
		expectError(t, h.post(userMsg("coder", "q"), hdr), 403, errOverride)
	}
	expectError(t, h.post(userMsg("coder", "q"), map[string]string{headerAgentPrompt: "!!!"}), 400, errInvalidRequest)
	expectError(t, h.post(userMsg("claude-code/sonnet", "q"), map[string]string{headerAgentTools: "Read"}), 403, errOverride)

	resp := h.post(userMsg("coder", "q"), map[string]string{
		headerAgentCwd:    filepath.Join(h.dir, "work"),
		headerAgentTools:  "Read, Edit",
		headerAgentPrompt: b64("extra rules"),
	})
	if resp.StatusCode != 200 {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("status %d: %s", resp.StatusCode, b)
	}
	resp.Body.Close()
	r := h.starts()[0]
	if v, _ := argValue(r.Args, "--allowedTools"); v != "Read,Edit" {
		t.Fatalf("tools %q", r.Args)
	}
	if v, _ := argValue(r.Args, "--append-system-prompt"); v != "agent prompt\n\nextra rules" {
		t.Fatalf("prompt %q", v)
	}
}

func TestOverridesDisabled(t *testing.T) {
	h := newHarness(t, harnessOpts{})
	for _, hdr := range []map[string]string{
		{headerAgentCwd: h.dir},
		{headerAgentTools: "Read"},
		{headerAgentPrompt: "eA=="},
	} {
		expectError(t, h.post(userMsg("coder", "q"), hdr), 403, errOverride)
	}
}

func TestSubscriptionWithAPIKeySourceLogsError(t *testing.T) {
	h := newHarness(t, harnessOpts{env: []string{"FAKE_KEY_SOURCE=ANTHROPIC_API_KEY"}})
	resp := h.post(userMsg("chat", "q"), nil)
	resp.Body.Close()
	if !strings.Contains(h.logs.String(), "level=ERROR") {
		t.Fatalf("no error logged: %s", h.logs.String())
	}
}

func TestGarbageOutputTolerated(t *testing.T) {
	h := newHarness(t, harnessOpts{env: []string{"FAKE_SCENARIO=garbage"}})
	c := decode[chatCompletion](t, h.post(userMsg("coder", "q"), nil))
	if c.Choices[0].Message.Content != "Fake answer." {
		t.Fatalf("content %q", c.Choices[0].Message.Content)
	}
}

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatal("condition not met in time")
		}
		time.Sleep(5 * time.Millisecond)
	}
}
