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
	"slices"
	"strconv"
	"strings"
	"sync"
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

	// kokoro speaks, in 54 voices, of which sherpa-onnx speaks the English
	// ones. Apache-2.0; its espeak-ng-data is GPL-3.0, downloaded from
	// sherpa-onnx's release and never carried in the bundle.
	kokoro = asset{"kokoro-int8-multi-lang-v1_0", "Kokoro 82M",
		sherpaRelease + "tts-models/kokoro-int8-multi-lang-v1_0.tar.bz2",
		"72c347230107ef8a31098ec4d468a605244beef5f1ed00db97efa13518800653", 132353410}
)

// localSpeech is Kokoro as a voice names its model.
const localSpeech = "hexgrad/kokoro-82m"

// kokoroVoices are Kokoro 1.0's English voices, each at its speaker number in
// voices.bin: American (a) then British (b), women (f) then men (m).
var kokoroVoices = []string{"af_alloy", "af_aoede", "af_bella", "af_heart", "af_jessica", "af_kore", "af_nicole",
	"af_nova", "af_river", "af_sarah", "af_sky", "am_adam", "am_echo", "am_eric", "am_fenrir", "am_liam", "am_michael",
	"am_onyx", "am_puck", "am_santa", "bf_alice", "bf_emma", "bf_isabella", "bf_lily", "bm_daniel", "bm_fable",
	"bm_george", "bm_lewis"}

// One line is spoken at a time, on every core: each run of the program loads
// the model, and four at once would hold it four times.
var speaking sync.Mutex

// local runs what it needs on this machine, once downloaded: the helper and
// either the transcription models or the speech model.
type local struct {
	needs []asset
	dirs  map[string]string // by asset name, the folder it was unpacked to
}

func newLocal(needs ...asset) (*local, error) {
	if _, ok := helpers[platform]; !ok {
		return nil, fmt.Errorf("nothing runs on this machine: sherpa-onnx is not built for %s. "+
			"The operator sets an OpenRouter API key (%s) in this bundle's settings, with the backend auto or openrouter; "+
			"until then, voiceover_frames still works", platform, keyName)
	}
	return &local{needs: needs, dirs: map[string]string{}}, nil
}

func (l *local) Name() string  { return "this machine" }
func (l *local) Model() string { return parakeet.title + " (CC-BY-4.0)" }

// in is a file of a downloaded asset.
func (l *local) in(a asset, name ...string) string {
	return filepath.Join(append([]string{l.dirs[a.name]}, name...)...)
}

// Ready downloads what is not in the cache yet, and says what it downloaded.
func (l *local) Ready(ctx context.Context) (string, error) {
	var got []string
	for _, a := range l.needs {
		dir, fetched, err := download(ctx, a)
		if err != nil {
			return "", err
		}
		l.dirs[a.name] = dir
		if fetched {
			got = append(got, fmt.Sprintf("%s (%d MB)", a.title, (a.size+500_000)/1_000_000))
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
	cmd := exec.CommandContext(ctx, l.in(helpers[platform], "bin", "sherpa-onnx-vad-with-offline-asr"),
		"--silero-vad-model="+l.in(silero, "silero_vad_v5.onnx"),
		// Phrases about as long as Whisper's: a word's time is found by
		// spreading a phrase's words over it.
		"--silero-vad-min-silence-duration=0.3",
		"--silero-vad-max-speech-duration=15",
		"--model-type=nemo_transducer",
		"--encoder="+l.in(parakeet, "encoder.int8.onnx"),
		"--decoder="+l.in(parakeet, "decoder.int8.onnx"),
		"--joiner="+l.in(parakeet, "joiner.int8.onnx"),
		"--tokens="+l.in(parakeet, "tokens.txt"),
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

// Voices are Kokoro's English voices, free; af_heart, its best, is the
// default.
func (l *local) Voices(context.Context) ([]Voice, error) {
	out := make([]Voice, len(kokoroVoices))
	for i, v := range kokoroVoices {
		out[i] = Voice{Name: v, Model: localSpeech, ModelName: "Kokoro 82M, on this machine", Default: v == "af_heart"}
	}
	return out, nil
}

// Languages: sherpa-onnx speaks Kokoro's English voices, and a script
// translated into another language would be read in an English voice.
func (l *local) Languages() [][2]string { return [][2]string{{"en", "English"}} }

// Speak has Kokoro say the line into a 24 kHz WAV. A word missing from the
// lexicon, a name or a number, is spoken by espeak-ng's rules instead.
func (l *local) Speak(ctx context.Context, text string, v Voice, out string) (string, Cost, error) {
	free := Cost{Known: true}
	sid := slices.Index(kokoroVoices, v.Name)
	if v.Model != localSpeech || sid < 0 {
		return "", free, fmt.Errorf("%s in %s is not a voice of this machine: choose one of Kokoro's", v.Name, v.Model)
	}
	lexicon := "lexicon-us-en.txt"
	if strings.HasPrefix(v.Name, "b") {
		lexicon = "lexicon-gb-en.txt"
	}
	speaking.Lock()
	defer speaking.Unlock()
	path := out + ".wav"
	cmd := exec.CommandContext(ctx, l.in(helpers[platform], "bin", "sherpa-onnx-offline-tts"),
		"--kokoro-model="+l.in(kokoro, "model.int8.onnx"),
		"--kokoro-voices="+l.in(kokoro, "voices.bin"),
		"--kokoro-tokens="+l.in(kokoro, "tokens.txt"),
		"--kokoro-lexicon="+l.in(kokoro, lexicon),
		"--kokoro-data-dir="+l.in(kokoro, "espeak-ng-data"),
		"--num-threads="+strconv.Itoa(min(runtime.NumCPU(), 8)),
		"--sid="+strconv.Itoa(sid),
		"--output-filename="+path,
		text)
	said, err := cmd.CombinedOutput()
	if err == nil {
		_, err = os.Stat(path)
	}
	if err != nil {
		return "", free, fmt.Errorf("%s did not speak on this machine: %s", v.Name, lastLine(string(said), err.Error()))
	}
	return path, free, nil
}
