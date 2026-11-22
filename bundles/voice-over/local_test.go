package main

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/JonasPf/agent-plugins/internal/servetest"
)

// "With no key and the backend left at auto, the lines are spoken on this
// machine, by Kokoro through sherpa-onnx, downloaded the first time it is
// needed. The result names the backend, model and voice; it costs nothing."
func TestRenderWithNoKeySpeaksOnThisMachine(t *testing.T) {
	dir := t.TempDir()
	servetest.InDir(t, dir)
	bands(t, "clip.mov", true, "red:4", "blue:4")
	t.Setenv(keyName, "")
	t.Setenv(backendName, "")
	d, cache := withLocalModels(t)
	writeScript(t, [3]float64{1, 440, 1}, [3]float64{4, 880, 1})

	res := render(t, `{"video":"clip.mov"}`)
	if res.Error {
		t.Fatalf("voiceover_render: %s", res.Text)
	}
	for _, want := range []string{"Downloaded", "sherpa-onnx 1.13.8", "Kokoro 82M",
		"Spoken by this machine, " + localSpeech + ", voice af_heart: 2 lines spoken now", "$0.0000"} {
		if !strings.Contains(res.Text, want) {
			t.Errorf("the result does not say %q:\n%s", want, res.Text)
		}
	}
	pcm := track(t, dir)
	if hz := pitch(pcm, 1.2, 1.8); !near(hz, 440, 10) {
		t.Errorf("the first line plays at %.0f Hz, want its 440 Hz tone where it was placed", hz)
	}
	k := filepath.Join(cache, kokoro.name)
	args := mustRead(t, filepath.Join(dir, "tts-args.txt"))
	for _, want := range []string{"--kokoro-model=" + filepath.Join(k, "model.int8.onnx"), "--kokoro-voices=" + filepath.Join(k, "voices.bin"),
		"--kokoro-tokens=" + filepath.Join(k, "tokens.txt"), "--kokoro-lexicon=" + filepath.Join(k, "lexicon-us-en.txt"),
		"--kokoro-data-dir=" + filepath.Join(k, "espeak-ng-data"), "--sid=3", "tone 440 1", "tone 880 1"} {
		if !strings.Contains(args, want+"\n") {
			t.Errorf("the speech program was not given %s:\n%s", want, args)
		}
	}
	if got, _ := d.asked(); len(got) != 2 || strings.Contains(strings.Join(got, " "), "parakeet") {
		t.Errorf("downloaded %v, want the helper and Kokoro only", got)
	}

	// A British voice is spoken with the British lexicon.
	res = render(t, `{"video":"clip.mov","voice":"bm_george"}`)
	if res.Error || !strings.Contains(res.Text, "voice bm_george: 2 lines spoken now") || strings.Contains(res.Text, "Downloaded") {
		t.Fatalf("voiceover_render in bm_george: %+v", res)
	}
	args = mustRead(t, filepath.Join(dir, "tts-args.txt"))
	if !strings.Contains(args, "--sid=26\n") || !strings.Contains(args, "--kokoro-lexicon="+filepath.Join(k, "lexicon-gb-en.txt")+"\n") {
		t.Errorf("bm_george was not spoken as Kokoro's speaker 26 with the British lexicon:\n%s", args)
	}
}

// "On this machine the script's voices are Kokoro's English ones, free, and
// listing them downloads nothing."
func TestScriptOnThisMachineOffersKokorosVoices(t *testing.T) {
	_, _ = narrated(t, "100", talk)
	t.Setenv(keyName, "")
	d, _ := withLocalModels(t)
	cs := servetest.Client(t, manifest, tools, runs, pages...)

	res := servetest.Call(t, cs, "voiceover_script", `{"video":"clip.mov","lines":[
		{"starts_with":"okay so this is","text":"This is the opening."}]}`)
	if res.Error {
		t.Fatalf("voiceover_script: %s", res.Text)
	}
	for _, want := range []string{"in af_heart", "Kokoro 82M", "$0 per million characters", "af_heart, af_jessica", "bm_george, bm_lewis"} {
		if !strings.Contains(res.Text, want) {
			t.Errorf("the result does not say %q:\n%s", want, res.Text)
		}
	}
	if strings.Contains(res.Text, "gareth") || strings.Contains(res.Text, "ef_dora") {
		t.Errorf("a voice not offered on this machine is listed:\n%s", res.Text)
	}
	if got, _ := d.asked(); len(got) != 0 {
		t.Errorf("listing the voices downloaded %v", got)
	}
}

// "On this machine the review page offers Kokoro's voices and English only,
// the one language they are spoken in here, and Send refuses another."
func TestReviewOnThisMachineOffersEnglishOnly(t *testing.T) {
	dir := t.TempDir()
	servetest.InDir(t, dir)
	bands(t, "clip.mov", true, "red:4")
	t.Setenv(keyName, "")
	t.Setenv(backendName, "local")
	withLocalModels(t)
	writeScript(t, [3]float64{1, 440, 1})
	cs := pageClient(t)

	got := data(t, cs)
	if len(got.Voices) != 1 || got.Voices[0].Model != localSpeech || got.Voices[0].Price != 0 || len(got.Voices[0].Voices) != 28 {
		t.Errorf("voices = %+v, want Kokoro's 28 English voices, free", got.Voices)
	}
	if got.Voice != "af_heart" || got.Model != localSpeech {
		t.Errorf("voice %s on %s, want af_heart on %s", got.Voice, got.Model, localSpeech)
	}
	if len(got.Languages) != 1 || got.Languages[0] != [2]string{"en", "English"} {
		t.Errorf("languages = %v, want English only", got.Languages)
	}
	res := servetest.Call(t, cs, "voiceover_save_review", `{"video":"clip.mov","voice":"af_heart","model":"`+localSpeech+`","language":"de","lines":[{"at":1,"text":"Hallo"}]}`)
	if !res.Error || !strings.Contains(res.Text, "de") {
		t.Errorf("Send with German on this machine: %+v, want a refusal", res)
	}
}
