package main

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/JonasPf/agent-plugins/internal/serve"
)

const maxFrames = 20

// frames is voiceover_frames: what the video is, and the picture at each time
// asked for.
func frames(ctx context.Context, raw json.RawMessage) (string, error) {
	var args struct {
		Video string    `json:"video"`
		At    []float64 `json:"at"`
	}
	if err := serve.Args(raw, &args); err != nil {
		return "", err
	}
	if len(args.At) > maxFrames {
		return "", fmt.Errorf("%d times is more than the %d one call takes: ask for the rest in another call", len(args.At), maxFrames)
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
	for _, at := range args.At {
		if at < 0 || at >= c.seconds {
			return "", fmt.Errorf("%.1f s is not in the video, which is %.1f s long: nothing was written", at, c.seconds)
		}
	}

	var b strings.Builder
	fmt.Fprintf(&b, "%s: %s\nffmpeg: %s (%s)\n", shown(p.video), c, ff.path, ff.from)
	if len(args.At) == 0 {
		return b.String(), nil
	}
	dir, err := p.folder("frames")
	if err != nil {
		return "", err
	}
	for _, at := range args.At {
		out := filepath.Join(dir, fmt.Sprintf("%.2f.jpg", at))
		if err := ff.frame(ctx, p.video, at, out); err != nil {
			return "", fmt.Errorf("no frame at %.1f s: %v", at, err)
		}
		fmt.Fprintf(&b, "%s  the frame at %.1f s\n", shown(out), at)
	}
	return b.String(), nil
}

func (c clip) String() string {
	sound := "no sound"
	if c.sound {
		sound = "with sound"
	}
	return fmt.Sprintf("%.1f s, %dx%d, %g fps, %s", c.seconds, c.width, c.height, c.fps, sound)
}
