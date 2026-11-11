package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/JonasPf/agent-plugins/internal/servetest"
)

// The three tools as a host calls them: in the directory the server was
// started in, which is the only place they reach.
func TestWriteReadAndEditAFileInTheWorkingDirectory(t *testing.T) {
	dir := t.TempDir()
	servetest.InDir(t, dir)
	cs := servetest.Client(t, manifest, tools, runs)

	if res := servetest.Call(t, cs, "write", `{"path":"notes/a.txt","content":"one\ntwo\nthree\n"}`); res.Error || res.Text != "wrote notes/a.txt (14 bytes)" {
		t.Fatalf("write = %+v", res)
	}
	if res := servetest.Call(t, cs, "read", `{"path":"notes/a.txt","offset":1,"limit":1}`); res.Error || res.Text != "two" {
		t.Errorf("read of one line = %+v", res)
	}
	if res := servetest.Call(t, cs, "edit", `{"path":"notes/a.txt","old":"two","new":"2"}`); res.Error || res.Text != "edited notes/a.txt (1 replacement(s))" {
		t.Errorf("edit = %+v", res)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "notes", "a.txt")); string(b) != "one\n2\nthree\n" {
		t.Errorf("the file holds %q", b)
	}
	if res := servetest.Call(t, cs, "edit", `{"path":"notes/a.txt","old":"absent","new":"x"}`); !res.Error || !strings.Contains(res.Text, "not found") {
		t.Errorf("an edit that matches nothing = %+v", res)
	}
	if res := servetest.Call(t, cs, "read", `{"path":"missing.txt"}`); !res.Error {
		t.Errorf("reading a file that is not there = %+v", res)
	}
}

func TestAPathOutsideTheWorkingDirectoryIsRefused(t *testing.T) {
	servetest.InDir(t, t.TempDir())
	outside := filepath.Join(t.TempDir(), "secret.txt")
	if err := os.WriteFile(outside, []byte("keep out"), 0o644); err != nil {
		t.Fatal(err)
	}
	cs := servetest.Client(t, manifest, tools, runs)
	path, _ := json.Marshal(outside)
	for tool, args := range map[string]string{
		"read":  `{"path":` + string(path) + `}`,
		"write": `{"path":"../escape.txt","content":"x"}`,
		"edit":  `{"path":` + string(path) + `,"old":"keep","new":"let"}`,
	} {
		if res := servetest.Call(t, cs, tool, args); !res.Error || !strings.Contains(res.Text, "outside the workspace") {
			t.Errorf("%s %s = %+v, want it refused as outside the workspace", tool, args, res)
		}
	}
	if b, _ := os.ReadFile(outside); string(b) != "keep out" {
		t.Errorf("a file outside was changed: %q", b)
	}
}

// A skill's scripts run from its folder, so an agent sees that path and
// reaches for read. A host that says where its skills are gets the call that
// reads the file named in the refusal; any other path is only outside.
func TestReadingASkillsFileNamesTheCallThatReadsIt(t *testing.T) {
	servetest.InDir(t, t.TempDir())
	skill := filepath.Join(t.TempDir(), "trip")
	if err := os.MkdirAll(filepath.Join(skill, "scripts"), 0o755); err != nil {
		t.Fatal(err)
	}
	script := filepath.Join(skill, "scripts", "pack.py")
	if err := os.WriteFile(script, []byte("print('packed')\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	dirs, _ := json.Marshal(map[string]string{"trip": skill})
	t.Setenv("AGENT_SKILL_DIRS", string(dirs))
	cs := servetest.Client(t, manifest, tools, runs)
	read := func(path string) servetest.Result {
		args, _ := json.Marshal(map[string]string{"path": path})
		return servetest.Call(t, cs, "read", string(args))
	}

	if res := read(script); !res.Error || !strings.Contains(res.Text, `skill_read {"name": "trip", "file": "scripts/pack.py"}`) {
		t.Errorf("read of a skill's script = %+v, want the skill_read call that reads it", res)
	}
	if res := read("/etc/hostname"); !res.Error || strings.Contains(res.Text, "skill_read") {
		t.Errorf("read of another path = %+v, want only that it is outside", res)
	}
	t.Setenv("AGENT_SKILL_DIRS", "")
	if res := read(script); !res.Error || strings.Contains(res.Text, "skill_read") {
		t.Errorf("on a host that names no skills, read = %+v, want only that it is outside", res)
	}
}
