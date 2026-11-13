package main

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"

	"github.com/JonasPf/agent-plugins/internal/serve"
)

var (
	// longAfter is where a recording stops being transcribed until the
	// operator agrees: past an hour, speaking it costs real money and time.
	longAfter = 60 * 60.0
	// partSeconds is the most of a recording one call carries. A call takes
	// 20 MB, which the 64 kbps sound fills in about 40 minutes.
	partSeconds = 40 * 60.0
)

const (
	// charsPerSecond is what a narrator says in a second, in characters: a
	// ten-minute video is about 6,000.
	charsPerSecond = 10
	// oneBlockAfter: Whisper's phrases are seconds long, so a recording longer
	// than this that comes back as one phrase came back as one block, which
	// no line can be placed in. gemini-3.5-transcribe does that.
	oneBlockAfter = 60.0
)

// part is a stretch of the recording sent in one call, and where it begins.
type part struct {
	path           string
	start, seconds float64
}

// transcribe is voiceover_transcribe: the video's sound, written down with
// the time of every phrase, saved as segments.txt in the project.
func transcribe(ctx context.Context, raw json.RawMessage) (string, error) {
	var args struct {
		Video    string `json:"video"`
		Language string `json:"language"`
		Terms    string `json:"terms"`
		Long     bool   `json:"long"`
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
	var b strings.Builder
	fmt.Fprintf(&b, "%s: %s\nffmpeg: %s (%s)\n", shown(p.video), c, ff.path, ff.from)
	if !c.sound {
		b.WriteString(noNarration("It has no sound"))
		return b.String(), nil
	}
	backend, err := newOpenRouter()
	if err != nil {
		return "", err
	}
	if c.seconds > longAfter && !args.Long {
		return "", tooLong(ctx, backend, c.seconds)
	}

	if err := os.MkdirAll(p.dir, 0o755); err != nil {
		return "", fmt.Errorf("could not make %s: %v", shown(p.dir), err)
	}
	// AAC, which ffmpeg encodes with no library of its own: at 48 kbps, the
	// 40 minutes one call carries come to about 14 MB.
	audio := filepath.Join(p.dir, "audio.m4a")
	if _, err := ff.run(ctx, "-i", p.video, "-vn", "-ac", "1", "-ar", "16000", "-c:a", "aac", "-b:a", "48k", audio); err != nil {
		return "", fmt.Errorf("could not take the sound out of %s: %v", shown(p.video), err)
	}
	parts, err := cutParts(ctx, ff, p, audio, c.seconds)
	defer os.RemoveAll(filepath.Join(p.dir, "parts"))
	if err != nil {
		return "", err
	}

	var all []Segment
	cost := Cost{Known: true}
	for i, pt := range parts {
		segs, spent, err := backend.Transcribe(ctx, pt.path, TranscribeOptions{Language: args.Language, Terms: args.Terms})
		cost = cost.Plus(spent)
		if err != nil {
			if len(parts) > 1 {
				return "", fmt.Errorf("part %d of %d: %v", i+1, len(parts), err)
			}
			return "", err
		}
		if len(segs) == 1 && pt.seconds > oneBlockAfter {
			return "", fmt.Errorf("%s gave %s of sound back as one block, and no line can be placed inside a block: "+
				"nothing was saved. Transcribe with %s", defaultTranscriber, length(pt.seconds), defaultTranscriber)
		}
		for _, s := range segs {
			all = append(all, Segment{s.Start + pt.start, s.End + pt.start, s.Text})
		}
	}

	var lines strings.Builder
	for _, s := range all {
		fmt.Fprintf(&lines, "[%.2f–%.2f] %s\n", s.Start, s.End, s.Text)
	}
	out := filepath.Join(p.dir, "segments.txt")
	if err := os.WriteFile(out, []byte(lines.String()), 0o644); err != nil {
		return "", err
	}
	fmt.Fprintf(&b, "Transcribed by %s, %s, %s.\n", backend.Name(), defaultTranscriber, cost)
	if len(all) == 0 {
		b.WriteString(noNarration("No words were heard in its sound"))
		return b.String(), nil
	}
	fmt.Fprintf(&b, "%s: %s, the narrator's words and when they were said:\n%s", shown(out), count(len(all), "line"), lines.String())
	return b.String(), nil
}

func noNarration(why string) string {
	return why + `, so there is no narration to take the lines from. Write the script with "at", the second each line ` +
		"starts, chosen from voiceover_frames, or let the operator place the lines on the review page (voiceover_review).\n"
}

// cutParts is the recording as the calls will carry it: whole, or in parts
// of partSeconds, each cut without being encoded again.
func cutParts(ctx context.Context, ff ffmpeg, p project, audio string, seconds float64) ([]part, error) {
	if seconds <= partSeconds {
		return []part{{audio, 0, seconds}}, nil
	}
	dir, err := p.folder("parts")
	if err != nil {
		return nil, err
	}
	var parts []part
	for i, start := 0, 0.0; start < seconds; i, start = i+1, start+partSeconds {
		out := filepath.Join(dir, fmt.Sprintf("audio-%03d.m4a", i))
		if _, err := ff.run(ctx, "-ss", fmt.Sprintf("%.3f", start), "-t", fmt.Sprintf("%.3f", partSeconds),
			"-i", audio, "-c", "copy", out); err != nil {
			return nil, fmt.Errorf("could not cut part %d of the sound: %v", i+1, err)
		}
		parts = append(parts, part{out, start, math.Min(partSeconds, seconds-start)})
	}
	return parts, nil
}

// tooLong is the refusal of a recording over the hour, with what speaking
// it would cost in the default voice: the number the operator decides by.
func tooLong(ctx context.Context, s Speaker, seconds float64) error {
	chars := int(seconds * charsPerSecond)
	price := "what that costs could not be read now"
	if vs, err := s.Voices(ctx); err == nil {
		for _, v := range vs {
			if v.Model == defaultSpeech && v.Name == defaultVoice {
				price = fmt.Sprintf("about %s in the default voice, %s on %s at $%g per million characters",
					dollars(float64(chars)*v.PerMillion/1e6), v.Name, v.ModelName, v.PerMillion)
			}
		}
	} else {
		price += " (" + err.Error() + ")"
	}
	return fmt.Errorf("the video is %s long, over the hour this stops at before anything is sent. Speaking it is "+
		"about %d characters, %s, and transcribing and speaking it take a while. Tell the operator and end the turn; "+
		"call again with long: true only once they agree", length(seconds), chars, price)
}

// length is a duration as a person says it.
func length(seconds float64) string {
	switch {
	case seconds >= 3600:
		m := int(seconds/60 + 0.5)
		return fmt.Sprintf("%d h %d min", m/60, m%60)
	case seconds >= 60:
		return fmt.Sprintf("%d min %d s", int(seconds)/60, int(seconds)%60)
	}
	return fmt.Sprintf("%.1f s", seconds)
}

// count is a number of things, singular when there is one.
func count(n int, thing string) string {
	if n == 1 {
		return "1 " + thing
	}
	return fmt.Sprintf("%d %ss", n, thing)
}
