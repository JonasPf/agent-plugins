package main

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
)

// The local backend: the models run on this machine, through sherpa-onnx's
// programs, so nothing is sent anywhere and nothing is paid. It is what a
// host without an OpenRouter key gets. sherpa-onnx is pinned at 1.13.8, the
// last release that still speaks a word missing from its lexicon (2.0 drops
// it, numbers too: k2-fsa/sherpa-onnx#4139).

const sherpaRelease = "https://github.com/k2-fsa/sherpa-onnx/releases/download/"

var (
	// platform is where the server runs, as the helper's builds are named.
	platform = runtime.GOOS + "/" + runtime.GOARCH

	helpers = map[string]asset{
		"darwin/arm64": {"sherpa-onnx-v1.13.8-osx-arm64-shared", "sherpa-onnx 1.13.8",
			sherpaRelease + "v1.13.8/sherpa-onnx-v1.13.8-osx-arm64-shared.tar.bz2",
			"b10e5c7e2c30ea03de9c442655d14860d9edc475c6251d58a8f5f06e913a1d56", 20314448},
		"linux/amd64": {"sherpa-onnx-v1.13.8-linux-x64-shared", "sherpa-onnx 1.13.8",
			sherpaRelease + "v1.13.8/sherpa-onnx-v1.13.8-linux-x64-shared.tar.bz2",
			"c0bdb7907d3a74bba1d55d22bf4d9fa75586cf1530614ebe88a27b9118e015c4", 28156791},
		"linux/arm64": {"sherpa-onnx-v1.13.8-linux-aarch64-shared-cpu", "sherpa-onnx 1.13.8",
			sherpaRelease + "v1.13.8/sherpa-onnx-v1.13.8-linux-aarch64-shared-cpu.tar.bz2",
			"4e3734f82bc1379fd91f219f5869c7e9d03b7a4f7561907d8abca4849c51a789", 28091778},
	}

	// parakeet hears 25 European languages and finds which by itself.
	parakeet = asset{"sherpa-onnx-nemo-parakeet-tdt-0.6b-v3-int8", "nvidia/parakeet-tdt-0.6b-v3",
		sherpaRelease + "asr-models/sherpa-onnx-nemo-parakeet-tdt-0.6b-v3-int8.tar.bz2",
		"1495e91b59d7fc53ddca91f3431758ec99f8fa253fdb0e1e851be0f72b14b0f9", 487211443}

	// silero finds where someone speaks, which cuts the sound into phrases.
	silero = asset{"silero_vad_v5", "Silero VAD v5",
		sherpaRelease + "asr-models/silero_vad_v5.onnx",
		"6b99cbfd39246b6706f98ec13c7c50c6b299181f2474fa05cbc8046acc274396", 2313101}
)

type local struct {
	helper, model, vad string // the folders they were unpacked to
}

func newLocal() (*local, error) {
	if _, ok := helpers[platform]; !ok {
		return nil, fmt.Errorf("nothing transcribes on this machine: sherpa-onnx is not built for %s. "+
			"The operator sets an OpenRouter API key (%s) in this bundle's settings, with the backend auto or openrouter; "+
			"until then, voiceover_frames still works", platform, keyName)
	}
	return &local{}, nil
}

func (l *local) Name() string  { return "this machine" }
func (l *local) Model() string { return parakeet.title + " (CC-BY-4.0)" }

// Ready downloads what is not in the cache yet, and says what it downloaded.
func (l *local) Ready(ctx context.Context) (string, error) {
	var got []string
	for _, a := range []struct {
		to *string
		a  asset
	}{{&l.helper, helpers[platform]}, {&l.model, parakeet}, {&l.vad, silero}} {
		dir, fetched, err := download(ctx, a.a)
		if err != nil {
			return "", err
		}
		*a.to = dir
		if fetched {
			got = append(got, fmt.Sprintf("%s (%d MB)", a.a.title, (a.a.size+500_000)/1_000_000))
		}
	}
	if len(got) == 0 {
		return "", nil
	}
	root, _ := cacheRoot()
	return fmt.Sprintf("Downloaded once, into %s: %s.\n", root, strings.Join(got, ", ")), nil
}

// phrase is a line of the helper's output: "7.248 -- 8.204: Thank you."
var phrase = regexp.MustCompile(`^(\d+(?:\.\d+)?) -- (\d+(?:\.\d+)?): (.*)$`)

// Transcribe has the voice detector cut the sound into phrases, and the model
// write each down. The language and the terms are not used: the model finds
// the language itself, and takes no words to expect.
func (l *local) Transcribe(ctx context.Context, audio string, _ TranscribeOptions) ([]Segment, Cost, error) {
	free := Cost{Known: true}
	ff, err := findFFmpeg()
	if err != nil {
		return nil, free, err
	}
	// The helper reads 16-bit WAV, at the 16 kHz the model hears.
	wav := strings.TrimSuffix(audio, filepath.Ext(audio)) + ".helper.wav"
	defer os.Remove(wav)
	if _, err := ff.run(ctx, "-i", audio, "-ac", "1", "-ar", "16000", "-c:a", "pcm_s16le", wav); err != nil {
		return nil, free, fmt.Errorf("could not make the sound into WAV for the model: %v", err)
	}
	threads := min(runtime.NumCPU(), 8)
	cmd := exec.CommandContext(ctx, filepath.Join(l.helper, "bin", "sherpa-onnx-vad-with-offline-asr"),
		"--silero-vad-model="+filepath.Join(l.vad, "silero_vad_v5.onnx"),
		// Phrases about as long as Whisper's: a word's time is found by
		// spreading a phrase's words over it.
		"--silero-vad-min-silence-duration=0.3",
		"--silero-vad-max-speech-duration=15",
		"--model-type=nemo_transducer",
		"--encoder="+filepath.Join(l.model, "encoder.int8.onnx"),
		"--decoder="+filepath.Join(l.model, "decoder.int8.onnx"),
		"--joiner="+filepath.Join(l.model, "joiner.int8.onnx"),
		"--tokens="+filepath.Join(l.model, "tokens.txt"),
		"--num-threads="+strconv.Itoa(threads),
		wav)
	var out, said bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &said
	if err := cmd.Run(); err != nil {
		return nil, free, fmt.Errorf("the model on this machine stopped: %s", lastLine(said.String(), err.Error()))
	}
	var segs []Segment
	for _, line := range strings.Split(out.String(), "\n") {
		m := phrase.FindStringSubmatch(strings.TrimSpace(line))
		if m == nil || strings.TrimSpace(m[3]) == "" {
			continue
		}
		start, _ := strconv.ParseFloat(m[1], 64)
		end, _ := strconv.ParseFloat(m[2], 64)
		segs = append(segs, Segment{start, end, strings.TrimSpace(m[3])})
	}
	return segs, free, nil
}
