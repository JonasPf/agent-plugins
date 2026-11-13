package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/JonasPf/agent-plugins/internal/serve"
	"github.com/JonasPf/agent-plugins/internal/servetest"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// pageSays is what voiceover_review_data hands the page.
type pageSays struct {
	Video    string  `json:"video"`
	Duration float64 `json:"duration"`
	Step     int     `json:"step"`
	Shots    []string
	Voices   []struct {
		Name   string   `json:"name"`
		Model  string   `json:"model"`
		Price  float64  `json:"price"`
		Voices []string `json:"voices"`
	} `json:"voices"`
	Model  string `json:"model"`
	Voice  string `json:"voice"`
	Spoken *struct {
		Model string `json:"model"`
		Voice string `json:"voice"`
	} `json:"spoken"`
	Languages [][2]string `json:"languages"`
	Lines     []struct {
		At     float64  `json:"at"`
		Text   string   `json:"text"`
		Length *float64 `json:"length"`
		Audio  string   `json:"audio"`
	} `json:"lines"`
	Pace     float64                      `json:"pace"`
	MaxChars int                          `json:"max_chars"`
	Samples  map[string]map[string]string `json:"samples"`
}

func pageClient(t *testing.T) *mcp.ClientSession {
	return servetest.Client(t, manifest, tools, runs, pages...)
}

// spokenLine makes the speech of a line in a voice, as voiceover_render would
// have kept it: a tone with the silence a voice leaves at its ends.
func spokenLine(t *testing.T, video string, v Voice, text string, seconds string) {
	t.Helper()
	p, err := openProject(video)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(p.dir, "speech"), 0o755); err != nil {
		t.Fatal(err)
	}
	out := p.speechOf(v, text) + ".wav"
	if b, err := exec.Command("ffmpeg", "-y", "-hide_banner", "-loglevel", "error",
		"-f", "lavfi", "-i", "anullsrc=r=24000:cl=mono:d=0.4", "-f", "lavfi", "-i", "sine=f=220:r=24000:d="+seconds,
		"-f", "lavfi", "-i", "anullsrc=r=24000:cl=mono:d=0.6", "-filter_complex", "concat=n=3:v=0:a=1", out).CombinedOutput(); err != nil {
		t.Fatalf("could not make the speech: %v\n%s", err, b)
	}
}

// "voiceover_review names its page, served as an MCP App; the two tools the
// page calls are the page's alone, never shown to the model."
func TestReviewNamesItsPageAndKeepsItsHelpersForThePage(t *testing.T) {
	cs := pageClient(t)
	listed, err := cs.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	meta := map[string]string{}
	for _, tool := range listed.Tools {
		b, _ := json.Marshal(tool.Meta)
		meta[tool.Name] = string(b)
	}
	if meta["voiceover_review"] != `{"ui":{"resourceUri":"ui://voice-over/review"}}` {
		t.Errorf("voiceover_review's _meta = %s", meta["voiceover_review"])
	}
	for _, name := range []string{"voiceover_review_data", "voiceover_save_review"} {
		if meta[name] != `{"ui":{"visibility":["app"]}}` {
			t.Errorf("%s's _meta = %s, want it for the page only", name, meta[name])
		}
	}
	res, err := cs.ReadResource(context.Background(), &mcp.ReadResourceParams{URI: "ui://voice-over/review"})
	if err != nil {
		t.Fatal(err)
	}
	if c := res.Contents[0]; c.MIMEType != serve.AppMIME || !strings.Contains(c.Text, "voiceover_save_review") ||
		!strings.Contains(c.Text, "ui/initialize") {
		t.Errorf("the page is %s, %d bytes, and does not speak to its host", c.MIMEType, len(c.Text))
	}
}

// "voiceover_review tells the model the page is ready and that its turn ends
// here; it works with no script, when the operator writes it on the page."
func TestReviewTellsTheModelToEndItsTurn(t *testing.T) {
	_, _ = scripted(t, [3]float64{1, 440, 1})
	cs := pageClient(t)

	res := servetest.Call(t, cs, "voiceover_review", `{"video":"clip.mov"}`)
	if res.Error || !strings.Contains(res.Text, "review page") || !strings.Contains(res.Text, "End your turn") || len(res.Text) > 1200 {
		t.Errorf("voiceover_review = %+v", res)
	}
	_ = os.Remove(filepath.Join("clip.voiceover", "script.json"))
	res = servetest.Call(t, cs, "voiceover_review", `{"video":"clip.mov"}`)
	if res.Error || !strings.Contains(res.Text, "write the script") {
		t.Errorf("with no script: %+v, want the page offered for the operator to write it", res)
	}
	if res := servetest.Call(t, cs, "voiceover_review", `{"video":"gone.mov"}`); !res.Error {
		t.Errorf("a video that is not there: %+v", res)
	}
}

