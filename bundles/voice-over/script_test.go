package main

import (
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/JonasPf/agent-plugins/internal/servetest"
)

// offeredVoices answers the gateway's list of speaking models as it stood on
// 2026-11-13, cut to what the tests need: the two models offered, a third
// that is not, and a free one.
const offeredVoices = `{"data":[
	{"id":"heygen/heygen-voice-1-turbo","name":"HeyGen: HeyGen Voice Turbo","architecture":{"output_modalities":["speech"]},
	 "supported_voices":["bram","gareth","meredith"],"pricing":{"prompt":"0.000004"}},
	{"id":"heygen/heygen-voice-1-turbo:free","name":"HeyGen (free)","architecture":{"output_modalities":["speech"]},
	 "supported_voices":["gareth"],"pricing":{"prompt":"0"}},
	{"id":"elevenlabs/eleven-flash-v2.5","name":"ElevenLabs: Eleven Flash v2.5","architecture":{"output_modalities":["speech"]},
	 "supported_voices":["alice","brian"],"pricing":{"prompt":"0.00004"}},
	{"id":"microsoft/mai-voice-2.1","name":"Microsoft: MAI Voice 2.1","architecture":{"output_modalities":["speech"]},
	 "supported_voices":["ava"],"pricing":{"prompt":"0.000022"}}]}`

// narrated is a video with sound, its transcript written as voiceover_transcribe
// would have, and a stand-in gateway that lists the voices.
func narrated(t *testing.T, seconds string, transcript string) (string, *gateway) {
	dir := t.TempDir()
	servetest.InDir(t, dir)
	bands(t, "clip.mov", true, "red:"+seconds)
	if err := os.MkdirAll("clip.voiceover", 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join("clip.voiceover", "segments.txt"), []byte(transcript), 0o644); err != nil {
		t.Fatal(err)
	}
	g := withGateway(t)
	g.on("/models", answer(200, offeredVoices))
	return dir, g
}

func readScript(t *testing.T, dir string) []map[string]any {
	t.Helper()
	var lines []map[string]any
	if err := json.Unmarshal([]byte(mustRead(t, filepath.Join(dir, "clip.voiceover", "script.json"))), &lines); err != nil {
		t.Fatal(err)
	}
	return lines
}

const talk = "[2.00–4.00] okay so this is the opening\n" +
	"[33.00–36.00] and here is the middle part\n" +
	"[80.00–81.00] and the end\n" +
	"[81.00–84.00] well the very last words\n"

// "Each line starts where the narrator said its first words, found in the
// transcript in order; script.json holds those times and the words, and the
// table shows what the narrator says at each start."
func TestScriptPlacesEachLineWhereItsWordsAre(t *testing.T) {
	dir, _ := narrated(t, "100", talk)
	cs := servetest.Client(t, manifest, tools, runs)

	res := servetest.Call(t, cs, "voiceover_script", `{"video":"clip.mov","lines":[
		{"starts_with":"okay so this is","text":"This is the opening."},
		{"starts_with":"and here is the","text":"Here is the middle part."},
		{"starts_with":"well the very last","text":"The very last words."}]}`)
	if res.Error {
		t.Fatalf("voiceover_script: %s", res.Text)
	}
	got := readScript(t, dir)
	want := []map[string]any{
		{"at": 2.0, "text": "This is the opening."},
		{"at": 33.0, "text": "Here is the middle part."},
		{"at": 81.0, "text": "The very last words."},
	}
	if len(got) != len(want) {
		t.Fatalf("script.json = %v", got)
	}
	for i := range want {
		if got[i]["at"] != want[i]["at"] || got[i]["text"] != want[i]["text"] || len(got[i]) != 2 {
			t.Errorf("line %d = %v, want %v: times and words only", i+1, got[i], want[i])
		}
	}
	for _, want := range []string{"okay so this is the opening", "and here is the middle part", "well the very last words",
		"clip.voiceover/script.json"} {
		if !strings.Contains(res.Text, want) {
			t.Errorf("the table does not show %q:\n%s", want, res.Text)
		}
	}
}

// "A start is found from a word on: within a phrase, the words are spread
// evenly across its time."
func TestScriptFindsAStartInsideAPhrase(t *testing.T) {
	dir, _ := narrated(t, "20", "[0.00–8.00] one two three four five six seven eight\n")
	cs := servetest.Client(t, manifest, tools, runs)

	res := servetest.Call(t, cs, "voiceover_script", `{"video":"clip.mov","lines":[
		{"starts_with":"one two three","text":"One two three."},
		{"starts_with":"five six seven","text":"Five six seven eight."}]}`)
	if res.Error {
		t.Fatalf("voiceover_script: %s", res.Text)
	}
	if got := readScript(t, dir); got[1]["at"] != 4.0 {
		t.Errorf("the second line starts at %v, want 4: the fifth of eight words over eight seconds", got[1]["at"])
	}
}

