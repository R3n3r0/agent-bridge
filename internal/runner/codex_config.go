package runner

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"sort"
	"strings"
)

var mcpServerNameRe = regexp.MustCompile(`^[a-zA-Z0-9_-]{1,64}$`)

// mcpServer is one server of an MCP configuration file
// ({"mcpServers": {...}}), the format shared by the agent mcp_config files
// and the client functions configuration.
type mcpServer struct {
	Name    string            `json:"-"`
	Type    string            `json:"type"`
	Command string            `json:"command"`
	Args    []string          `json:"args"`
	Env     map[string]string `json:"env"`
	URL     string            `json:"url"`
	Headers map[string]string `json:"headers"`
}

// readMCPServers loads an MCP configuration file, sorted by server name.
func readMCPServers(path string) ([]mcpServer, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("mcp config: %w", err)
	}
	var file struct {
		MCPServers map[string]mcpServer `json:"mcpServers"`
	}
	if err := json.Unmarshal(data, &file); err != nil {
		return nil, fmt.Errorf("mcp config %s: %w", path, err)
	}
	out := make([]mcpServer, 0, len(file.MCPServers))
	for name, srv := range file.MCPServers {
		if !mcpServerNameRe.MatchString(name) {
			return nil, fmt.Errorf("mcp config %s: server name %q must be 1-64 characters from [a-zA-Z0-9_-]", path, name)
		}
		if srv.Command == "" && srv.URL == "" {
			return nil, fmt.Errorf("mcp config %s: server %q needs a command or a url", path, name)
		}
		srv.Name = name
		out = append(out, srv)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// overrides returns the codex configuration entries of the server.
func (s mcpServer) overrides() [][2]string {
	key := "mcp_servers." + s.Name
	var out [][2]string
	add := func(k, v string) { out = append(out, [2]string{key + "." + k, v}) }
	if s.Command != "" {
		add("command", tomlString(s.Command))
		if len(s.Args) > 0 {
			add("args", tomlStrings(s.Args))
		}
		if len(s.Env) > 0 {
			add("env", tomlTable(s.Env))
		}
		return out
	}
	add("url", tomlString(s.URL))
	if len(s.Headers) > 0 {
		add("http_headers", tomlTable(s.Headers))
	}
	return out
}

// tomlString encodes s as a TOML basic string. JSON string escapes are a
// subset of the TOML ones.
func tomlString(s string) string {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(s)
	return strings.TrimSuffix(b.String(), "\n")
}

// tomlStrings encodes list as a TOML array of strings.
func tomlStrings(list []string) string {
	parts := make([]string, len(list))
	for i, s := range list {
		parts[i] = tomlString(s)
	}
	return "[" + strings.Join(parts, ", ") + "]"
}

// tomlTable encodes m as a TOML inline table with quoted keys, sorted.
func tomlTable(m map[string]string) string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, len(keys))
	for i, k := range keys {
		parts[i] = tomlString(k) + " = " + tomlString(m[k])
	}
	return "{" + strings.Join(parts, ", ") + "}"
}