func data(t *testing.T, cs *mcp.ClientSession) pageSays {
	t.Helper()
	res := servetest.Call(t, cs, "voiceover_review_data", `{"video":"clip.mov"}`)
	if res.Error {
		t.Fatalf("voiceover_review_data: %s", res.Text)
	}
	var d pageSays
	if err := json.Unmarshal([]byte(res.Text), &d); err != nil {
		t.Fatalf("the page's data is not JSON: %v", err)
	}
	return d
}

// "The page's data: the video's length, a screenshot every 30 seconds, the
// voices of both models with their prices and samples, the languages, the
// limits, and each line, with its real length and its speech when it is
// spoken in the voice chosen."
func TestReviewDataHoldsWhatThePageShows(t *testing.T) {
	dir := t.TempDir()
	servetest.InDir(t, dir)
	bands(t, "clip.mov", true, "red:30", "green:30", "blue:5")
	g := withGateway(t)
	g.on("/models", answer(200, offeredVoices))
	lines := []scriptLine{{1, "The first line."}, {40, "The second line."}}
	if err := (project{dir: filepath.Join(dir, "clip.voiceover")}).writeScript(lines); err != nil {
		t.Fatal(err)
	}
	spokenLine(t, "clip.mov", Voice{Name: "gareth", Model: defaultSpeech}, "The first line.", "2")
	cs := pageClient(t)

	d := data(t, cs)
	if d.Video != "clip.mov" || d.Duration < 64.9 || d.Duration > 65.1 || d.Step != 30 || len(d.Shots) != 3 {
		t.Fatalf("video %q, %.1f s, every %d s, %d shots", d.Video, d.Duration, d.Step, len(d.Shots))
	}
	for i, s := range d.Shots {
		jpeg, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(s, "data:image/jpeg;base64,"))
		if !strings.HasPrefix(s, "data:image/jpeg;base64,") || err != nil || len(jpeg) < 100 || jpeg[0] != 0xFF || jpeg[1] != 0xD8 {
			t.Errorf("screenshot %d is not a JPEG data URL", i)
		}
	}
	if len(d.Voices) != 2 || d.Voices[0].Model != defaultSpeech || d.Voices[0].Price != 4 ||
		strings.Join(d.Voices[0].Voices, ",") != "bram,gareth,meredith" || d.Voices[1].Name != "ElevenLabs: Eleven Flash v2.5" {
		t.Errorf("voices = %+v", d.Voices)
	}
	if d.Voice != "gareth" || d.Model != defaultSpeech || d.Spoken == nil || d.Spoken.Voice != "gareth" {
		t.Errorf("voice %s on %s, spoken %+v", d.Voice, d.Model, d.Spoken)
	}
	first, second := d.Lines[0], d.Lines[1]
	if first.Text != "The first line." || first.At != 1 || first.Length == nil || *first.Length < 1.9 || *first.Length > 2.1 {
		t.Errorf("the spoken line = %+v, want its speech's length without the silence at its ends", first)
	}
	mp3, err := base64.StdEncoding.DecodeString(first.Audio)
	if err != nil || len(mp3) < 100 || mp3[0] != 0xFF || mp3[1]&0xE0 != 0xE0 {
		t.Errorf("the spoken line's audio is not MP3 the page can play")
	}
	if second.Length != nil || second.Audio != "" {
		t.Errorf("a line not spoken has a length or speech: %+v", second)
	}
	if len(d.Languages) != 7 || d.Languages[1] != [2]string{"de", "German"} || d.Pace != 2.3 || d.MaxChars != 4500 {
		t.Errorf("languages %v, pace %v, most characters %d", d.Languages, d.Pace, d.MaxChars)
	}
	if len(d.Samples[defaultSpeech]) != 10 || len(d.Samples["elevenlabs/eleven-flash-v2.5"]) != 21 {
		t.Errorf("samples for %d and %d voices, want every voice of both", len(d.Samples[defaultSpeech]), len(d.Samples["elevenlabs/eleven-flash-v2.5"]))
	}
}

// "With no script the page starts empty, for the operator to write it."
func TestReviewDataStartsEmptyWithoutAScript(t *testing.T) {
	dir := t.TempDir()
	servetest.InDir(t, dir)
	bands(t, "clip.mov", false, "red:10")
	g := withGateway(t)
	g.on("/models", answer(200, offeredVoices))
	cs := pageClient(t)

	d := data(t, cs)
	if d.Lines == nil || len(d.Lines) != 0 || d.Spoken != nil || len(d.Shots) != 1 || d.Voice != defaultVoice {
		t.Errorf("lines %v, spoken %v, %d shots, voice %s", d.Lines, d.Spoken, len(d.Shots), d.Voice)
	}
}