// "A start found only nearly is marked ~, and a line the next one will
// probably cut off is marked TIGHT."
func TestScriptMarksANearMatchAndATightLine(t *testing.T) {
	_, _ = narrated(t, "40", "[1.00–3.00] so we use block demand for this\n[4.00–6.00] then we move on\n")
	cs := servetest.Client(t, manifest, tools, runs)

	res := servetest.Call(t, cs, "voiceover_script", `{"video":"clip.mov","lines":[
		{"starts_with":"so we use blockdaemon","text":"We use Blockdaemon for this, and a great many other things besides, all day long."},
		{"starts_with":"then we move","text":"Then we move on."}]}`)
	if res.Error {
		t.Fatalf("voiceover_script: %s", res.Text)
	}
	rows := strings.Split(res.Text, "\n")
	var first, second string
	for _, r := range rows {
		if strings.HasPrefix(r, "01 ") {
			first = r
		}
		if strings.HasPrefix(r, "02 ") {
			second = r
		}
	}
	// The nearest window is the one a word later, as Python's difflib finds
	// it: "weuseblockdemand" is 0.824 like the words wanted, the window at
	// "so" only 0.8. A port that disagreed would place lines elsewhere than
	// the skill did.
	if !strings.Contains(first, "~ we use block demand for this") {
		t.Errorf("a near match is not marked ~: %q", first)
	}
	if !strings.HasSuffix(first, "TIGHT") {
		t.Errorf("a line of 15 words with 3 seconds to say them is not TIGHT: %q", first)
	}
	if strings.Contains(second, "TIGHT") || strings.Contains(second, "~") {
		t.Errorf("an exact, roomy line is marked: %q", second)
	}
}

// "A video with no narration has its lines placed by time, with no transcript."
func TestScriptPlacesLinesByTimeWithoutATranscript(t *testing.T) {
	dir := t.TempDir()
	servetest.InDir(t, dir)
	bands(t, "clip.mov", false, "red:30")
	g := withGateway(t)
	g.on("/models", answer(200, offeredVoices))
	cs := servetest.Client(t, manifest, tools, runs)

	res := servetest.Call(t, cs, "voiceover_script", `{"video":"clip.mov","lines":[{"at":1.5,"text":"First."},{"at":12,"text":"Second."}]}`)
	if res.Error {
		t.Fatalf("voiceover_script: %s", res.Text)
	}
	if got := readScript(t, dir); got[0]["at"] != 1.5 || got[1]["at"] != 12.0 {
		t.Errorf("script.json = %v", got)
	}
	if !strings.Contains(res.Text, "(set by time)") {
		t.Errorf("a line placed by time does not say so:\n%s", res.Text)
	}
}

// "The result says what speaking the lines will cost in the voice chosen,
// and offers the voices of the two models there are to choose from, not
// every model the gateway lists."
func TestScriptSaysWhatSpeakingWillCostAndWhichVoicesThereAre(t *testing.T) {
	_, _ = narrated(t, "100", talk)
	cs := servetest.Client(t, manifest, tools, runs)

	res := servetest.Call(t, cs, "voiceover_script", `{"video":"clip.mov","lines":[
		{"starts_with":"okay so this is","text":"This is the opening."},
		{"starts_with":"and here is the","text":"Here is the middle part."}]}`)
	if res.Error {
		t.Fatalf("voiceover_script: %s", res.Text)
	}
	for _, want := range []string{"2 lines to speak", "44 characters", "$0.0002", "gareth",
		"HeyGen Voice Turbo", "bram, gareth, meredith", "Eleven Flash v2.5", "alice, brian"} {
		if !strings.Contains(res.Text, want) {
			t.Errorf("the result does not say %q:\n%s", want, res.Text)
		}
	}
	if strings.Contains(res.Text, "ava") || strings.Contains(res.Text, "free") {
		t.Errorf("a voice not offered is listed:\n%s", res.Text)
	}
}

