package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/signal"

	"github.com/r3n3r0/agent-bridge/internal/tools"
)

// mcpTools serves the API client's functions to the CLI as an MCP server on
// stdin/stdout. The CLI starts it; it is not meant to be run by hand.
func mcpTools(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("mcp-tools", flag.ContinueOnError)
	fs.SetOutput(stderr)
	specPath := fs.String("spec", "", "function specification written by the bridge")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	spec, err := tools.ReadSpec(*specPath)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	if err := tools.ServeMCP(ctx, stdin, stdout, spec, &http.Client{}); err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	return 0
}
