package main

import (
	"context"
	_ "embed"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"

	"github.com/JonasPf/agent-plugins/internal/serve"
)

// The review page: where the operator hears every line at its real length,
// moves and changes lines, and picks the voice and the language, and Send
// saves what they settled on. It is an MCP App: voiceover_review's result
// opens it, and it reaches the server only through the two tools here that a
// model is never shown, one for what it shows and one for Send.

const reviewURI = "ui://voice-over/review"

//go:embed review.html
var reviewPage string

// samplesJSON is every offered voice saying "Hello World 1 2 3", as MP3 in
// base64, by model and voice: what the page plays when a voice is picked.
//
//go:embed samples.json
var samplesJSON []byte

var pages = []serve.Page{{URI: reviewURI, HTML: reviewPage}}

// languages are those both models were tried in, every line spoken and read
// back word for word by Whisper, on 2026-10-31.
var languages = [][2]string{{"en", "English"}, {"de", "German"}, {"es", "Spanish"}, {"fr", "French"},
	{"it", "Italian"}, {"nl", "Dutch"}, {"pt", "Portuguese"}}

const (
	shotEvery   = 30
	mostShots   = 120
	shotWidth   = 800
	shotQuality = "6"
	// pageBudget is the most the page's data comes to. The speech may take
	// half; the screenshots are made smaller together until they fit the rest.
	pageBudget   = 15 << 20
	smallerTries = 6
)

// speechRates are the qualities the page's speech is made at, best first: an
// hour of it at the best would leave no room for the screenshots.
var speechRates = [][2]int{{48, 24000}, {32, 24000}, {24, 24000}, {16, 16000}, {8, 8000}}

// review is voiceover_review: it opens the page, as an App where the host
// shows pages and as a link to this machine where it does not, and tells the
// model its turn ends here.
func review(ctx context.Context, raw json.RawMessage) (string, error) {
	var args struct {
		Video string `json:"video"`
	}
	if err := serve.Args(raw, &args); err != nil {
		return "", err
	}
	p, err := openProject(args.Video)
	if err != nil {
		return "", err
	}
	scripted := ""
	if _, err := os.Stat(filepath.Join(p.dir, "script.json")); errors.Is(err, os.ErrNotExist) {
		scripted = " There is no script yet: the operator can write the script on the page, or paste it there."
	}
	asLink, err := showsAsLink(ctx)
	if err != nil {
		return "", err
	}
	if !asLink {
		return fmt.Sprintf("The review page for %s is open for the operator. On it they hear each line at its real length, "+
			"move and change lines, and pick the voice and the language.%s Send saves the script and tells you what to do next. "+
			"End your turn now: say in one sentence that the page is ready, and wait for the message Send brings.\n",
			shown(p.video), scripted), nil
	}
	url, err := openReviewLink(args.Video)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("The review page for %s is at %s\nGive the operator that link. It opens on this machine, "+
		"for as long as this tool's server runs, and stops after Send or two hours left alone. On it they hear each line "+
		"at its real length, move and change lines, and pick the voice and the language.%s Send saves the script and gives "+
		"them a message to paste back here, saying what to do next. End your turn now: give the link, and wait.\n",
		shown(p.video), url, scripted), nil
}

// pageLine is a line as the page shows it: with the length and the sound of
// its speech, when it has speech in the voice chosen.
type pageLine struct {
	At     float64  `json:"at"`
	Text   string   `json:"text"`
	Length *float64 `json:"length,omitempty"`
	Audio  string   `json:"audio,omitempty"`
}

type voiceGroup struct {
	Name   string   `json:"name"`
	Model  string   `json:"model"`
	Price  float64  `json:"price"`
	Voices []string `json:"voices"`
}

type pageData struct {
	Video     string          `json:"video"`
	Duration  float64         `json:"duration"`
	Step      int             `json:"step"`
	Voices    []voiceGroup    `json:"voices"`
	Model     string          `json:"model"`
	Voice     string          `json:"voice"`
	Spoken    *choices        `json:"spoken"`
	Languages [][2]string     `json:"languages"`
	Lines     []pageLine      `json:"lines"`
	Pace      float64         `json:"pace"`
	MaxChars  int             `json:"max_chars"`
	Samples   json.RawMessage `json:"samples"`
	Shots     []string        `json:"shots"`
}

