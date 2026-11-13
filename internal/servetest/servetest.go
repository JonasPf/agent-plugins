// Package servetest calls a bundle's tools the way a host does: over the
// protocol, against the server the bundle ships.
package servetest

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/JonasPf/agent-plugins/internal/serve"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Result is what a call came back with: its text, and whether it was an error.
type Result struct {
	Text  string
	Error bool
}

// Client connects a client to the bundle's server, which lives as long as the
// test does.
func Client(t *testing.T, manifest, tools []byte, runs map[string]serve.Run, pages ...serve.Page) *mcp.ClientSession {
	t.Helper()
	s, err := serve.New(manifest, tools, runs, pages...)
	if err != nil {
		t.Fatal(err)
	}
	here, there := mcp.NewInMemoryTransports()
	if _, err := s.Connect(context.Background(), there, nil); err != nil {
		t.Fatal(err)
	}
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "0"}, nil).Connect(context.Background(), here, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cs.Close() })
	return cs
}

// Call calls one tool with arguments written as JSON.
func Call(t *testing.T, cs *mcp.ClientSession, tool, args string) Result {
	t.Helper()
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: tool, Arguments: json.RawMessage(args)})
	if err != nil {
		t.Fatalf("%s %s: %v", tool, args, err)
	}
	var parts []string
	for _, c := range res.Content {
		if tc, ok := c.(*mcp.TextContent); ok {
			parts = append(parts, tc.Text)
		}
	}
	return Result{Text: strings.Join(parts, "\n"), Error: res.IsError}
}

// InDir makes dir the working directory for the test, which is where a host
// runs a server: the conversation's own directory.
func InDir(t *testing.T, dir string) {
	t.Helper()
	t.Chdir(dir)
}
