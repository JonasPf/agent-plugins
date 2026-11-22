package main

import (
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/JonasPf/agent-plugins/internal/servetest"
)

// withGateway points the bundle's settings at a stand-in gateway.
func withGateway(t *testing.T) *gateway {
	g, srv := newGateway(t)
	t.Setenv(keyName, "sk-test-secret")
	t.Setenv(urlName, srv.URL)
	return g
}

// shorter lowers the lengths the tool stops and cuts at, so a test's short
// video can stand in for an hour.
func shorter(t *testing.T, long, part float64) {
	oldLong, oldPart := longAfter, partSeconds
	longAfter, partSeconds = long, part
	t.Cleanup(func() { longAfter, partSeconds = oldLong, oldPart })
}

// "voiceover_transcribe takes the video's sound into the project, has it
// written down with the time of every phrase, saves that as segments.txt, and
// says what the video is, who transcribed it and what it cost."
func TestTranscribeWritesTheTranscriptBesideTheVideo(t *testing.T) {
	dir := t.TempDir()
	servetest.InDir(t, dir)
	bands(t, "clip.mov", true, "red:3", "green:3")
	g := withGateway(t)
	g.on("/audio/transcriptions", answer(200, `{"text":"So in this case we use Blockdaemon.",
		"segments":[{"start":0,"end":2.5,"text":" So in this case"},{"start":2.5,"end":4.1,"text":" we use Blockdaemon."}],
		"usage":{"cost":0.0003}}`))
	cs := servetest.Client(t, manifest, tools, runs, pages...)

	res := servetest.Call(t, cs, "voiceover_transcribe", `{"video":"clip.mov","language":"en","terms":"Blockdaemon"}`)
	if res.Error {
		t.Fatalf("voiceover_transcribe: %s", res.Text)
	}
	for _, want := range []string{"clip.mov: 6.0 s, 320x180, 30 fps, with sound", "ffmpeg: ",
		"OpenRouter", defaultTranscriber, "$0.0003", "clip.voiceover/segments.txt", "2 lines",
		"[0.00–2.50] So in this case", "[2.50–4.10] we use Blockdaemon."} {
		if !strings.Contains(res.Text, want) {
			t.Errorf("the result does not say %q:\n%s", want, res.Text)
		}
	}
	if strings.Contains(res.Text, "sk-test-secret") {
		t.Error("the result holds the key")
	}
	if got := mustRead(t, filepath.Join(dir, "clip.voiceover", "segments.txt")); got != "[0.00–2.50] So in this case\n[2.50–4.10] we use Blockdaemon.\n" {
		t.Errorf("segments.txt = %q", got)
	}
	// What was sent is the video's sound as the contract has it: AAC, mono,
	// 16 kHz, which ffmpeg encodes with no library of its own.
	said, _ := exec.Command(fullFFmpeg(), "-hide_banner", "-i", filepath.Join(dir, "clip.voiceover", "audio.m4a")).CombinedOutput()
	if !strings.Contains(string(said), "Audio: aac") || !strings.Contains(string(said), "16000 Hz, mono") {
		t.Errorf("audio.m4a is not AAC, mono at 16 kHz:\n%s", said)
	}
	if r := g.asked("/audio/transcriptions")[0]; at(r.body, "language") != "en" || at(r.body, "provider", "options", "deepinfra", "prompt") != "Blockdaemon" ||
		at(r.body, "input_audio", "format") != "m4a" {
		t.Errorf("the language and terms did not reach the model: %v", r.body)
	}
}

// "A video with no sound is said to have none, which is not a failure: the
// script is then written with times. Nothing is asked of the gateway, so no
// key is needed for it."
func TestTranscribeOfAVideoWithNoSoundSaysSo(t *testing.T) {
	dir := t.TempDir()
	servetest.InDir(t, dir)
	bands(t, "silent.mp4", false, "red:3")
	t.Setenv(keyName, "")
	cs := servetest.Client(t, manifest, tools, runs, pages...)

	res := servetest.Call(t, cs, "voiceover_transcribe", `{"video":"silent.mp4"}`)
	if res.Error || !strings.Contains(res.Text, "no sound") || !strings.Contains(res.Text, `"at"`) {
		t.Errorf("voiceover_transcribe = %+v, want a result saying there is no sound and to place lines by time", res)
	}
	if _, err := os.Stat(filepath.Join(dir, "silent.voiceover", "segments.txt")); err == nil {
		t.Error("a transcript was written for a video with no sound")
	}
}

