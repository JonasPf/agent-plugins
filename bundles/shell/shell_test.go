package main

import (
	"strings"
	"testing"
	"time"

	"github.com/JonasPf/agent-plugins/internal/servetest"
)

func shell(t *testing.T) func(command string) servetest.Result {
	t.Helper()
	servetest.InDir(t, t.TempDir())
	cs := servetest.Client(t, manifest, tools, runs)
	return func(command string) servetest.Result {
		t.Helper()
		return servetest.Call(t, cs, "bash", `{"command":`+quote(command)+`}`)
	}
}

func quote(s string) string {
	return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`, "\n", `\n`).Replace(s) + `"`
}

func TestACommandRunsInTheWorkingDirectoryAndItsFailureIsAResult(t *testing.T) {
	run := shell(t)
	if res := run("echo hello > f.txt && cat f.txt"); res.Error || strings.TrimSpace(res.Text) != "hello" {
		t.Errorf("result = %+v", res)
	}
	if res := run("true"); res.Error || res.Text != "(no output)" {
		t.Errorf("a silent command = %+v", res)
	}
	if res := run("echo broken; exit 3"); !res.Error || !strings.Contains(res.Text, "exit status 3") || !strings.Contains(res.Text, "broken") {
		t.Errorf("a failing command = %+v, want its status and what it said", res)
	}
	if res := run("  "); !res.Error || res.Text != "command is required" {
		t.Errorf("no command = %+v", res)
	}
}

// A command that starts something in the background and returns — a server, a
// watcher — left the background process holding the shell's output open, and
// the call waited on it until its timeout: two minutes of a turn spent on a
// command that had finished at once.
func TestACommandThatLeavesAProcessRunningReturnsAtOnce(t *testing.T) {
	run := shell(t)
	start := time.Now()
	res := run("sleep 30 & echo started")
	if took := time.Since(start); took > 10*time.Second {
		t.Fatalf("the call waited %v on a process left in the background", took.Round(time.Second))
	}
	if res.Error || !strings.Contains(res.Text, "started") {
		t.Errorf("result = %+v, want the command's own output", res)
	}
}

// A process left in the background outlives the shell that started it, and
// whoever inherits it has to collect it when it exits. A session that polled
// "is it still running?" once heard yes every two minutes for three hours
// about a script that had finished in five, because `kill -0` on a process
// nobody collected succeeds.
func TestABackgroundProcessThatExitsIsGone(t *testing.T) {
	run := shell(t)
	res := run("sleep 2 >/dev/null 2>&1 & echo $!")
	pid := strings.TrimSpace(res.Text)
	if res.Error || pid == "" {
		t.Fatalf("result = %+v, want the background process's pid", res)
	}
	alive := "kill -0 " + pid + " 2>/dev/null && echo running || echo gone"
	if got := run(alive); !strings.Contains(got.Text, "running") {
		t.Fatalf("while it runs, the check says %+v; it cannot tell running from gone", got)
	}
	deadline := time.Now().Add(15 * time.Second)
	for !strings.Contains(run(alive).Text, "gone") {
		if time.Now().After(deadline) {
			t.Fatalf("pid %s still answers 15s after it exited", pid)
		}
		time.Sleep(500 * time.Millisecond)
	}
}

// A command that runs past its timeout is ended, and everything it started
// with it: the timeout bounds the whole call.
func TestACommandPastItsTimeoutEndsWithEverythingItStarted(t *testing.T) {
	servetest.InDir(t, t.TempDir())
	cs := servetest.Client(t, manifest, tools, runs)
	start := time.Now()
	res := servetest.Call(t, cs, "bash", `{"command":"(sleep 60; echo late) & sleep 60","timeout_seconds":2}`)
	if took := time.Since(start); took > 10*time.Second {
		t.Fatalf("a two-second timeout took %v", took.Round(time.Second))
	}
	if !res.Error || !strings.Contains(res.Text, "timed out after 2s") {
		t.Errorf("result = %+v, want a timeout", res)
	}
}

func TestLongOutputIsCutAndSaysSo(t *testing.T) {
	run := shell(t)
	res := run("head -c 30000 /dev/zero | tr '\\0' x")
	if res.Error || !strings.Contains(res.Text, "[output truncated: 20000 of 30000 characters shown, 10000 dropped]") {
		t.Errorf("result ends %q", res.Text[max(0, len(res.Text)-120):])
	}
}
