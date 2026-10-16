package login

import (
	"encoding/json"
	"strings"

	"github.com/r3n3r0/agent-bridge/internal/config"
)

// CLI describes how to log in to one CLI and how to read its login state.
type CLI struct {
	Name       string
	Binary     string
	LoginArgs  []string
	StatusArgs []string
	// NeedsTerminal runs the login in a pseudo-terminal: the CLI waits for
	// a pasted code only when attached to one.
	NeedsTerminal bool
	// AcceptsInput reports whether the flow may ask for a pasted code.
	AcceptsInput bool
	// parseStatus reads the output and outcome of the status command.
	parseStatus func(out string, err error) (*bool, string)
}

// CLIs returns the login definitions of the supported CLIs, with the
// binaries of cfg.
func CLIs(cfg *config.Config) []CLI {
	return []CLI{
		{
			Name: config.BackendDefault, Binary: cfg.DefaultCLIBinary, LoginArgs: []string{"auth", "login"},
			StatusArgs: []string{"auth", "status", "--json"}, NeedsTerminal: true, AcceptsInput: true,
			parseStatus: parseJSONStatus,
		},
		{
			Name: config.BackendCodex, Binary: cfg.CodexBinary, LoginArgs: []string{"login", "--device-auth"},
			StatusArgs: []string{"login", "status"}, parseStatus: parseExitStatus,
		},
		{
			Name: config.BackendKimi, Binary: cfg.KimiBinary, LoginArgs: kimiLoginArgs(cfg.KimiRegion),
			StatusArgs: []string{"provider", "list"}, parseStatus: parseProviderList,
		},
	}
}

// kimiLoginArgs logs in on the region's site: kimi.ai for global,
// kimi.com for mainland-cn. The CLI remembers the region with the login.
func kimiLoginArgs(region string) []string {
	if region == "" {
		return []string{"login"}
	}
	return []string{"login", "--region", region}
}

// parseJSONStatus reads {"loggedIn": bool, "authMethod": "..."}.
func parseJSONStatus(out string, _ error) (*bool, string) {
	var s struct {
		LoggedIn   *bool  `json:"loggedIn"`
		AuthMethod string `json:"authMethod"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &s); err != nil || s.LoggedIn == nil {
		return nil, lastLine(out)
	}
	return s.LoggedIn, s.AuthMethod
}

// parseExitStatus treats a zero exit status as logged in.
func parseExitStatus(out string, err error) (*bool, string) {
	ok := err == nil
	return &ok, lastLine(out)
}

// parseProviderList treats a configured provider as logged in.
func parseProviderList(out string, err error) (*bool, string) {
	if err != nil {
		return nil, lastLine(out)
	}
	ok := !strings.Contains(out, "No providers configured")
	return &ok, lastLine(out)
}

// lastLine returns the last non-empty line of a command's output, where
// CLIs print their verdict after any warnings.
func lastLine(s string) string {
	s = strings.TrimSpace(cleanOutput(s))
	if i := strings.LastIndexByte(s, '\n'); i >= 0 {
		s = strings.TrimSpace(s[i+1:])
	}
	if len(s) > 200 {
		s = s[:200]
	}
	return s
}
