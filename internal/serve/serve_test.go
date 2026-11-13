package serve_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/JonasPf/agent-plugins/internal/serve"
	"github.com/JonasPf/agent-plugins/internal/servetest"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const manifest = `{"name":"demo","version":"1.2.3"}`
const tools = `[{"name":"echo","description":"Say it back — exactly.",
  "inputSchema":{"type":"object","properties":{"text":{"type":"string","description":"What to say."}},"required":["text"]}}]`

func echo(_ context.Context, raw json.RawMessage) (string, error) {
	var a struct{ Text string }
	if err := serve.Args(raw, &a); err != nil {
		return "", err
	}
	if a.Text == "" {
		return "", errors.New("text is required")
	}
	return a.Text, nil
}

// A model is shown a tool's description and schema as they were written, not
// as a library would derive them from a type.
func TestAToolIsListedAsItWasWritten(t *testing.T) {
	cs := servetest.Client(t, []byte(manifest), []byte(tools), map[string]serve.Run{"echo": echo})
	got, err := cs.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Tools) != 1 || got.Tools[0].Name != "echo" || got.Tools[0].Description != "Say it back — exactly." {
		t.Fatalf("listed %+v", got.Tools)
	}
	schema, _ := json.Marshal(got.Tools[0].InputSchema)
	for _, want := range []string{`"What to say."`, `"required":["text"]`} {
		if !strings.Contains(string(schema), want) {
			t.Errorf("the schema %s lost %s", schema, want)
		}
	}
}

// A tool's answer is text, and its failure is an error result the model can
// read: the server carries on either way.
func TestACallAnswersInTextAndFailsAsAResult(t *testing.T) {
	cs := servetest.Client(t, []byte(manifest), []byte(tools), map[string]serve.Run{"echo": echo})
	if res := servetest.Call(t, cs, "echo", `{"text":"hello"}`); res.Error || res.Text != "hello" {
		t.Errorf("echo = %+v", res)
	}
	if res := servetest.Call(t, cs, "echo", `{}`); !res.Error || res.Text != "text is required" {
		t.Errorf("echo of nothing = %+v, want the refusal as an error result", res)
	}
	if res := servetest.Call(t, cs, "echo", `{"text":"still here"}`); res.Error || res.Text != "still here" {
		t.Errorf("after a failure, echo = %+v", res)
	}
}

// A tool described and not written is one a model would be shown and could
// not call; written and not described, one it could call and was never shown.
func TestDescriptionsAndCodeMustAgree(t *testing.T) {
	if _, err := serve.New([]byte(manifest), []byte(tools), map[string]serve.Run{"other": echo}); err == nil {
		t.Error("a tool described and not written was accepted")
	}
	if _, err := serve.New([]byte(manifest), []byte(tools), map[string]serve.Run{"echo": echo, "more": echo}); err == nil {
		t.Error("a tool written and not described was accepted")
	}
	if _, err := serve.New([]byte(`{"name":"demo"}`), []byte(tools), map[string]serve.Run{"echo": echo}); err == nil {
		t.Error("a manifest with no version was accepted")
	}
}

const withPage = `[{"name":"show","description":"Show the page.","inputSchema":{"type":"object"},
  "_meta":{"ui":{"resourceUri":"ui://demo/page"}}},
 {"name":"save","description":"What the page saves with.","inputSchema":{"type":"object"},
  "_meta":{"ui":{"visibility":["app"]}}}]`

// A tool's _meta in tools.json reaches the host as written: it is how a tool
// names the page its result opens (MCP Apps), and who may call it.
func TestAToolCarriesItsMeta(t *testing.T) {
	cs := servetest.Client(t, []byte(manifest), []byte(withPage), map[string]serve.Run{"show": echo, "save": echo},
		serve.Page{URI: "ui://demo/page", HTML: "<p>the page</p>"})
	got, err := cs.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	meta := map[string]string{}
	for _, tool := range got.Tools {
		b, _ := json.Marshal(tool.Meta)
		meta[tool.Name] = string(b)
	}
	if meta["show"] != `{"ui":{"resourceUri":"ui://demo/page"}}` || meta["save"] != `{"ui":{"visibility":["app"]}}` {
		t.Errorf("listed _meta = %v", meta)
	}
}

// "A page is served as an MCP App: read by its ui:// address, as HTML of the
// Apps profile."
func TestAPageIsServedAsAnApp(t *testing.T) {
	cs := servetest.Client(t, []byte(manifest), []byte(withPage), map[string]serve.Run{"show": echo, "save": echo},
		serve.Page{URI: "ui://demo/page", HTML: "<p>the page</p>"})
	res, err := cs.ReadResource(context.Background(), &mcp.ReadResourceParams{URI: "ui://demo/page"})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Contents) != 1 || res.Contents[0].MIMEType != "text/html;profile=mcp-app" || res.Contents[0].Text != "<p>the page</p>" {
		t.Errorf("read %+v", res.Contents)
	}
}

// A tool that names a page the bundle does not have opens nothing; a page no
// tool names is never shown.
func TestPagesAndToolsMustAgree(t *testing.T) {
	runs := map[string]serve.Run{"show": echo, "save": echo}
	if _, err := serve.New([]byte(manifest), []byte(withPage), runs); err == nil {
		t.Error("a tool naming a page that is not there was accepted")
	}
	if _, err := serve.New([]byte(manifest), []byte(withPage), runs,
		serve.Page{URI: "ui://demo/page", HTML: "x"}, serve.Page{URI: "ui://demo/other", HTML: "y"}); err == nil {
		t.Error("a page no tool names was accepted")
	}
}
