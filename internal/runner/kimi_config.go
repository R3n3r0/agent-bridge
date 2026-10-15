package runner

import "sort"

// acpBlock is an ACP prompt content block.
type acpBlock struct {
	Type     string `json:"type"`
	Text     string `json:"text,omitempty"`
	MimeType string `json:"mimeType,omitempty"`
	Data     string `json:"data,omitempty"`
}

// acpNameValue is an ACP environment variable or HTTP header.
type acpNameValue struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

// acpMCPServer is an MCP server in ACP session/new and session/resume:
// acpStdioServer or acpHTTPServer.
type acpMCPServer any

// acpStdioServer is a stdio MCP server; ACP requires args and env.
type acpStdioServer struct {
	Name    string         `json:"name"`
	Command string         `json:"command"`
	Args    []string       `json:"args"`
	Env     []acpNameValue `json:"env"`
}

// acpHTTPServer is a streamable HTTP MCP server.
type acpHTTPServer struct {
	Type    string         `json:"type"`
	Name    string         `json:"name"`
	URL     string         `json:"url"`
	Headers []acpNameValue `json:"headers"`
}

// toACPServers converts servers of an MCP configuration file.
func toACPServers(list []mcpServer) []acpMCPServer {
	out := make([]acpMCPServer, 0, len(list))
	for _, s := range list {
		if s.Command != "" {
			out = append(out, acpStdioServer{Name: s.Name, Command: s.Command, Args: nonNil(s.Args), Env: nameValues(s.Env)})
			continue
		}
		out = append(out, acpHTTPServer{Type: "http", Name: s.Name, URL: s.URL, Headers: nameValues(s.Headers)})
	}
	return out
}

// nameValues converts a map, sorted by name; never nil, as ACP requires
// the arrays.
func nameValues(m map[string]string) []acpNameValue {
	out := []acpNameValue{}
	for _, k := range sortedKeys(m) {
		out = append(out, acpNameValue{Name: k, Value: m[k]})
	}
	return out
}

func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}