// "Send writes the script and the voice, every line timed and on one line,
// and its answer is what the conversation is told: how many lines, what is
// to be spoken and what it costs, and what to do next."
func TestSaveReviewWritesTheScriptAndSaysWhatComesNext(t *testing.T) {
	dir, _ := scripted(t)
	spokenLine(t, "clip.mov", Voice{Name: "gareth", Model: defaultSpeech}, "First line.", "1")
	cs := pageClient(t)

	res := servetest.Call(t, cs, "voiceover_save_review", `{"video":"clip.mov","voice":"gareth","model":"heygen/heygen-voice-1-turbo",
		"lines":[{"at":1.5,"text":" First   line. "},{"at":4,"text":"Second\nline."}]}`)
	if res.Error {
		t.Fatalf("voiceover_save_review: %s", res.Text)
	}
	got := readScript(t, dir)
	if len(got) != 2 || got[0]["at"] != 1.5 || got[0]["text"] != "First line." || got[1]["text"] != "Second line." {
		t.Errorf("script.json = %v", got)
	}
	var p choices
	_ = json.Unmarshal([]byte(mustRead(t, filepath.Join(dir, "clip.voiceover", "project.json"))), &p)
	if p != (choices{Voice: "gareth", Model: defaultSpeech}) {
		t.Errorf("project.json = %+v", p)
	}
	for _, want := range []string{"2 lines", "1 to speak", "1 keeps its speech", "gareth", "voiceover_render", "voiceover_review", "clip.mov"} {
		if !strings.Contains(res.Text, want) {
			t.Errorf("Send's message does not say %q:\n%s", want, res.Text)
		}
	}

	res = servetest.Call(t, cs, "voiceover_save_review", `{"video":"clip.mov","voice":"alice","model":"elevenlabs/eleven-flash-v2.5",
		"language":"de","lines":[{"at":1.5,"text":"First line."}]}`)
	if res.Error {
		t.Fatalf("with a language: %s", res.Text)
	}
	_ = json.Unmarshal([]byte(mustRead(t, filepath.Join(dir, "clip.voiceover", "project.json"))), &p)
	if p.TranslateTo != "de" || p.Voice != "alice" {
		t.Errorf("project.json = %+v, want a translation into de owed", p)
	}
	for _, want := range []string{"German", "translate", "keep every at", "voiceover_script", "script.json"} {
		if !strings.Contains(res.Text, want) {
			t.Errorf("Send's message with a language does not say %q:\n%s", want, res.Text)
		}
	}
}

// "What cannot be spoken is refused, and nothing is written."
func TestSaveReviewRefusesWhatCannotBeSpoken(t *testing.T) {
	dir, _ := scripted(t, [3]float64{1, 440, 1})
	cs := pageClient(t)
	script := filepath.Join(dir, "clip.voiceover", "script.json")
	before := mustRead(t, script)
	long := strings.Repeat("word ", 1000)
	for _, c := range []struct{ why, args, says string }{
		{"an unknown voice", `"voice":"nobody","model":"heygen/heygen-voice-1-turbo","lines":[{"at":1,"text":"x"}]`, "nobody"},
		{"a voice of the other model", `"voice":"alice","model":"heygen/heygen-voice-1-turbo","lines":[{"at":1,"text":"x"}]`, "alice"},
		{"an unknown language", `"voice":"gareth","model":"heygen/heygen-voice-1-turbo","language":"xx","lines":[{"at":1,"text":"x"}]`, "xx"},
		{"no lines", `"voice":"gareth","model":"heygen/heygen-voice-1-turbo","lines":[]`, "no lines"},
		{"an empty line", `"voice":"gareth","model":"heygen/heygen-voice-1-turbo","lines":[{"at":1,"text":" "}]`, "no text"},
		{"a line too long", `"voice":"gareth","model":"heygen/heygen-voice-1-turbo","lines":[{"at":1,"text":"` + long + `"}]`, "split it"},
		{"a line after the end", `"voice":"gareth","model":"heygen/heygen-voice-1-turbo","lines":[{"at":9,"text":"x"}]`, "after the video's end"},
		{"a line before the start", `"voice":"gareth","model":"heygen/heygen-voice-1-turbo","lines":[{"at":-1,"text":"x"}]`, "before the video starts"},
		{"lines out of order", `"voice":"gareth","model":"heygen/heygen-voice-1-turbo","lines":[{"at":3,"text":"a"},{"at":2,"text":"b"}]`, "before line 1"},
	} {
		res := servetest.Call(t, cs, "voiceover_save_review", `{"video":"clip.mov",`+c.args+`}`)
		if !res.Error || !strings.Contains(res.Text, c.says) {
			t.Errorf("%s: %+v, want a refusal saying %q", c.why, res, c.says)
		}
		if got := mustRead(t, script); got != before {
			t.Errorf("%s: script.json was changed", c.why)
		}
	}
}
