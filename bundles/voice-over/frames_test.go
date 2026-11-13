package main

import (
	"context"
	"fmt"
	"image/jpeg"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/JonasPf/agent-plugins/internal/servetest"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// bands makes a video whose every second can be checked: solid colours, one
// after the other, each for so many seconds, with a tone when sound is asked
// for. Tests make their own videos; none is kept in the repository.
func bands(t *testing.T, path string, sound bool, parts ...string) {
	t.Helper()
	var args []string
	total := 0
	for _, part := range parts {
		colour, seconds, _ := strings.Cut(part, ":")
		args = append(args, "-f", "lavfi", "-i", "color=c="+colour+":s=320x180:r=30:d="+seconds)
		var n int
		fmt.Sscan(seconds, &n)
		total += n
	}
	if sound {
		args = append(args, "-f", "lavfi", "-i", fmt.Sprintf("sine=f=440:d=%d", total))
	}
	chain := ""
	for i := range parts {
		chain += fmt.Sprintf("[%d:v]", i)
	}
	args = append(args, "-filter_complex", fmt.Sprintf("%sconcat=n=%d:v=1:a=0[v]", chain, len(parts)), "-map", "[v]")
	if sound {
		args = append(args, "-map", fmt.Sprintf("%d:a", len(parts)), "-c:a", "aac")
	}
	// One keyframe for the whole video, so a frame found by its keyframe
	// alone would be the first colour whatever time was asked for.
	args = append(args, "-c:v", "libx264", "-pix_fmt", "yuv420p", "-g", "600", "-preset", "ultrafast", path)
	if out, err := exec.Command("ffmpeg", append([]string{"-y", "-hide_banner", "-loglevel", "error"}, args...)...).CombinedOutput(); err != nil {
		t.Fatalf("could not make the test's video: %v\n%s", err, out)
	}
}

// colour is which of red, green and blue the middle of a JPEG mostly is.
func colour(t *testing.T, path string) string {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	img, err := jpeg.Decode(f)
	if err != nil {
		t.Fatalf("%s is not a JPEG: %v", path, err)
	}
	b := img.Bounds()
	r, g, bl, _ := img.At(b.Dx()/2, b.Dy()/2).RGBA()
	switch {
	case r > g && r > bl:
		return "red"
	case g > r && g > bl:
		return "green"
	}
	return "blue"
}

// "voiceover_frames says what the video is, and writes the frame at each time
// asked for into the project's folder beside the video, the exact frame and
// not the keyframe before it."
func TestFramesSaysWhatTheVideoIsAndWritesTheExactFrames(t *testing.T) {
	dir := t.TempDir()
	servetest.InDir(t, dir)
	bands(t, "clip.mov", true, "red:2", "green:2", "blue:2")
	cs := servetest.Client(t, manifest, tools, runs)

	res := servetest.Call(t, cs, "voiceover_frames", `{"video":"clip.mov","at":[1,3,5.5]}`)
	if res.Error {
		t.Fatalf("voiceover_frames: %s", res.Text)
	}
	for _, want := range []string{"clip.mov: 6.0 s, 320x180, 30 fps, with sound", "ffmpeg: ", "found on PATH",
		"clip.voiceover/frames/1.00.jpg", "clip.voiceover/frames/3.00.jpg", "clip.voiceover/frames/5.50.jpg"} {
		if !strings.Contains(res.Text, want) {
			t.Errorf("the result does not say %q:\n%s", want, res.Text)
		}
	}
	for name, want := range map[string]string{"1.00.jpg": "red", "3.00.jpg": "green", "5.50.jpg": "blue"} {
		if got := colour(t, filepath.Join(dir, "clip.voiceover", "frames", name)); got != want {
			t.Errorf("%s is %s, want %s", name, got, want)
		}
	}
}

// "With no times it says what the video is and writes nothing; a video with no
// sound is said to have none."
func TestFramesWithNoTimesOnlyDescribesTheVideo(t *testing.T) {
	dir := t.TempDir()
	servetest.InDir(t, dir)
	bands(t, "silent.mp4", false, "red:3")
	cs := servetest.Client(t, manifest, tools, runs)

	res := servetest.Call(t, cs, "voiceover_frames", `{"video":"silent.mp4"}`)
	if res.Error || !strings.Contains(res.Text, "3.0 s, 320x180, 30 fps, no sound") {
		t.Errorf("voiceover_frames = %+v", res)
	}
	if _, err := os.Stat(filepath.Join(dir, "silent.voiceover")); err == nil {
		t.Error("a folder was made though nothing was asked for")
	}
}

// "A failure says what was wrong and what to do, and writes nothing."
func TestFramesRefusesWhatItCannotDo(t *testing.T) {
	dir := t.TempDir()
	servetest.InDir(t, dir)
	bands(t, "clip.mov", false, "red:2")
	if err := os.WriteFile("notes.txt", []byte("not a video"), 0o644); err != nil {
		t.Fatal(err)
	}
	cs := servetest.Client(t, manifest, tools, runs)

	var many []string
	for i := 0; i < 21; i++ {
		many = append(many, "0")
	}
	for _, c := range []struct{ why, args, says string }{
		{"no video named", `{}`, "video is required"},
		{"a video that is not there", `{"video":"gone.mov","at":[1]}`, "is not a file"},
		{"a file that is no video", `{"video":"notes.txt","at":[1]}`, "is not a video ffmpeg can read"},
		{"a time after the end", `{"video":"clip.mov","at":[1,2]}`, "is not in the video, which is 2.0 s long"},
		{"a time before the start", `{"video":"clip.mov","at":[-1]}`, "is not in the video"},
		{"too many times", `{"video":"clip.mov","at":[` + strings.Join(many, ",") + `]}`, "more than the 20"},
	} {
		res := servetest.Call(t, cs, "voiceover_frames", c.args)
		if !res.Error || !strings.Contains(res.Text, c.says) {
			t.Errorf("%s: %+v, want a refusal saying %q", c.why, res, c.says)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "clip.voiceover")); err == nil {
		t.Error("a refused call wrote something")
	}
}

// "ffmpeg is looked for where the operator said, then in the bundle, then on
// PATH; a path set and wrong is said to be wrong rather than passed over."
func TestFFmpegIsFoundWhereTheOperatorSaidThenOnPath(t *testing.T) {
	onPath, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Fatal("these tests need ffmpeg on PATH")
	}
	if ff, err := findFFmpeg(); err != nil || ff.path != onPath || ff.from != "found on PATH" {
		t.Errorf("with nothing set: %+v %v", ff, err)
	}

	t.Setenv("VOICEOVER_FFMPEG", onPath)
	if ff, err := findFFmpeg(); err != nil || ff.path != onPath || ff.from != "set in the bundle's settings" {
		t.Errorf("with the path set: %+v %v", ff, err)
	}

	t.Setenv("VOICEOVER_FFMPEG", filepath.Join(t.TempDir(), "ffmpeg"))
	if _, err := findFFmpeg(); err == nil || !strings.Contains(err.Error(), "correct the path or clear it") {
		t.Errorf("with a wrong path set: %v", err)
	}

	t.Setenv("VOICEOVER_FFMPEG", "")
	t.Setenv("PATH", t.TempDir())
	if _, err := findFFmpeg(); err == nil || !strings.Contains(err.Error(), "no ffmpeg") {
		t.Errorf("with none anywhere: %v", err)
	}
}