// reviewData is voiceover_review_data, which only the page calls: everything
// it shows, made from the project as it is now.
func reviewData(ctx context.Context, raw json.RawMessage) (string, error) {
	var args struct {
		Video string `json:"video"`
	}
	if err := serve.Args(raw, &args); err != nil {
		return "", err
	}
	p, err := openProject(args.Video)
	if err != nil {
		return "", err
	}
	ff, err := findFFmpeg()
	if err != nil {
		return "", err
	}
	c, err := ff.probe(ctx, p.video)
	if err != nil {
		return "", err
	}
	var lines []scriptLine
	if _, err := os.Stat(filepath.Join(p.dir, "script.json")); err == nil {
		if lines, err = p.script(); err != nil {
			return "", err
		}
	}
	ch, err := p.choices()
	if err != nil {
		return "", err
	}
	backend, err := newOpenRouter()
	if err != nil {
		return "", err
	}
	vs, err := offered(ctx, backend)
	if err != nil {
		return "", err
	}
	v, err := pick(vs, ch.Voice)
	if err != nil {
		return "", err
	}
	scratch, err := p.folder("page")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(scratch)

	d := pageData{Video: filepath.Base(p.video), Duration: c.seconds, Model: v.Model, Voice: v.Name,
		Languages: languages, Lines: make([]pageLine, len(lines)), Pace: wordsPerSecond, MaxChars: maxChars,
		Samples: samplesJSON}
	for _, m := range offeredModels {
		var g *voiceGroup
		for _, x := range vs {
			if x.Model != m {
				continue
			}
			if g == nil {
				d.Voices = append(d.Voices, voiceGroup{Name: x.ModelName, Model: x.Model, Price: x.PerMillion})
				g = &d.Voices[len(d.Voices)-1]
			}
			g.Voices = append(g.Voices, x.Name)
		}
	}
	if err := hearLines(ctx, ff, p, lines, v, scratch, &d); err != nil {
		return "", err
	}
	if err := screenshots(ctx, ff, p.video, c.seconds, scratch, &d); err != nil {
		return "", err
	}
	out, err := json.Marshal(d)
	return string(out), err
}

// hearLines gives each line spoken in the voice chosen its speech's length,
// once trimmed as voiceover_render will place it, and that speech in a form
// a browser plays, at the best quality the page has room for.
func hearLines(ctx context.Context, ff ffmpeg, p project, lines []scriptLine, v Voice, scratch string, d *pageData) error {
	type heard struct {
		i      int
		pcm    string
		frames int64
	}
	var spoken []heard
	total := 0.0
	for i, l := range lines {
		d.Lines[i] = pageLine{At: l.At, Text: l.Text}
		src := p.spoken(v, l.Text)
		if src == "" {
			continue
		}
		pcm := filepath.Join(scratch, fmt.Sprintf("%03d.pcm", i+1))
		frames, err := level(ctx, ff, src, pcm)
		if err != nil {
			return fmt.Errorf("line %d's speech cannot be read (%v): render it again", i+1, err)
		}
		spoken = append(spoken, heard{i, pcm, frames})
		total += float64(frames) / trackRate
	}
	if len(spoken) == 0 {
		return nil
	}
	d.Spoken = &choices{Model: v.Model, Voice: v.Name}
	kbps, rate := speechRates[len(speechRates)-1][0], speechRates[len(speechRates)-1][1]
	for _, r := range speechRates {
		if total*float64(r[0])*1000/8*4/3 <= pageBudget/2 {
			kbps, rate = r[0], r[1]
			break
		}
	}
	for _, h := range spoken {
		// AAC in an MP4: any browser plays it, and ffmpeg makes it with no
		// library of its own.
		m4a := h.pcm + ".m4a"
		if _, err := ff.run(ctx, "-f", "s16le", "-ar", fmt.Sprint(trackRate), "-ac", "1", "-i", h.pcm, "-ac", "1",
			"-ar", fmt.Sprint(rate), "-c:a", "aac", "-b:a", fmt.Sprintf("%dk", kbps), "-f", "mp4", m4a); err != nil {
			return fmt.Errorf("line %d's speech could not be made playable: %v", h.i+1, err)
		}
		b, err := os.ReadFile(m4a)
		if err != nil {
			return err
		}
		length := math.Round(float64(h.frames)/trackRate*100) / 100
		d.Lines[h.i].Length = &length
		d.Lines[h.i].Audio = base64.StdEncoding.EncodeToString(b)
	}
	return nil
}

// screenshots are the frame at every step along the video, the exact frame
// and not the keyframe before it, made smaller together until the page fits.
func screenshots(ctx context.Context, ff ffmpeg, video string, seconds float64, scratch string, d *pageData) error {
	d.Step = shotEvery * max(1, int(math.Ceil(seconds/(shotEvery*mostShots))))
	var shots []string
	for t := 0; float64(t) < math.Ceil(seconds); t += d.Step {
		at := math.Max(0, math.Min(float64(t), seconds-1))
		out := filepath.Join(scratch, fmt.Sprintf("shot-%05d.jpg", t))
		if _, err := ff.run(ctx, "-ss", fmt.Sprintf("%.3f", at), "-i", video, "-frames:v", "1",
			"-vf", fmt.Sprintf("scale='min(%d,iw)':-2", shotWidth), "-q:v", shotQuality, out); err != nil {
			return fmt.Errorf("no screenshot at %.0f s: %v", at, err)
		}
		shots = append(shots, out)
	}
	rest, err := json.Marshal(d)
	if err != nil {
		return err
	}
	room := pageBudget - len(reviewPage) - len(rest) - 4*len(shots)
	for try := 0; ; try++ {
		d.Shots = d.Shots[:0]
		size := 0
		for _, s := range shots {
			b, err := os.ReadFile(s)
			if err != nil {
				return err
			}
			url := "data:image/jpeg;base64," + base64.StdEncoding.EncodeToString(b)
			d.Shots = append(d.Shots, url)
			size += len(url)
		}
		if size <= room {
			return nil
		}
		if try == smallerTries {
			return fmt.Errorf("the screenshots will not fit in the page")
		}
		scale := math.Max(0.4, math.Min(0.9, 0.95*math.Sqrt(float64(room)/float64(size))))
		for _, s := range shots {
			smaller := s + ".small.jpg"
			if _, err := ff.run(ctx, "-i", s, "-vf", fmt.Sprintf("scale=iw*%.3f:-2", scale), "-q:v", shotQuality, smaller); err != nil {
				return fmt.Errorf("could not make a screenshot smaller: %v", err)
			}
			if err := os.Rename(smaller, s); err != nil {
				return err
			}
		}
	}
}

