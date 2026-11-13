package main

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/JonasPf/agent-plugins/internal/servetest"
)

// speaker answers a speech request with a tone the line's words describe:
// "tone 440 5" is five seconds at 440 Hz. A line named in fail is refused,
// as often as fail says.
func speaker(t *testing.T, fail map[string]int) http.HandlerFunc {
	var mu sync.Mutex
	return func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Input string `json:"input"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("a speech request that is not JSON: %v", err)
		}
		mu.Lock()
		refuse := fail[body.Input] > 0
		if refuse {
			fail[body.Input]--
		}
		mu.Unlock()
		if refuse {
			answer(500, `{"error":{"message":"the provider fell over"}}`)(w, r)
			return
		}
		var hz, seconds float64
		if _, err := fmt.Sscanf(body.Input, "tone %g %g", &hz, &seconds); err != nil {
			hz, seconds = 330, 1
		}
		wav, err := exec.Command(fullFFmpeg(), "-hide_banner", "-loglevel", "error", "-f", "lavfi",
			"-i", fmt.Sprintf("sine=f=%g:d=%g", hz, seconds), "-ac", "1", "-ar", "24000", "-f", "wav", "-").Output()
		if err != nil {
			t.Errorf("could not make the tone: %v", err)
		}
		sound("audio/wav", wav, "gen-"+strconv.Itoa(len(body.Input)))(w, r)
	}
}

// scripted is an 8-second video, a gateway that speaks tones, and a script
// of lines at the times given, each a tone: [at, hz, seconds].
func scripted(t *testing.T, lines ...[3]float64) (string, *gateway) {
	dir := t.TempDir()
	servetest.InDir(t, dir)
	bands(t, "clip.mov", true, "red:4", "blue:4")
	g := withGateway(t)
	g.on("/models", answer(200, offeredVoices))
	g.on("/audio/speech", speaker(t, nil))
	g.on("/generation", answer(200, `{"data":{"usage":0.0001}}`))
	writeScript(t, lines...)
	return dir, g
}

func writeScript(t *testing.T, lines ...[3]float64) {
	t.Helper()
	var s []map[string]any
	for _, l := range lines {
		s = append(s, map[string]any{"at": l[0], "text": fmt.Sprintf("tone %g %g", l[1], l[2])})
	}
	b, _ := json.Marshal(s)
	if err := os.MkdirAll("clip.voiceover", 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join("clip.voiceover", "script.json"), b, 0o644); err != nil {
		t.Fatal(err)
	}
}

// track is the voice-over track's samples.
func track(t *testing.T, dir string) []int16 {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(dir, "clip.voiceover", "track.wav"))
	if err != nil {
		t.Fatal(err)
	}
	if string(b[:4]) != "RIFF" || binary.LittleEndian.Uint32(b[24:]) != trackRate || binary.LittleEndian.Uint16(b[22:]) != 1 {
		t.Fatalf("track.wav is not mono at %d Hz: % x", trackRate, b[:44])
	}
	pcm := make([]int16, (len(b)-44)/2)
	for i := range pcm {
		pcm[i] = int16(binary.LittleEndian.Uint16(b[44+2*i:]))
	}
	return pcm
}

// pitch is how often the track crosses zero going up, per second, between
// two times: a tone's frequency.
func pitch(pcm []int16, from, to float64) float64 {
	part := pcm[int(from*trackRate):int(to*trackRate)]
	n := 0
	for i := 1; i < len(part); i++ {
		if part[i-1] < 0 && part[i] >= 0 {
			n++
		}
	}
	return float64(n) / (to - from)
}

func near(got, want, by float64) bool { return math.Abs(got-want) <= by }

func render(t *testing.T, args string) servetest.Result {
	t.Helper()
	cs := servetest.Client(t, manifest, tools, runs, pages...)
	return servetest.Call(t, cs, "voiceover_render", args)
}

// "A line still speaking when the next starts is cut there, with a fade so
// it makes no click, and named; the next starts exactly where it was put;
// the track is as long as the video; and the video gets it as its sound,
// its picture copied, as long as it was."
func TestRenderCutsALineTheNextOneStartsOn(t *testing.T) {
	dir, _ := scripted(t, [3]float64{1, 440, 5}, [3]float64{3, 880, 2})

	res := render(t, `{"video":"clip.mov"}`)
	if res.Error {
		t.Fatalf("voiceover_render: %s", res.Text)
	}
	if !strings.Contains(res.Text, "line 1 cut 3.0 s short by line 2") {
		t.Errorf("the result does not say which line was cut, and by how much:\n%s", res.Text)
	}
	pcm := track(t, dir)
	if p := pitch(pcm, 1.2, 2.9); !near(p, 440, 15) {
		t.Errorf("between 1.2 and 2.9 s the track is at %.0f Hz, want the first line's 440", p)
	}
	if p := pitch(pcm, 3.02, 4.9); !near(p, 880, 15) {
		t.Errorf("between 3.02 and 4.9 s the track is at %.0f Hz, want the second line's 880", p)
	}
	for i, s := range pcm[int(5.1*trackRate):] {
		if s != 0 {
			t.Fatalf("the track is not silent after the second line: %d at %.2f s", s, 5.1+float64(i)/trackRate)
		}
	}
	if s := pcm[3*trackRate-1]; s > 300 || s < -300 {
		t.Errorf("the cut clicks: the sample before it is %d", s)
	}
	if got := float64(len(pcm)) / trackRate; !near(got, 8, 0.05) {
		t.Errorf("the track is %.2f s long, the video 8", got)
	}

	out := filepath.Join(dir, "clip.voiceover.mp4")
	said, _ := exec.Command(fullFFmpeg(), "-hide_banner", "-i", out).CombinedOutput()
	m := regexp.MustCompile(`Duration: 00:00:(\d+\.\d+)`).FindStringSubmatch(string(said))
	if m == nil {
		t.Fatalf("no video was written at %s:\n%s", out, said)
	}
	if d, _ := strconv.ParseFloat(m[1], 64); !near(d, 8, 0.1) {
		t.Errorf("the video with the voice-over is %.2f s long, the original 8", d)
	}
	if !strings.Contains(string(said), "Video: h264") || !strings.Contains(string(said), "Audio: aac") {
		t.Errorf("the output is not the picture with the new sound:\n%s", said)
	}
	for _, want := range []string{"clip.voiceover.mp4", "OpenRouter", "heygen/heygen-voice-1-turbo", "gareth",
		"$0.0002", "2 lines spoken now", "nobody has listened"} {
		if !strings.Contains(res.Text, want) {
			t.Errorf("the result does not say %q:\n%s", want, res.Text)
		}
	}
}

// "A line put on top of another replaces it, and says so."
func TestRenderALineOnTopOfAnotherReplacesIt(t *testing.T) {
	dir, _ := scripted(t, [3]float64{1, 440, 5}, [3]float64{1, 880, 2})

	res := render(t, `{"video":"clip.mov"}`)
	if res.Error || !strings.Contains(res.Text, "line 1 cut 5.0 s short by line 2") {
		t.Fatalf("voiceover_render = %+v", res)
	}
	if p := pitch(track(t, dir), 1.02, 2.9); !near(p, 880, 15) {
		t.Errorf("at the shared start the track is at %.0f Hz, want only the later line's 880", p)
	}
}

// "A last line still speaking when the video ends is cut there, like one
// cut by the next: refused, it sent the agent to shorten words the operator
// had approved."
func TestRenderCutsTheLastLineAtTheEnd(t *testing.T) {
	dir, _ := scripted(t, [3]float64{1, 440, 1}, [3]float64{7, 880, 2})

	res := render(t, `{"video":"clip.mov"}`)
	if res.Error || !strings.Contains(res.Text, "line 2 cut 1.0 s short by the end of the video") {
		t.Fatalf("voiceover_render = %+v", res)
	}
	pcm := track(t, dir)
	if p := pitch(pcm, 7.02, 7.95); !near(p, 880, 15) {
		t.Errorf("the last line does not play until the end: %.0f Hz", p)
	}
	if got := float64(len(pcm)) / trackRate; !near(got, 8, 0.05) {
		t.Errorf("the track is %.2f s long, the video 8", got)
	}
	if s := pcm[len(pcm)-1]; s > 300 || s < -300 {
		t.Errorf("the end clicks: the last sample is %d", s)
	}
}

// "Lines that do not meet are left alone."
func TestRenderLeavesLinesThatDoNotMeetAlone(t *testing.T) {
	_, _ = scripted(t, [3]float64{0.5, 440, 1}, [3]float64{5.8, 880, 1})

	res := render(t, `{"video":"clip.mov"}`)
	if res.Error || strings.Contains(res.Text, "short by") || !strings.Contains(res.Text, "none cut") {
		t.Fatalf("voiceover_render = %+v", res)
	}
}

// "A line is spoken once: the speech is named by the model, voice and words,
// so a line whose words did not change keeps its speech, a line changed is
// spoken again alone, and a new voice speaks every line again."
func TestRenderSpeaksALineOnce(t *testing.T) {
	_, g := scripted(t, [3]float64{1, 440, 1}, [3]float64{4, 880, 1})
	spoken := func() int { return len(g.asked("/audio/speech")) }

	if res := render(t, `{"video":"clip.mov"}`); res.Error || spoken() != 2 {
		t.Fatalf("first render: %d spoken, %+v", spoken(), res)
	}
	res := render(t, `{"video":"clip.mov"}`)
	if res.Error || spoken() != 2 || !strings.Contains(res.Text, "2 kept") {
		t.Errorf("an unchanged script was spoken again: %d requests, %s", spoken(), res.Text)
	}
	writeScript(t, [3]float64{2, 440, 1}, [3]float64{4, 660, 1})
	res = render(t, `{"video":"clip.mov"}`)
	if res.Error || spoken() != 3 || !strings.Contains(res.Text, "1 line spoken now, 1 kept") {
		t.Errorf("moving one line and changing the other's words: %d requests, want 3: %s", spoken(), res.Text)
	}
	res = render(t, `{"video":"clip.mov","voice":"alice"}`)
	if res.Error || spoken() != 5 {
		t.Errorf("a new voice: %d requests, want 5: %s", spoken(), res.Text)
	}
	last := g.asked("/audio/speech")[4]
	if at(last.body, "voice") != "alice" || at(last.body, "model") != "elevenlabs/eleven-flash-v2.5" {
		t.Errorf("the voice did not bring its model: %v", last.body)
	}
	// The voice chosen is the project's from then on.
	res = render(t, `{"video":"clip.mov"}`)
	if res.Error || spoken() != 5 || !strings.Contains(res.Text, "alice") {
		t.Errorf("the next render did not keep alice: %d requests, %s", spoken(), res.Text)
	}
}

// "A line that cannot be spoken is named, nothing is placed, and the lines
// already spoken are kept, so the next call pays only for the rest."
func TestRenderNamesALineThatCouldNotBeSpokenAndKeepsTheRest(t *testing.T) {
	dir, g := scripted(t, [3]float64{1, 440, 1}, [3]float64{4, 880, 1})
	g.on("/audio/speech", speaker(t, map[string]int{"tone 880 1": 1}))

	res := render(t, `{"video":"clip.mov"}`)
	if !res.Error || !strings.Contains(res.Text, "line 2") || !strings.Contains(res.Text, "the provider fell over") ||
		!strings.Contains(res.Text, "1 of 2") {
		t.Fatalf("voiceover_render = %+v, want line 2 named and what was kept", res)
	}
	if _, err := os.Stat(filepath.Join(dir, "clip.voiceover.mp4")); err == nil {
		t.Error("a video was written with a line missing")
	}
	before := len(g.asked("/audio/speech"))
	res = render(t, `{"video":"clip.mov"}`)
	if res.Error {
		t.Fatalf("the second render: %s", res.Text)
	}
	if n := len(g.asked("/audio/speech")) - before; n != 1 {
		t.Errorf("the second render asked for %d lines, want only the one missing", n)
	}
}

// "What it cannot do it refuses, saying what to do, and writes nothing."
func TestRenderRefusesWhatItCannotDo(t *testing.T) {
	dir, g := scripted(t, [3]float64{1, 440, 1})
	proj := filepath.Join(dir, "clip.voiceover", "project.json")

	res := render(t, `{"video":"clip.mov","voice":"nobody"}`)
	if !res.Error || !strings.Contains(res.Text, "nobody") || !strings.Contains(res.Text, "gareth") || !strings.Contains(res.Text, "alice") {
		t.Errorf("an unknown voice: %+v, want a refusal listing the voices there are", res)
	}
	if err := os.WriteFile(proj, []byte(`{"voice":"gareth","model":"heygen/heygen-voice-1-turbo","translate_to":"de"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	res = render(t, `{"video":"clip.mov"}`)
	if !res.Error || !strings.Contains(res.Text, "translat") || !strings.Contains(res.Text, "voiceover_script") {
		t.Errorf("a translation owed: %+v, want a refusal saying to translate and call voiceover_script", res)
	}
	_ = os.Remove(proj)
	_ = os.Remove(filepath.Join(dir, "clip.voiceover", "script.json"))
	res = render(t, `{"video":"clip.mov"}`)
	if !res.Error || !strings.Contains(res.Text, "voiceover_script") {
		t.Errorf("no script: %+v, want a refusal sending it to voiceover_script", res)
	}
	if n := len(g.asked("/audio/speech")); n != 0 {
		t.Errorf("%d lines were spoken by calls that were refused", n)
	}
	if _, err := os.Stat(filepath.Join(dir, "clip.voiceover.mp4")); err == nil {
		t.Error("a video was written")
	}
}

// "An hour of track is never held in memory: it is written as it is laid
// out, on a server with less than 2 GB."
func TestATrackIsWrittenWithoutBeingHeldInMemory(t *testing.T) {
	dir := t.TempDir()
	line := filepath.Join(dir, "line.pcm")
	if err := os.WriteFile(line, make([]byte, 2*trackRate), 0o644); err != nil {
		t.Fatal(err)
	}
	total := int64(61 * 60 * trackRate)
	lines := []laid{{n: 1, pcm: line, first: 0, frames: trackRate, kept: trackRate},
		{n: 2, pcm: line, first: total / 2, frames: trackRate, kept: trackRate}}

	var before, after runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)
	out := filepath.Join(dir, "track.wav")
	if err := writeTrack(out, total, lines); err != nil {
		t.Fatal(err)
	}
	runtime.ReadMemStats(&after)
	info, err := os.Stat(out)
	if err != nil || info.Size() != 44+2*total {
		t.Fatalf("the track is %v bytes, want %d: %v", info.Size(), 44+2*total, err)
	}
	if grew := after.TotalAlloc - before.TotalAlloc; grew > 32<<20 {
		t.Errorf("writing a %d MB track allocated %d MB", info.Size()>>20, grew>>20)
	}
}
