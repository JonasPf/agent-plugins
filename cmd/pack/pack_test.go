package main

import (
	"archive/zip"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"testing"
)

const root = "../.."

func bundles(t *testing.T) []string {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join(root, "bundles"))
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range entries {
		if e.IsDir() {
			names = append(names, e.Name())
		}
	}
	if len(names) == 0 {
		t.Fatal("no bundles")
	}
	return names
}

type listed struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

// A bundle's manifest lists its tools for a host to show before anything is
// run, and tools.json is what the server then says. Two lists of the same
// thing drift, so they are held to each other; and every tool has the eval
// cases that test its description.
func TestEveryBundleSaysTheSameInItsManifestAsItsServer(t *testing.T) {
	for _, name := range bundles(t) {
		dir := filepath.Join(root, "bundles", name)
		var m struct {
			Name   string   `json:"name"`
			Tools  []listed `json:"tools"`
			Server struct {
				Type string `json:"type"`
			} `json:"server"`
		}
		readJSON(t, filepath.Join(dir, "manifest.json"), &m)
		var served []listed
		readJSON(t, filepath.Join(dir, "tools.json"), &served)
		if m.Name != name {
			t.Errorf("bundles/%s names itself %q", name, m.Name)
		}
		if m.Server.Type != "binary" {
			t.Errorf("%s: server.type is %q, want binary", name, m.Server.Type)
		}
		if !reflect.DeepEqual(m.Tools, served) {
			t.Errorf("%s: manifest.json and tools.json list different tools:\n%+v\n%+v", name, m.Tools, served)
		}
		for _, tool := range served {
			var suite struct {
				Cases []json.RawMessage `json:"cases"`
			}
			readJSON(t, filepath.Join(dir, "evals", tool.Name+".json"), &suite)
			if len(suite.Cases) == 0 {
				t.Errorf("%s: the tool %s has no eval cases", name, tool.Name)
			}
		}
	}
}

func readJSON(t *testing.T, path string, v any) {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(b, v); err != nil {
		t.Fatalf("%s: %v", path, err)
	}
}

// What a release publishes: a zip a host unpacks, holding the manifest, which
// names the one platform its server was built for, the server, executable, and
// each tool's eval cases, so a host that can put them to a model has them.
func TestABundleIsPackedAsAManifestItsServerAndItsEvalCases(t *testing.T) {
	out := t.TempDir()
	path, err := Pack(root, "clock", runtime.GOOS, runtime.GOARCH, out)
	if err != nil {
		t.Fatal(err)
	}
	z, err := zip.OpenReader(path)
	if err != nil {
		t.Fatal(err)
	}
	defer z.Close()
	files := map[string]*zip.File{}
	for _, f := range z.File {
		files[f.Name] = f
	}
	if len(files) != 3 || files["manifest.json"] == nil || files["server"] == nil || files["evals/clock.json"] == nil {
		t.Fatalf("the bundle holds %v, want manifest.json, server and its tool's eval cases", z.File)
	}
	if files["server"].Mode()&0o111 == 0 {
		t.Error("the server is not executable")
	}
	r, _ := files["manifest.json"].Open()
	raw, _ := io.ReadAll(r)
	var m struct {
		Version       string `json:"version"`
		Compatibility struct {
			Platforms []string `json:"platforms"`
		} `json:"compatibility"`
	}
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(m.Compatibility.Platforms, []string{runtime.GOOS}) {
		t.Errorf("platforms = %v, want the one it was built for", m.Compatibility.Platforms)
	}
	if want := "clock-" + m.Version + "-" + runtime.GOOS + "-" + runtime.GOARCH + ".mcpb"; filepath.Base(path) != want {
		t.Errorf("the file is %s, want %s", filepath.Base(path), want)
	}
}

// A bundle that says it runs on one system is not built for another.
func TestABundleIsNotPackedForAPlatformItDoesNotRunOn(t *testing.T) {
	path, err := Pack(root, "web-browse", "darwin", "arm64", t.TempDir())
	if err != nil || path != "" {
		t.Errorf("packed %q (%v), want nothing for a platform the manifest leaves out", path, err)
	}
}