// The bundle as it ships: the program built and spoken to over its standard
// input and output, with an ffmpeg beside it in bin/, which it uses before
// the one on PATH.
func TestTheBuiltServerUsesTheFFmpegInItsBundle(t *testing.T) {
	onPath, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Fatal("these tests need ffmpeg on PATH")
	}
	bundle := t.TempDir()
	bin := filepath.Join(bundle, "server")
	if out, err := exec.Command("go", "build", "-o", bin, ".").CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, out)
	}
	if err := os.Mkdir(filepath.Join(bundle, "bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(onPath, filepath.Join(bundle, "bin", "ffmpeg")); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	bands(t, filepath.Join(dir, "clip.mov"), false, "green:2")

	server := exec.Command(bin)
	server.Dir = dir
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "0"}, nil).
		Connect(context.Background(), &mcp.CommandTransport{Command: server}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer cs.Close()
	res := servetest.Call(t, cs, "voiceover_frames", `{"video":"clip.mov","at":[1]}`)
	if res.Error || !strings.Contains(res.Text, "the bundle's own") || !strings.Contains(res.Text, "clip.voiceover/frames/1.00.jpg") {
		t.Errorf("voiceover_frames = %+v", res)
	}
	if got := colour(t, filepath.Join(dir, "clip.voiceover", "frames", "1.00.jpg")); got != "green" {
		t.Errorf("the frame is %s, want green", got)
	}
}
