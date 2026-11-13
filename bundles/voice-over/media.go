package main

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

// ffmpeg is the one program everything about sound and picture is done with.
// ffprobe is not used: what it would say, ffmpeg prints when it opens a file.
type ffmpeg struct {
	path string
	from string // where it was found, for a result to say
}

// findFFmpeg looks where the operator said, then in the bundle, then on PATH.
func findFFmpeg() (ffmpeg, error) {
	if set := setting("VOICEOVER_FFMPEG"); set != "" {
		if !runnable(set) {
			return ffmpeg{}, fmt.Errorf("the ffmpeg set in the bundle's settings, %s, is not a program that can be run: correct the path or clear it", set)
		}
		return ffmpeg{set, "set in the bundle's settings"}, nil
	}
	if exe, err := os.Executable(); err == nil {
		if bundled := filepath.Join(filepath.Dir(exe), "bin", "ffmpeg"); runnable(bundled) {
			return ffmpeg{bundled, "the bundle's own"}, nil
		}
	}
	if found, err := exec.LookPath("ffmpeg"); err == nil {
		return ffmpeg{found, "found on PATH"}, nil
	}
	return ffmpeg{}, fmt.Errorf("no ffmpeg: this bundle has none in it and there is none on PATH. Install ffmpeg, or set its path in the bundle's settings")
}

func runnable(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir() && info.Mode()&0o111 != 0
}

// run runs ffmpeg and returns what it printed. A failure is its last line,
// which is where ffmpeg says what was wrong.
func (f ffmpeg) run(ctx context.Context, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, f.path, append([]string{"-hide_banner", "-nostdin", "-y"}, args...)...)
	var out bytes.Buffer
	cmd.Stderr = &out
	if err := cmd.Run(); err != nil {
		return out.String(), fmt.Errorf("%s", lastLine(out.String(), err.Error()))
	}
	return out.String(), nil
}

func lastLine(text, otherwise string) string {
	lines := strings.Split(strings.TrimSpace(text), "\n")
	if last := strings.TrimSpace(lines[len(lines)-1]); last != "" {
		return last
	}
	return otherwise
}

// clip is what ffmpeg says a video is.
type clip struct {
	seconds       float64
	width, height int
	fps           float64
	sound         bool
}

var (
	durationLine = regexp.MustCompile(`Duration: (\d+):(\d\d):(\d\d(?:\.\d+)?)`)
	pictureLine  = regexp.MustCompile(`Stream #.*Video: .*?\b(\d{2,5})x(\d{2,5})\b`)
	fpsPart      = regexp.MustCompile(`([\d.]+) fps`)
	soundLine    = regexp.MustCompile(`Stream #.*Audio: `)
)

// probe opens a video and reads what ffmpeg prints about it. With no output
// named ffmpeg exits 1 having printed all of it, so the exit is not looked at.
func (f ffmpeg) probe(ctx context.Context, video string) (clip, error) {
	said, _ := f.run(ctx, "-i", video)
	d := durationLine.FindStringSubmatch(said)
	var c clip
	for _, line := range strings.Split(said, "\n") {
		if m := pictureLine.FindStringSubmatch(line); m != nil && c.width == 0 {
			c.width, _ = strconv.Atoi(m[1])
			c.height, _ = strconv.Atoi(m[2])
			if r := fpsPart.FindStringSubmatch(line); r != nil {
				c.fps, _ = strconv.ParseFloat(r[1], 64)
			}
		}
		if soundLine.MatchString(line) {
			c.sound = true
		}
	}
	if d == nil || c.width == 0 {
		return clip{}, fmt.Errorf("%s is not a video ffmpeg can read: %s", shown(video), lastLine(said, "it said nothing"))
	}
	h, _ := strconv.Atoi(d[1])
	m, _ := strconv.Atoi(d[2])
	s, _ := strconv.ParseFloat(d[3], 64)
	c.seconds = float64(h*3600+m*60) + s
	return c, nil
}

// frame writes the picture at one moment as a JPEG. The time goes before the
// input so ffmpeg seeks to it, and it then decodes from the keyframe before to
// the exact frame; a keyframe alone can be many seconds away.
func (f ffmpeg) frame(ctx context.Context, video string, at float64, out string) error {
	_, err := f.run(ctx, "-ss", fmt.Sprintf("%.3f", at), "-i", video, "-frames:v", "1",
		"-vf", "scale='min(1728,iw)':-2", "-q:v", "3", out)
	if err == nil {
		if _, missing := os.Stat(out); missing != nil {
			err = fmt.Errorf("ffmpeg wrote no picture")
		}
	}
	return err
}
