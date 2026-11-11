package main

import (
	"context"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/JonasPf/agent-plugins/internal/servetest"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestTheClockAnswersAndRefusesNonsense(t *testing.T) {
	cs := servetest.Client(t, manifest, tools, runs)
	if res := servetest.Call(t, cs, "clock", `{}`); res.Error || !strings.Contains(res.Text, "utc:") || !strings.Contains(res.Text, "weekday:") {
		t.Errorf("clock = %+v", res)
	}
	if res := servetest.Call(t, cs, "clock", `{"offset":"soon"}`); !res.Error || !strings.Contains(res.Text, "not a duration") {
		t.Errorf("a nonsense offset = %+v", res)
	}
}

// The bundle as it ships: the program built, started by a host, and spoken to
// over its standard input and output. Every bundle is this same main, so one
// of them proves it.
func TestTheBuiltServerAnswersOverStandardInputAndOutput(t *testing.T) {
	bin := filepath.Join(t.TempDir(), "server")
	if out, err := exec.Command("go", "build", "-o", bin, ".").CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, out)
	}
	ctx := context.Background()
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "0"}, nil).
		Connect(ctx, &mcp.CommandTransport{Command: exec.Command(bin)}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer cs.Close()
	listed, err := cs.ListTools(ctx, nil)
	if err != nil || len(listed.Tools) != 1 || listed.Tools[0].Name != "clock" {
		t.Fatalf("listed %+v (%v)", listed, err)
	}
	if res := servetest.Call(t, cs, "clock", `{"offset":"1h"}`); res.Error || !strings.Contains(res.Text, "unix:") {
		t.Errorf("clock = %+v", res)
	}
}