// saveReview is voiceover_save_review, what the page's Send calls: it writes
// the script and the voice, and what it answers is what the page tells the
// conversation.
func saveReview(ctx context.Context, raw json.RawMessage) (string, error) {
	var args struct {
		Video    string       `json:"video"`
		Voice    string       `json:"voice"`
		Model    string       `json:"model"`
		Language string       `json:"language"`
		Lines    []scriptLine `json:"lines"`
	}
	if err := serve.Args(raw, &args); err != nil {
		return "", err
	}
	p, err := openProject(args.Video)
	if err != nil {
		return "", err
	}
	language := ""
	for _, l := range languages {
		if l[0] == args.Language {
			language = l[1]
		}
	}
	if args.Language != "" && language == "" {
		return "", fmt.Errorf("%q is not a language the voices were tried in", args.Language)
	}
	if len(args.Lines) == 0 {
		return "", errors.New("no lines: write at least one line before Send")
	}
	ff, err := findFFmpeg()
	if err != nil {
		return "", err
	}
	c, err := ff.probe(ctx, p.video)
	if err != nil {
		return "", err
	}
	backend, err := newOpenRouter()
	if err != nil {
		return "", err
	}
	vs, err := offered(ctx, backend)
	if err != nil {
		return "", err
	}
	var v *Voice
	for i := range vs {
		if vs[i].Name == args.Voice && vs[i].Model == args.Model {
			v = &vs[i]
		}
	}
	if v == nil {
		return "", fmt.Errorf("%q is not a voice of %s", args.Voice, args.Model)
	}

	lines := make([]scriptLine, len(args.Lines))
	var problems []string
	for i, l := range args.Lines {
		n, text := i+1, strings.Join(strings.Fields(l.Text), " ")
		switch {
		case text == "":
			problems = append(problems, fmt.Sprintf("line %d has no text", n))
		case len(text) > maxChars:
			problems = append(problems, fmt.Sprintf("line %d has %d characters, over the %d one speech call can take: split it", n, len(text), maxChars))
		case l.At < 0:
			problems = append(problems, fmt.Sprintf("line %d starts at %.1f s, before the video starts", n, l.At))
		case l.At >= c.seconds:
			problems = append(problems, fmt.Sprintf("line %d starts at %.1f s, after the video's end at %.1f s", n, l.At, c.seconds))
		case i > 0 && l.At < args.Lines[i-1].At:
			problems = append(problems, fmt.Sprintf("line %d starts at %.1f s, before line %d", n, l.At, i))
		}
		lines[i] = scriptLine{l.At, text}
	}
	if len(problems) > 0 {
		return "", fmt.Errorf("nothing was saved:\n%s", strings.Join(problems, "\n"))
	}
	if err := p.writeScript(lines); err != nil {
		return "", err
	}
	if err := p.writeChoices(choices{Voice: v.Name, Model: v.Model, TranslateTo: args.Language}); err != nil {
		return "", err
	}

	todo, chars := 0, 0
	for _, l := range lines {
		if language != "" || p.spoken(*v, l.Text) == "" {
			todo++
			chars += len(l.Text)
		}
	}
	cost := dollars(float64(chars) * v.PerMillion / 1e6)
	saved := shown(filepath.Join(p.dir, "script.json"))
	var b strings.Builder
	fmt.Fprintf(&b, "Sent from the review page of %s: %s, saved in %s with the voice %s (%s). ",
		shown(p.video), count(len(lines), "line"), saved, v.Name, v.ModelName)
	if language != "" {
		fmt.Fprintf(&b, "To be translated into %s, then every line spoken, about %s.\n", language, cost)
		fmt.Fprintf(&b, "Next: translate the text of every line in %s into %s, keep every at, and call voiceover_script "+
			"with the translated lines, each with its at; then voiceover_render, then voiceover_review, so the operator "+
			"hears the translation at its real lengths.\n", saved, language)
		return b.String(), nil
	}
	kept := len(lines) - todo
	keeps := fmt.Sprintf("%d keep their speech", kept)
	if kept == 1 {
		keeps = "1 keeps its speech"
	}
	fmt.Fprintf(&b, "%d to speak, about %s; %s.\n", todo, cost, keeps)
	fmt.Fprintf(&b, "Next: call voiceover_render for %s, then voiceover_review again, so the operator sees every line at its real length.\n",
		shown(p.video))
	return b.String(), nil
}
