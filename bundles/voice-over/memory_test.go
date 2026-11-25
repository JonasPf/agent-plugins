package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/JonasPf/agent-plugins/internal/servetest"
)

// withMemory makes this machine have the memory given (none: unknown) and
// read its cgroups from the files given, by their paths under /
// (proc/self/cgroup, sys/fs/cgroup/...), in place of its own.
func withMemory(t *testing.T, machine int64, files map[string]string) {
	t.Helper()
	oldMachine := machineMemory
	machineMemory = func() (int64, bool) { return machine, machine > 0 }
	t.Cleanup(func() { machineMemory = oldMachine })
	root := t.TempDir()
	for name, body := range files {
		p := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	old := sysRoot
	sysRoot = root
	t.Cleanup(func() { sysRoot = old })
}

// "On this machine, transcribing needs about 2 GB of memory and speaking about
// 1 GB. Where the machine, or the container it runs in, allows less, the call
// refuses before downloading anything, saying how much there is and how to
// have OpenRouter do it instead."
func TestThisMachineRefusesWorkItHasNoMemoryFor(t *testing.T) {
	dir := t.TempDir()
	servetest.InDir(t, dir)
	bands(t, "clip.mov", true, "red:4", "blue:4")
	t.Setenv(keyName, "")
	t.Setenv(backendName, "")
	d, _ := withLocalModels(t)
	writeScript(t, [3]float64{1, 440, 1}, [3]float64{4, 880, 1})
	// As in the agent, where a bundle may read /sys/fs/cgroup and not /proc:
	// its cgroup is the container's own, at the root.
	container := func(limit string) {
		withMemory(t, 16<<30, map[string]string{"sys/fs/cgroup/memory.max": limit + "\n"})
	}
	cs := servetest.Client(t, manifest, tools, runs, pages...)

	container("536870912")
	res := servetest.Call(t, cs, "voiceover_transcribe", `{"video":"clip.mov"}`)
	for _, want := range []string{"Transcribing on this machine needs about 2 GB", "the container it runs in allows 512 MB",
		"OpenRouter API key (" + keyName + ")", "voiceover_frames"} {
		if !res.Error || !strings.Contains(res.Text, want) {
			t.Errorf("transcribing in 512 MB = %+v, want a refusal saying %q", res, want)
		}
	}
	res = render(t, `{"video":"clip.mov"}`)
	if !res.Error || !strings.Contains(res.Text, "Speaking on this machine needs about 1 GB") ||
		!strings.Contains(res.Text, "allows 512 MB") {
		t.Errorf("speaking in 512 MB = %+v, want a refusal naming 1 GB and 512 MB", res)
	}
	if got, _ := d.asked(); len(got) != 0 {
		t.Errorf("downloaded %v before refusing, want nothing", got)
	}

	// 1.5 GB is room to speak, not to transcribe.
	container("1610612736")
	res = servetest.Call(t, cs, "voiceover_transcribe", `{"video":"clip.mov"}`)
	if !res.Error || !strings.Contains(res.Text, "allows 1.5 GB") {
		t.Errorf("transcribing in 1.5 GB = %+v, want a refusal naming 1.5 GB", res)
	}
	res = render(t, `{"video":"clip.mov"}`)
	if res.Error || !strings.Contains(res.Text, "Spoken by this machine") {
		t.Errorf("speaking in 1.5 GB = %+v, want it spoken", res)
	}

	// A container with no limit is as large as the machine.
	container("max")
	res = servetest.Call(t, cs, "voiceover_transcribe", `{"video":"clip.mov"}`)
	if res.Error || !strings.Contains(res.Text, "Transcribed on this machine") {
		t.Errorf("transcribing with no limit on 16 GB = %+v, want it transcribed", res)
	}

	// A machine of 1.9 GB, as a small server, has not the room to transcribe.
	withMemory(t, 1965832<<10, nil)
	res = servetest.Call(t, cs, "voiceover_transcribe", `{"video":"clip.mov"}`)
	if !res.Error || !strings.Contains(res.Text, "this machine has 1.9 GB") {
		t.Errorf("transcribing on 1.9 GB = %+v, want a refusal naming the machine's 1.9 GB", res)
	}

	// Set to OpenRouter, the memory here is not asked about.
	container("536870912")
	g := withGateway(t)
	g.on("/audio/transcriptions", answer(200, `{"text":"hello","segments":[{"start":0,"end":1,"text":"hello"}],"usage":{"cost":0.0001}}`))
	res = servetest.Call(t, cs, "voiceover_transcribe", `{"video":"clip.mov"}`)
	if res.Error || !strings.Contains(res.Text, "OpenRouter") {
		t.Errorf("transcribing by OpenRouter in 512 MB = %+v, want it transcribed", res)
	}
}

func TestMemoryLimitIsTheTightestThereIs(t *testing.T) {
	const gib = 1 << 30
	for _, c := range []struct {
		name    string
		machine int64
		files   map[string]string
		bytes   int64
		of      string
	}{
		{"nothing known, as on a Mac", 0, nil, 0, ""},
		{"the machine alone, with no cgroup limit", gib, map[string]string{
			"proc/self/cgroup": "0::/\n", "sys/fs/cgroup/memory.max": "max\n"}, gib, "this machine has"},
		{"a container with no limit of its own, inside one with a limit", 16 * gib, map[string]string{
			"proc/self/cgroup":                                     "0::/system.slice/docker-a.scope\n",
			"sys/fs/cgroup/system.slice/memory.max":                "1073741824\n",
			"sys/fs/cgroup/system.slice/docker-a.scope/memory.max": "max\n",
		}, gib, "the container it runs in allows"},
		{"cgroup v1", 16 * gib, map[string]string{
			"proc/self/cgroup":                                    "12:cpu,cpuacct:/docker/a\n4:memory:/docker/a\n",
			"sys/fs/cgroup/memory/memory.limit_in_bytes":          "9223372036854771712\n",
			"sys/fs/cgroup/memory/docker/a/memory.limit_in_bytes": "536870912\n",
		}, gib / 2, "the container it runs in allows"},
		{"cgroup v1, with /proc unreadable", 16 * gib, map[string]string{
			"sys/fs/cgroup/memory/memory.limit_in_bytes": "536870912\n",
		}, gib / 2, "the container it runs in allows"},
		{"a container larger than the machine", gib, map[string]string{
			"proc/self/cgroup": "0::/\n", "sys/fs/cgroup/memory.max": "2147483648\n"}, gib, "this machine has"},
	} {
		t.Run(c.name, func(t *testing.T) {
			withMemory(t, c.machine, c.files)
			got, ok := memoryLimit()
			if want := c.bytes != 0; ok != want || got.bytes != c.bytes || got.of != c.of {
				t.Errorf("memoryLimit() = %+v, %v; want %d bytes, %q", got, ok, c.bytes, c.of)
			}
		})
	}
}

// "In the agent, which confines a bundle to what its manifest names, it asks
// to read /sys/fs/cgroup, where a container's limit is, and not /proc."
func TestTheBundleAsksToReadItsCgroupsAndNothingElse(t *testing.T) {
	var m struct {
		Meta map[string]struct {
			Reads []string `json:"reads"`
		} `json:"_meta"`
	}
	if err := json.Unmarshal(manifest, &m); err != nil {
		t.Fatal(err)
	}
	if got := m.Meta["io.github.jonaspf.agent"].Reads; !slices.Equal(got, []string{"/sys/fs/cgroup"}) {
		t.Errorf("the manifest asks to read %v, want /sys/fs/cgroup alone", got)
	}
}
