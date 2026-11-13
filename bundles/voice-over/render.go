package main

import (
	"bufio"
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"

	"github.com/JonasPf/agent-plugins/internal/serve"
)

// voiceover_render speaks every line not spoken yet, lays each at its moment
// on a silent track as long as the video, and gives the video that track as
// its sound. It is place_chunks.py and vo_mux.py from the skill, ported, with
// the speaking the agent used to do one call at a time done here.

const (
	trackRate = 44100
	// fadeFrames is what a line cut short fades out over, so it ends
	// without a click: 20 ms.
	fadeFrames = 882
	// targetDB is the loudness every line is brought to, so no line is
	// louder than the one before.
	targetDB = -20.0
	// speakAtOnce is how many lines are asked for at a time.
	speakAtOnce = 4
	// trimSilence takes the silence off both ends of a line, so it starts
	// when its first word does.
	trimSilence = "silenceremove=start_periods=1:start_threshold=-45dB,areverse," +
		"silenceremove=start_periods=1:start_threshold=-45dB,areverse"
)

var meanVolume = regexp.MustCompile(`mean_volume: (-?[\d.]+) dB`)

// laid is a line on the track: its levelled samples, where they start, how
// many there are, and how many are kept before what cuts them off.
type laid struct {
	n                   int
	pcm                 string
	first, frames, kept int64
	by                  string
}

func renderVideo(ctx context.Context, raw json.RawMessage) (string, error) {
	var args struct {
		Video string `json:"video"`
		Voice string `json:"voice"`
	}
	if err := serve.Args(raw, &args); err != nil {
		return "", err
	}
	p, err := openProject(args.Video)
	if err != nil {
		return "", err
	}
	lines, err := p.script()
	if err != nil {
		return "", err
	}
	ch, err := p.choices()
	if err != nil {
		return "", err
	}
	if ch.TranslateTo != "" {
		return "", fmt.Errorf("a translation into %s is owed: translate every line's text, keep every at, and call "+
			"voiceover_script with the translated lines; then render", ch.TranslateTo)
	}
	ff, err := findFFmpeg()
	if err != nil {
		return "", err
	}
	c, err := ff.probe(ctx, p.video)
	if err != nil {
		return "", err
	}
	for i, l := range lines {
		if l.At >= c.seconds {
			return "", fmt.Errorf("line %d starts at %.1f s, after the video's end at %.1f s: place it again with voiceover_script", i+1, l.At, c.seconds)
		}
	}
	backend, err := newOpenRouter()
	if err != nil {
		return "", err
	}
	vs, err := offered(ctx, backend)
	if err != nil {
		return "", err
	}
	name := args.Voice
	if name == "" {
		name = ch.Voice
	}
	v, err := pick(vs, name)
	if err != nil {
		return "", err
	}
	ch.Voice, ch.Model = v.Name, v.Model
	if err := p.writeChoices(ch); err != nil {
		return "", err
	}

	paths, now, cost, err := speakAll(ctx, backend, p, lines, v)
	if err != nil {
		return "", err
	}
	dir, err := p.folder("placed")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(dir)
	total := int64(c.seconds * trackRate)
	var track []laid
	for i, l := range lines {
		pcm := filepath.Join(dir, fmt.Sprintf("%03d.pcm", i+1))
		frames, err := level(ctx, ff, paths[i], pcm)
		if err != nil {
			return "", fmt.Errorf("line %d's speech could not be levelled: %v", i+1, err)
		}
		track = append(track, laid{n: i + 1, pcm: pcm, first: int64(l.At * trackRate), frames: frames, kept: frames})
	}
	for i := range track {
		limit, by := total, "the end of the video"
		if i+1 < len(track) {
			limit, by = track[i+1].first, fmt.Sprintf("line %d", track[i+1].n)
		}
		track[i].kept = min(track[i].frames, limit-track[i].first)
		track[i].by = by
	}
	trackPath := filepath.Join(p.dir, "track.wav")
	if err := writeTrack(trackPath, total, track); err != nil {
		return "", err
	}
	out := p.dir + ".mp4"
	if _, err := ff.run(ctx, "-i", p.video, "-i", trackPath, "-map", "0:v", "-map", "1:a", "-c:v", "copy",
		"-c:a", "aac", "-b:a", "192k", "-ar", strconv.Itoa(trackRate), "-af", "apad",
		"-t", fmt.Sprintf("%.3f", c.seconds), out); err != nil {
		return "", fmt.Errorf("could not put the track on the video: %v", err)
	}
	made, err := ff.probe(ctx, out)
	if err != nil {
		return "", err
	}

	var b strings.Builder
	fmt.Fprintf(&b, "Wrote %s: %.1f s, the video's picture as it was with the voice-over as its sound.\n", shown(out), made.seconds)
	fmt.Fprintf(&b, "Spoken by %s, %s, voice %s: %s spoken now, %d kept, %s.\n",
		backend.Name(), v.Model, v.Name, count(now, "line"), len(lines)-now, cost)
	cutShort := 0
	for _, l := range track {
		if l.kept < l.frames {
			cutShort++
			fmt.Fprintf(&b, "line %d cut %.1f s short by %s\n", l.n, float64(l.frames-l.kept)/trackRate, l.by)
		}
	}
	if cutShort == 0 {
		b.WriteString("Every line plays to its end: none cut short.\n")
	} else {
		b.WriteString("A line cut short stays where it was put, with its words: moving or shortening it is the operator's, on the review page.\n")
	}
	b.WriteString("Tell the operator: every line starts where the transcript or the operator put it, and nobody has listened to the result.\n")
	return b.String(), nil
}

