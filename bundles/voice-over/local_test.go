package main

import (
	"fmt"
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

// "A recording is heard in chunks of 30 s that overlap by 2: a word in the
// overlap is kept once, from the chunk where it is further from the edge, at
// its time in the whole recording."
// heard is what the helper says of a chunk whose words, each "at:text",
// last 0.3 s.
func heard(words ...string) chunkHeard {
	var c chunkHeard
	for _, w := range words {
		var at float64
		var text string
		fmt.Sscanf(w, "%g:%s", &at, &text)
		c.Tokens = append(c.Tokens, " "+text)
		c.Timestamps = append(c.Timestamps, at)
		c.Durations = append(c.Durations, 0.3)
	}
	return c
}

func wordsOf(chunks ...chunkHeard) string {
	var said []string
	for _, w := range placeWords(chunks) {
		said = append(said, fmt.Sprintf("%g:%s", w.start, w.text))
	}
	return strings.Join(said, " ")
}

func TestLocalWordsAreKeptOnceAcrossChunks(t *testing.T) {
	// Chunk 0 is 0–30 s and chunk 1 is 28–58 s: they meet at 29 s.
	got := wordsOf(
		heard("1:one", "27.5:two", "28.8:three", "29.4:fou"),
		heard("0.2:ree", "1.4:four", "20:five"),
	)
	if want := "1:one 27.5:two 28.8:three 29.4:four 48:five"; got != want {
		t.Errorf("words = %s, want %s", got, want)
	}
}

// Two chunks place the same word a few hundredths of a second apart, so a
// word said at the cut may start before it in one chunk and after it in the
// other. The real run of 23 Nov 2026 wrote "we will will look" so.
func TestLocalAWordSaidAtTheCutIsKeptOnce(t *testing.T) {
	for _, c := range []struct {
		name   string
		chunks []chunkHeard
		want   string
	}{
		{"before the cut in the first chunk, after it in the second",
			[]chunkHeard{heard("27.5:we", "28.95:will"), heard("0.4:we", "1.05:will", "1.4:look")},
			"27.5:we 28.95:will 29.4:look"},
		{"after the cut in the first chunk, before it in the second",
			[]chunkHeard{heard("28.6:we", "29.03:will", "29.5:loo"), heard("0.6:we", "0.97:will", "1.5:look")},
			"28.6:we 28.97:will 29.5:look"},
		{"written differently by the two chunks",
			[]chunkHeard{heard("27.9:North", "28.5:Automato"), heard("0.5:tomato", "1.2:row")},
			"27.9:North 28.5:Automato 29.2:row"},
		{"written differently, and said past the cut",
			[]chunkHeard{heard("27.9:North", "28.9:Automato"), heard("1.0:tomato", "1.6:row")},
			"27.9:North 28.9:Automato 29.6:row"},
		{"said twice",
			[]chunkHeard{heard("28.5:that", "28.8:that"), heard("0.5:that", "0.8:that", "1.1:one")},
			"28.5:that 28.8:that 29.1:one"},
	} {
		if got := wordsOf(c.chunks...); got != c.want {
			t.Errorf("%s: words = %s, want %s", c.name, got, c.want)
		}
	}
}

// "A phrase ends at a sentence's end, at a pause, and at a comma once it is
// four seconds long; a token without a space before it is part of the word
// before."
func TestLocalPhrasesEndAtSentencesPausesAndLongCommas(t *testing.T) {
	type tok struct {
		text      string
		at, lasts float64
	}
	var c chunkHeard
	for _, k := range []tok{{" Open", 0, 0.25}, {" the", 0.3, 0.25}, {" app", 0.6, 0.25}, {".", 0.85, 0.1},
		{" Then", 1.2, 0.25}, {" wait", 1.5, 0.3},
		{" for", 3, 0.3}, {" it", 3.4, 0.3}, {",", 3.7, 0.1}, {" and", 4, 0.3}, {" when", 4.5, 0.3}, {" it", 5, 0.3},
		{" starts", 5.5, 0.4}, {" to", 6, 0.2}, {" beep", 6.4, 0.4}, {",", 6.8, 0.25},
		{" press", 7.2, 0.3}, {" go", 7.6, 0.3}} {
		c.Tokens = append(c.Tokens, k.text)
		c.Timestamps = append(c.Timestamps, k.at)
		c.Durations = append(c.Durations, k.lasts)
	}
	var got []string
	for _, s := range phrases(placeWords([]chunkHeard{c})) {
		got = append(got, fmt.Sprintf("[%.2f–%.2f] %s", s.Start, s.End, s.Text))
	}
	want := []string{"[0.00–0.95] Open the app.", "[1.20–1.80] Then wait", "[3.00–7.05] for it, and when it starts to beep,", "[7.20–7.90] press go"}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("phrases:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}