// "Over an hour it stops before anything is sent, saying what the length will
// cost to speak; told it may go on, it sends the recording in parts and joins
// their times into one transcript."
func TestTranscribeStopsAtAnHourAndSendsALongRecordingInParts(t *testing.T) {
	dir := t.TempDir()
	servetest.InDir(t, dir)
	bands(t, "talk.mov", true, "red:3", "green:3")
	shorter(t, 5, 2.5)
	g := withGateway(t)
	g.on("/models", answer(200, `{"data":[{"id":"heygen/heygen-voice-1-turbo","name":"HeyGen: Voice 1 Turbo",
		"architecture":{"output_modalities":["speech"]},"supported_voices":["gareth"],"pricing":{"prompt":"0.000004"}}]}`))
	cs := servetest.Client(t, manifest, tools, runs, pages...)

	res := servetest.Call(t, cs, "voiceover_transcribe", `{"video":"talk.mov"}`)
	if !res.Error || !strings.Contains(res.Text, "long: true") || !strings.Contains(res.Text, "gareth") || !strings.Contains(res.Text, "$") {
		t.Fatalf("voiceover_transcribe = %+v, want a refusal that names the cost and how to go on", res)
	}
	if n := len(g.asked("/audio/transcriptions")); n != 0 {
		t.Fatalf("the recording was sent %d times before the operator agreed", n)
	}

	var parts []http.HandlerFunc
	for i := 0; i < 3; i++ {
		parts = append(parts, answer(200, fmt.Sprintf(`{"text":"part %d","segments":[{"start":0.5,"end":1.5,"text":"part %d"}],"usage":{"cost":0.0001}}`, i, i)))
	}
	g.on("/audio/transcriptions", parts...)
	res = servetest.Call(t, cs, "voiceover_transcribe", `{"video":"talk.mov","long":true}`)
	if res.Error {
		t.Fatalf("voiceover_transcribe with long: %s", res.Text)
	}
	if n := len(g.asked("/audio/transcriptions")); n != 3 {
		t.Errorf("a 6-second recording in 2.5-second parts was sent as %d parts, want 3", n)
	}
	want := "[0.50–1.50] part 0\n[3.00–4.00] part 1\n[5.50–6.50] part 2\n"
	if got := mustRead(t, filepath.Join(dir, "talk.voiceover", "segments.txt")); got != want {
		t.Errorf("segments.txt = %q, want each part's times moved to where it begins: %q", got, want)
	}
	if !strings.Contains(res.Text, "$0.0003") {
		t.Errorf("the cost of the parts is not added up:\n%s", res.Text)
	}
}

// "A model that gives the whole of a long recording as one block is refused
// with that reason: lines cannot be placed in it."
func TestTranscribeRefusesOneBlockForAWholeRecording(t *testing.T) {
	dir := t.TempDir()
	servetest.InDir(t, dir)
	bands(t, "talk.mov", true, "red:70")
	g := withGateway(t)
	g.on("/audio/transcriptions", answer(200, `{"text":"everything","segments":[{"start":0,"end":70,"text":"everything"}]}`))
	cs := servetest.Client(t, manifest, tools, runs, pages...)

	res := servetest.Call(t, cs, "voiceover_transcribe", `{"video":"talk.mov"}`)
	if !res.Error || !strings.Contains(res.Text, "one block") {
		t.Fatalf("voiceover_transcribe = %+v, want a refusal of one block", res)
	}
	if _, err := os.Stat(filepath.Join(dir, "talk.voiceover", "segments.txt")); err == nil {
		t.Error("the block was saved as a transcript")
	}
}

// "Set to OpenRouter and without a key, it says where the operator sets one,
// downloads nothing, and writes nothing."
func TestTranscribeWithoutAKeySaysWhereToSetOne(t *testing.T) {
	dir := t.TempDir()
	servetest.InDir(t, dir)
	bands(t, "clip.mov", true, "red:2")
	t.Setenv(keyName, "")
	t.Setenv(backendName, "openrouter")
	d, _ := withLocalModels(t)
	cs := servetest.Client(t, manifest, tools, runs, pages...)

	res := servetest.Call(t, cs, "voiceover_transcribe", `{"video":"clip.mov"}`)
	if !res.Error || !strings.Contains(res.Text, "OpenRouter API key") {
		t.Fatalf("voiceover_transcribe = %+v, want a refusal naming the key", res)
	}
	if _, err := os.Stat(filepath.Join(dir, "clip.voiceover")); err == nil {
		t.Error("the project was started without a key to finish it")
	}
	if got, _ := d.asked(); len(got) != 0 {
		t.Errorf("set to OpenRouter, it downloaded %v to run on this machine", got)
	}
}