// speakAll speaks every line that has no speech yet, a few at a time, and
// returns each line's speech. A line that fails is named, and the rest are
// kept, so the next call asks only for what is missing.
func speakAll(ctx context.Context, s Speaker, p project, lines []scriptLine, v Voice) ([]string, int, Cost, error) {
	if _, err := p.folder("speech"); err != nil {
		return nil, 0, Cost{}, err
	}
	paths := make([]string, len(lines))
	errs := make([]error, len(lines))
	costs := make([]Cost, len(lines))
	asked := make([]bool, len(lines))
	var wg sync.WaitGroup
	slots := make(chan struct{}, speakAtOnce)
	now := 0
	for i, l := range lines {
		if paths[i] = p.spoken(v, l.Text); paths[i] != "" {
			continue
		}
		now++
		asked[i] = true
		wg.Add(1)
		go func() {
			defer wg.Done()
			slots <- struct{}{}
			defer func() { <-slots }()
			paths[i], costs[i], errs[i] = s.Speak(ctx, l.Text, v, p.speechOf(v, l.Text))
		}()
	}
	wg.Wait()
	cost := Cost{Known: true}
	var failed []string
	for i, err := range errs {
		if asked[i] && err == nil {
			cost = cost.Plus(costs[i])
		}
		if err != nil {
			failed = append(failed, fmt.Sprintf("line %d: %v", i+1, err))
		}
	}
	if len(failed) > 0 {
		return nil, 0, cost, fmt.Errorf("%d of %d lines could not be spoken, so nothing was placed:\n%s\n"+
			"The lines spoken are kept, so calling voiceover_render again asks only for the rest",
			len(failed), len(lines), strings.Join(failed, "\n"))
	}
	return paths, now, cost, nil
}

// level takes the silence off a line's ends, brings it to the loudness every
// line shares, and writes it as raw samples at the track's rate. It returns
// how many samples that is.
func level(ctx context.Context, ff ffmpeg, src, pcm string) (int64, error) {
	trimmed := pcm + ".wav"
	defer os.Remove(trimmed)
	if _, err := ff.run(ctx, "-i", src, "-ac", "1", "-ar", strconv.Itoa(trackRate), "-af", trimSilence, trimmed); err != nil {
		return 0, err
	}
	report, err := ff.run(ctx, "-i", trimmed, "-af", "volumedetect", "-f", "null", "-")
	if err != nil {
		return 0, err
	}
	gain := 0.0
	if m := meanVolume.FindStringSubmatch(report); m != nil {
		mean, _ := strconv.ParseFloat(m[1], 64)
		gain = targetDB - mean
	}
	if _, err := ff.run(ctx, "-i", trimmed, "-af", fmt.Sprintf("volume=%.2fdB,alimiter=limit=0.84:level=0", gain),
		"-ac", "1", "-ar", strconv.Itoa(trackRate), "-f", "s16le", "-c:a", "pcm_s16le", pcm); err != nil {
		return 0, err
	}
	info, err := os.Stat(pcm)
	if err != nil {
		return 0, err
	}
	return info.Size() / 2, nil
}

// writeTrack writes the lines on a silent track total samples long, as it
// goes: an hour of track is 300 MB, on a server with less than 2 GB.
func writeTrack(path string, total int64, lines []laid) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	w := bufio.NewWriterSize(f, 1<<20)
	if _, err := w.Write(wavOf(nil, trackRate, 1)); err != nil {
		f.Close()
		return err
	}
	written := int64(0)
	for _, l := range lines {
		if err := silence(w, l.first-written); err != nil {
			f.Close()
			return err
		}
		if err := copyLine(w, l); err != nil {
			f.Close()
			return fmt.Errorf("line %d: %v", l.n, err)
		}
		written = l.first + l.kept
	}
	if err := silence(w, total-written); err != nil {
		f.Close()
		return err
	}
	if err := w.Flush(); err != nil {
		f.Close()
		return err
	}
	// The sizes in the header, now that they are known.
	var sizes [4]byte
	binary.LittleEndian.PutUint32(sizes[:], uint32(36+2*total))
	if _, err := f.WriteAt(sizes[:], 4); err != nil {
		f.Close()
		return err
	}
	binary.LittleEndian.PutUint32(sizes[:], uint32(2*total))
	if _, err := f.WriteAt(sizes[:], 40); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

var quiet = make([]byte, 64<<10)

func silence(w io.Writer, frames int64) error {
	for n := 2 * frames; n > 0; {
		k := min(n, int64(len(quiet)))
		if _, err := w.Write(quiet[:k]); err != nil {
			return err
		}
		n -= k
	}
	return nil
}

// copyLine writes the samples of a line that are kept. One cut short falls
// away to nothing over its last fadeFrames.
func copyLine(w io.Writer, l laid) error {
	f, err := os.Open(l.pcm)
	if err != nil {
		return err
	}
	defer f.Close()
	if l.kept >= l.frames {
		_, err := io.CopyN(w, f, 2*l.kept)
		return err
	}
	fade := min(int64(fadeFrames), l.kept)
	if _, err := io.CopyN(w, f, 2*(l.kept-fade)); err != nil {
		return err
	}
	tail := make([]byte, 2*fade)
	if _, err := io.ReadFull(f, tail); err != nil {
		return err
	}
	for i := int64(0); i < fade; i++ {
		s := int32(int16(binary.LittleEndian.Uint16(tail[2*i:])))
		left := fade - 1 - i // samples after this one
		binary.LittleEndian.PutUint16(tail[2*i:], uint16(int16(s*int32(left)/int32(fade))))
	}
	_, err = w.Write(tail)
	return err
}
