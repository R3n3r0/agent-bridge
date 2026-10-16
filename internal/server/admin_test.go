package server

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/r3n3r0/agent-bridge/internal/config"
	"github.com/r3n3r0/agent-bridge/internal/login"
)

// fakeLogins records the calls of the admin API.
type fakeLogins struct {
	calls []string
	err   error
}

func (f *fakeLogins) Statuses(context.Context) []login.Status {
	yes := true
	return []login.Status{{CLI: "codex", Installed: true, LoggedIn: &yes}}
}

func (f *fakeLogins) Start(cli string) (login.FlowView, error) {
	f.calls = append(f.calls, "start "+cli)
	return login.FlowView{State: login.StateRunning, URLs: []string{"https://x.test"}}, f.err
}

func (f *fakeLogins) Input(cli, text string) error {
	f.calls = append(f.calls, "input "+cli+" "+text)
	return f.err
}

func (f *fakeLogins) Cancel(cli string) error {
	f.calls = append(f.calls, "cancel "+cli)
	return f.err
}

func adminServer(t *testing.T, logins login.Manager, key string) *httptest.Server {
	t.Helper()
	cfg := &config.Config{APIKey: key, Backend: config.BackendCodex}
	ts := httptest.NewServer(New(Options{Config: cfg, Logins: logins, Ready: func() error { return nil }}).Handler())
	t.Cleanup(ts.Close)
	return ts
}

func adminDo(t *testing.T, ts *httptest.Server, method, path, body string, headers ...string) (int, string) {
	t.Helper()
	req, _ := http.NewRequest(method, ts.URL+path, strings.NewReader(body))
	for i := 0; i+1 < len(headers); i += 2 {
		req.Header.Set(headers[i], headers[i+1])
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b)
}

func TestAdminAPI(t *testing.T) {
	f := &fakeLogins{}
	ts := adminServer(t, f, "k")
	auth := []string{"Authorization", "Bearer k", headerAdmin, "1"}

	code, body := adminDo(t, ts, "GET", "/admin", "")
	if code != 200 || !strings.Contains(body, "agent-bridge logins") || !strings.Contains(body, "/admin/assets/admin.js") {
		t.Fatalf("page %d %s", code, body)
	}
	for asset, ctype := range map[string]string{"admin.js": "javascript", "admin.css": "text/css"} {
		resp, err := http.Get(ts.URL + "/admin/assets/" + asset)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != 200 || !strings.Contains(resp.Header.Get("Content-Type"), ctype) ||
			!strings.Contains(resp.Header.Get("Content-Security-Policy"), "script-src 'self'") {
			t.Fatalf("%s: %d %v", asset, resp.StatusCode, resp.Header)
		}
	}
	for _, p := range []string{"/admin/assets/missing.js", "/admin/assets/", "/admin/assets/..%2Findex.html"} {
		if code, _ := adminDo(t, ts, "GET", p, ""); code != 404 {
			t.Errorf("%s: %d, want 404", p, code)
		}
	}
	if code, _ := adminDo(t, ts, "GET", "/admin/api/logins", ""); code != 401 {
		t.Fatalf("status without key: %d", code)
	}
	code, body = adminDo(t, ts, "GET", "/admin/api/logins", "", auth...)
	if code != 200 || !strings.Contains(body, `"backend":"codex"`) || !strings.Contains(body, `"logged_in":true`) {
		t.Fatalf("statuses %d %s", code, body)
	}
	if code, _ := adminDo(t, ts, "POST", "/admin/api/logins/codex", "", "Authorization", "Bearer k"); code != 403 {
		t.Fatalf("missing admin header: %d", code)
	}
	if code, body := adminDo(t, ts, "POST", "/admin/api/logins/codex", "", auth...); code != 200 || !strings.Contains(body, "https://x.test") {
		t.Fatalf("start %d %s", code, body)
	}
	if code, _ := adminDo(t, ts, "POST", "/admin/api/logins/claude/input", `{"text":"abc"}`, auth...); code != 204 {
		t.Fatalf("input %d", code)
	}
	if code, _ := adminDo(t, ts, "POST", "/admin/api/logins/claude/input", `nope`, auth...); code != 400 {
		t.Fatalf("bad input body %d", code)
	}
	if code, _ := adminDo(t, ts, "DELETE", "/admin/api/logins/kimi", "", auth...); code != 204 {
		t.Fatalf("cancel %d", code)
	}
	if strings.Join(f.calls, "|") != "start codex|input claude abc|cancel kimi" {
		t.Fatalf("calls %q", f.calls)
	}

	for err, want := range map[error]int{login.ErrUnknownCLI: 404, login.ErrNoFlow: 409, login.ErrBadInput: 400, io.EOF: 500} {
		f.err = err
		if code, _ := adminDo(t, ts, "DELETE", "/admin/api/logins/x", "", auth...); code != want {
			t.Errorf("%v -> %d, want %d", err, code, want)
		}
		if code, _ := adminDo(t, ts, "POST", "/admin/api/logins/x", "", auth...); code != want {
			t.Errorf("start %v -> %d, want %d", err, code, want)
		}
		if code, _ := adminDo(t, ts, "POST", "/admin/api/logins/x/input", `{"text":"a"}`, auth...); code != want {
			t.Errorf("input %v -> %d, want %d", err, code, want)
		}
	}
}

func TestAdminDisabled(t *testing.T) {
	ts := adminServer(t, nil, "")
	if code, _ := adminDo(t, ts, "GET", "/admin", ""); code != 404 {
		t.Fatalf("admin page must not exist when disabled: %d", code)
	}
}
