package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/r3n3r0/agent-bridge/internal/testutil"
)

func TestDoctorCodex(t *testing.T) {
	bin, err := testutil.BuildFakeCLI(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for k, v := range map[string]string{
		"AGENT_BRIDGE_AUTH_MODE": "subscription", "AGENT_BRIDGE_CONFIG": "", "AGENT_BRIDGE_LISTEN": "",
		"CODEX_API_KEY": "", "CODEX_ACCESS_TOKEN": "", "FAKE_SCENARIO": "success", "FAKE_RECORD": "",
		"FAKE_VERSION": "codex-cli 0.170.0",
	} {
		t.Setenv(k, v)
	}
	var out, errb bytes.Buffer
	if code := run([]string{"doctor", "-live", "-backend", "codex", "-codex-bin", bin}, &out, &errb); code != 0 {
		t.Fatalf("code %d:\n%s%s", code, out.String(), errb.String())
	}
	for _, want := range []string{"[ok]   backend: codex", "[ok]   CLI version 0.170.0 (minimum 0.162.0)", "[ok]   codex login:",
		`[ok]   live check: model (configured in the CLI) answered "Fake answer."`} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("missing %q in\n%s", want, out.String())
		}
	}

	out.Reset()
	t.Setenv("FAKE_VERSION", "codex-cli 0.100.0")
	if code := run([]string{"doctor", "-backend", "codex", "-codex-bin", bin}, &out, &errb); code != 1 ||
		!strings.Contains(out.String(), "older than the minimum supported 0.162.0") {
		t.Fatalf("old version accepted: %d\n%s", code, out.String())
	}

	out.Reset()
	if code := run([]string{"check-config", "-backend", "codex", "-codex-bin", bin}, &out, &errb); code != 0 ||
		!strings.Contains(out.String(), "backend: codex") || !strings.Contains(out.String(), "sandbox: read-only") {
		t.Fatalf("check-config: %d\n%s", code, out.String())
	}
}