// "A line that cannot be placed is refused, naming it, and script.json is
// left as it was."
func TestScriptRefusesALineItCannotPlace(t *testing.T) {
	dir, _ := narrated(t, "100", talk)
	cs := servetest.Client(t, manifest, tools, runs)
	before := `[{"at":1,"text":"kept"}]`
	path := filepath.Join(dir, "clip.voiceover", "script.json")

	long := strings.Repeat("word ", 1000)
	for _, c := range []struct{ why, lines, says string }{
		{"no text", `[{"starts_with":"okay so this is","text":"  "}]`, "line 1 has no text"},
		{"too long for one call", `[{"starts_with":"okay so this is","text":"` + long + `"}]`, "split it"},
		{"too few words to find", `[{"starts_with":"okay so","text":"x"}]`, "at least 3 words"},
		{"words never said", `[{"starts_with":"this was never said","text":"x"}]`, "is not in the transcript after 0.0 s"},
		{"words said only earlier", `[{"starts_with":"and here is","text":"a"},{"starts_with":"okay so this is","text":"b"}]`,
			"line 2: \"okay so this is\" is not in the transcript after 33.0 s"},
		{"a time before the line ahead", `[{"starts_with":"and here is","text":"a"},{"at":10,"text":"b"}]`, "line 2 starts at 10.0 s, before line 1"},
		{"a time after the end", `[{"at":100.5,"text":"a"}]`, "after the video's end at 100.0 s"},
		{"no lines", `[]`, "no lines"},
	} {
		if err := os.WriteFile(path, []byte(before), 0o644); err != nil {
			t.Fatal(err)
		}
		res := servetest.Call(t, cs, "voiceover_script", `{"video":"clip.mov","lines":`+c.lines+`}`)
		if !res.Error || !strings.Contains(res.Text, c.says) {
			t.Errorf("%s: %+v, want a refusal saying %q", c.why, res, c.says)
		}
		if got := mustRead(t, path); got != before {
			t.Errorf("%s: script.json was changed to %s", c.why, got)
		}
	}
}

// "A line placed by its words needs the transcript; without one the refusal
// says to transcribe first."
func TestScriptWithWordsButNoTranscriptSaysToTranscribe(t *testing.T) {
	dir := t.TempDir()
	servetest.InDir(t, dir)
	bands(t, "clip.mov", true, "red:10")
	g := withGateway(t)
	g.on("/models", answer(200, offeredVoices))
	cs := servetest.Client(t, manifest, tools, runs)

	res := servetest.Call(t, cs, "voiceover_script", `{"video":"clip.mov","lines":[{"starts_with":"okay so this","text":"x"}]}`)
	if !res.Error || !strings.Contains(res.Text, "voiceover_transcribe") {
		t.Errorf("voiceover_script = %+v, want a refusal sending it to voiceover_transcribe", res)
	}
}

// "Writing the script settles a translation that was owed: the lines are the
// translation."
func TestScriptClearsAnOwedTranslation(t *testing.T) {
	dir, _ := narrated(t, "100", talk)
	proj := filepath.Join(dir, "clip.voiceover", "project.json")
	if err := os.WriteFile(proj, []byte(`{"voice":"alice","model":"elevenlabs/eleven-flash-v2.5","translate_to":"de"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	cs := servetest.Client(t, manifest, tools, runs)

	res := servetest.Call(t, cs, "voiceover_script", `{"video":"clip.mov","lines":[{"starts_with":"okay so this is","text":"Das ist der Anfang."}]}`)
	if res.Error {
		t.Fatalf("voiceover_script: %s", res.Text)
	}
	var p map[string]any
	if err := json.Unmarshal([]byte(mustRead(t, proj)), &p); err != nil {
		t.Fatal(err)
	}
	if _, owed := p["translate_to"]; owed || p["voice"] != "alice" {
		t.Errorf("project.json = %v, want the voice kept and no translation owed", p)
	}
	if !strings.Contains(res.Text, "alice") {
		t.Errorf("the cost is not given in the voice chosen:\n%s", res.Text)
	}
}

// "Nearness is measured as Python's difflib measures it, which is how the
// skill placed lines: these are difflib's own answers."
func TestRatioIsDifflibs(t *testing.T) {
	for _, c := range []struct {
		a, b string
		want float64
	}{
		{"soweuseblockdaemon", "soweuseblock", 0.8},
		{"soweuseblockdaemon", "weuseblockdemand", 0.8235},
		{"soweuseblockdaemon", "useblockdemandfor", 0.6857},
		{"abcd", "bcda", 0.75},
		{"okaysothis", "okaysothisis", 0.9091},
		{"", "", 1},
	} {
		if got := ratio([]rune(c.a), []rune(c.b)); math.Abs(got-c.want) > 0.0001 {
			t.Errorf("ratio(%q, %q) = %.4f, difflib says %.4f", c.a, c.b, got, c.want)
		}
	}
}
