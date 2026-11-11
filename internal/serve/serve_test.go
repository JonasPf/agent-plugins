package serve_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/JonasPf/agent-plugins/internal/serve"
	"github.com/JonasPf/agent-plugins/internal/servetest"
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
