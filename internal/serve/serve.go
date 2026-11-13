// Package serve is what every bundle's server is built on: it turns a bundle's
// tools into an MCP server over standard input and output.
//
// A bundle says what its tools are in two files beside its code. manifest.json
// is the MCP Bundle manifest, and names the bundle and its version. tools.json
// is what a model is shown of each tool: its name, description, and input
// schema, written by hand. A model programmes against those words, and the eval
// cases test them, so they are kept as text rather than derived from a Go type.
package serve

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Run does one call of a tool: the arguments as they arrived, and either the
// text the model reads or an error it can act on. An error is a result like
// any other, and never ends the server.
type Run func(ctx context.Context, args json.RawMessage) (string, error)

type toolDef struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"inputSchema"`
	// Meta is the tool's _meta, as written: under ui, the page its result
	// opens and who may call it (MCP Apps).
	Meta map[string]any `json:"_meta,omitempty"`
}

// Page is a page a tool's result opens, an MCP App: HTML that a host shows
// in a box and that talks to it in messages. A tool names it by its ui://
// address under _meta.ui.resourceUri.
type Page struct {
	URI  string
	HTML string
}

// AppMIME is what a page is served as.
const AppMIME = "text/html;profile=mcp-app"

// New builds the server for a bundle from its manifest.json, its tools.json,
// and the code for each tool. A tool described and not written, or written and
// not described, is refused: either is a tool a model would be shown and could
// not call, or could call and was never shown.
func New(manifest, tools []byte, runs map[string]Run, pages ...Page) (*mcp.Server, error) {
	var m struct{ Name, Version string }
	if err := json.Unmarshal(manifest, &m); err != nil {
		return nil, fmt.Errorf("manifest.json: %w", err)
	}
	if m.Name == "" || m.Version == "" {
		return nil, fmt.Errorf("manifest.json names no name or no version")
	}
	var defs []toolDef
	if err := json.Unmarshal(tools, &defs); err != nil {
		return nil, fmt.Errorf("tools.json: %w", err)
	}
	if len(defs) != len(runs) {
		return nil, fmt.Errorf("tools.json describes %d tools and %d are written", len(defs), len(runs))
	}
	// A tool that names a page the bundle does not have opens nothing, and a
	// page no tool names is never shown.
	named := map[string]bool{}
	for _, d := range defs {
		if ui, ok := d.Meta["ui"].(map[string]any); ok {
			if uri, _ := ui["resourceUri"].(string); uri != "" {
				named[uri] = true
			}
		}
	}
	given := map[string]bool{}
	for _, p := range pages {
		if !named[p.URI] {
			return nil, fmt.Errorf("the page %s is named by no tool", p.URI)
		}
		given[p.URI] = true
	}
	for uri := range named {
		if !given[uri] {
			return nil, fmt.Errorf("a tool names the page %s, which is not given", uri)
		}
	}
	s := mcp.NewServer(&mcp.Implementation{Name: m.Name, Version: m.Version}, nil)
	for _, p := range pages {
		html := p.HTML
		s.AddResource(&mcp.Resource{URI: p.URI, Name: p.URI, MIMEType: AppMIME},
			func(_ context.Context, req *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
				return &mcp.ReadResourceResult{Contents: []*mcp.ResourceContents{
					{URI: req.Params.URI, MIMEType: AppMIME, Text: html}}}, nil
			})
	}
	for _, d := range defs {
		run, ok := runs[d.Name]
		if !ok {
			return nil, fmt.Errorf("tools.json describes %q, which is not written", d.Name)
		}
		s.AddTool(&mcp.Tool{Name: d.Name, Description: d.Description, InputSchema: d.InputSchema, Meta: d.Meta},
			func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
				text, err := run(ctx, req.Params.Arguments)
				if err != nil {
					return &mcp.CallToolResult{IsError: true,
						Content: []mcp.Content{&mcp.TextContent{Text: err.Error()}}}, nil
				}
				return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: text}}}, nil
			})
	}
	return s, nil
}

// Main serves a bundle's tools over standard input and output until the host
// closes them.
func Main(manifest, tools []byte, runs map[string]Run, pages ...Page) {
	s, err := New(manifest, tools, runs, pages...)
	if err != nil {
		log.Fatal(err)
	}
	if err := s.Run(context.Background(), &mcp.StdioTransport{}); err != nil {
		log.Fatal(err)
	}
}

// Args decodes a call's arguments into v, a pointer to a struct of the tool's
// parameters. No arguments at all is an empty object.
func Args(raw json.RawMessage, v any) error {
	if len(bytes.TrimSpace(raw)) == 0 {
		raw = json.RawMessage("{}")
	}
	if err := json.Unmarshal(raw, v); err != nil {
		return fmt.Errorf("could not parse the call arguments: %v", err)
	}
	return nil
}
