package login

import (
	"regexp"
	"strings"
)

var (
	// Terminal control sequences: OSC (hyperlinks, titles) and CSI.
	oscRe = regexp.MustCompile(`\x1b\][^\x07\x1b]*(?:\x07|\x1b\\)`)
	csiRe = regexp.MustCompile(`\x1b\[[0-9;?]*[ -/]*[@-~]`)
	escRe = regexp.MustCompile(`\x1b[@-Z\\-_]`)
	urlRe = regexp.MustCompile(`https?://[^\s"'<>\x1b]+`)
	// Device codes look like "ABCD-1234" or "Z3C8-E3Q7O".
	codeRe = regexp.MustCompile(`\b[A-Z0-9]{4}-[A-Z0-9]{4,6}\b`)
)

// cleanOutput removes terminal control sequences and carriage returns.
func cleanOutput(s string) string {
	s = oscRe.ReplaceAllString(s, "")
	s = csiRe.ReplaceAllString(s, "")
	s = escRe.ReplaceAllString(s, "")
	s = strings.ReplaceAll(s, "\r\n", "\n")
	return strings.ReplaceAll(s, "\r", "")
}

// findURLs returns the distinct links in s, in order.
func findURLs(s string) []string {
	out := []string{}
	seen := map[string]bool{}
	for _, u := range urlRe.FindAllString(s, -1) {
		u = strings.TrimRight(u, ".,);]")
		if !seen[u] {
			seen[u] = true
			out = append(out, u)
		}
	}
	return out
}

// findCodes returns the distinct one-time device codes in s, in order.
// Codes inside links are ignored.
func findCodes(s string) []string {
	out := []string{}
	seen := map[string]bool{}
	for _, c := range codeRe.FindAllString(urlRe.ReplaceAllString(s, " "), -1) {
		if !seen[c] {
			seen[c] = true
			out = append(out, c)
		}
	}
	return out
}