// "With no key and the backend left at auto, it transcribes on this machine:
// it downloads its helper and model the first time, says so and where to, and
// names the backend and model in the result. Nothing is sent anywhere."
func TestTranscribeWithNoKeyRunsOnThisMachine(t *testing.T) {
	dir := t.TempDir()
	servetest.InDir(t, dir)
	bands(t, "clip.mov", true, "red:3", "green:3")
	t.Setenv(keyName, "")
	t.Setenv(backendName, "")
	d, cache := withLocalModels(t)
	cs := servetest.Client(t, manifest, tools, runs, pages...)

	res := servetest.Call(t, cs, "voiceover_transcribe", `{"video":"clip.mov"}`)
	if res.Error {
		t.Fatalf("voiceover_transcribe: %s", res.Text)
	}
	for _, want := range []string{"Downloaded", cache, "sherpa-onnx 1.13.8", "nvidia/parakeet-tdt-0.6b-v3", "Silero VAD v5",
		"Transcribed on this machine", "CC-BY-4.0", "2 lines", "[0.00–2.50] So in this case", "[2.50–4.10] we use Blockdaemon."} {
		if !strings.Contains(res.Text, want) {
			t.Errorf("the result does not say %q:\n%s", want, res.Text)
		}
	}
	if got := mustRead(t, filepath.Join(dir, "clip.voiceover", "segments.txt")); got != "[0.00–2.50] So in this case\n[2.50–4.10] we use Blockdaemon.\n" {
		t.Errorf("segments.txt = %q", got)
	}
	// The helper is given the model it was downloaded with, the voice
	// detector that cuts the sound into phrases, and the sound as it reads
	// it: 16-bit WAV, mono, 16 kHz.
	args := mustRead(t, filepath.Join(dir, "helper-args.txt"))
	for _, want := range []string{"--model-type=nemo_transducer", "--encoder=" + filepath.Join(cache, parakeet.name, "encoder.int8.onnx"),
		"--tokens=" + filepath.Join(cache, parakeet.name, "tokens.txt"), "--silero-vad-model=" + filepath.Join(cache, silero.name, "silero_vad_v5.onnx")} {
		if !strings.Contains(args, want+"\n") {
			t.Errorf("the helper was not given %s:\n%s", want, args)
		}
	}
	said, _ := exec.Command(fullFFmpeg(), "-hide_banner", "-i", filepath.Join(dir, "helper-input.wav")).CombinedOutput()
	if !strings.Contains(string(said), "pcm_s16le") || !strings.Contains(string(said), "16000 Hz, mono") {
		t.Errorf("the helper was not given 16-bit WAV, mono at 16 kHz:\n%s", said)
	}
	left, _ := filepath.Glob(filepath.Join(dir, "clip.voiceover", "*.wav"))
	if len(left) != 0 {
		t.Errorf("the sound made for the helper was left in the project: %v", left)
	}

	res = servetest.Call(t, cs, "voiceover_transcribe", `{"video":"clip.mov"}`)
	if res.Error || strings.Contains(res.Text, "Downloaded") {
		t.Errorf("a second call = %+v, want a transcript and no download", res)
	}
	if got, _ := d.asked(); len(got) != 3 {
		t.Errorf("downloaded %d times over two calls, want the helper, the model and the detector once each: %v", len(got), got)
	}
}

// "The backend setting chooses: openrouter, local, or auto, which is
// OpenRouter when a key is set and this machine when not. Anything else is
// refused, naming the three."
func TestTheBackendSettingChoosesWhereToTranscribe(t *testing.T) {
	dir := t.TempDir()
	servetest.InDir(t, dir)
	bands(t, "clip.mov", true, "red:2")
	g := withGateway(t)
	g.on("/audio/transcriptions", answer(200, `{"text":"hello","segments":[{"start":0,"end":1,"text":"hello"}],"usage":{"cost":0.0001}}`))
	withLocalModels(t)
	cs := servetest.Client(t, manifest, tools, runs, pages...)

	t.Setenv(backendName, "local")
	res := servetest.Call(t, cs, "voiceover_transcribe", `{"video":"clip.mov"}`)
	if res.Error || !strings.Contains(res.Text, "this machine") {
		t.Errorf("set to local with a key: %+v, want it transcribed on this machine", res)
	}
	if n := len(g.asked("/audio/transcriptions")); n != 0 {
		t.Errorf("set to local, the recording was sent to the gateway %d times", n)
	}

	t.Setenv(backendName, "Auto")
	res = servetest.Call(t, cs, "voiceover_transcribe", `{"video":"clip.mov"}`)
	if res.Error || !strings.Contains(res.Text, "OpenRouter") {
		t.Errorf("auto with a key: %+v, want it transcribed by OpenRouter", res)
	}

	t.Setenv(backendName, "cloud")
	res = servetest.Call(t, cs, "voiceover_transcribe", `{"video":"clip.mov"}`)
	if !res.Error || !strings.Contains(res.Text, "auto, openrouter or local") {
		t.Errorf("set to cloud: %+v, want a refusal naming the three settings", res)
	}
}

// "Where no helper is built for the machine, auto with no key says there is
// neither, and how to get a key in."
func TestNoLocalBackendWhereNoHelperIsBuilt(t *testing.T) {
	dir := t.TempDir()
	servetest.InDir(t, dir)
	bands(t, "clip.mov", true, "red:2")
	t.Setenv(keyName, "")
	t.Setenv(backendName, "")
	old := platform
	platform = "plan9/386"
	t.Cleanup(func() { platform = old })
	cs := servetest.Client(t, manifest, tools, runs, pages...)

	res := servetest.Call(t, cs, "voiceover_transcribe", `{"video":"clip.mov"}`)
	if !res.Error || !strings.Contains(res.Text, "plan9/386") || !strings.Contains(res.Text, "OpenRouter API key") {
		t.Errorf("voiceover_transcribe = %+v, want a refusal naming the platform and the key", res)
	}
}

func mustRead(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
