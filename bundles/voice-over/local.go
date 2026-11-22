package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
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

// The recording is heard in chunks, each written down by one run of the
// helper with the time of every token, and those times are what a phrase's
// start and end are made of.
const (
	chunkSeconds   = 30.0
	overlapSeconds = 2.0
	chunkStep      = chunkSeconds - overlapSeconds
	// chunksPerRun is how many chunks one run of the helper hears: each run
	// loads the model again, and every chunk it holds takes memory.
	chunksPerRun = 8
	// A phrase ends at a sentence's end, at a pause this long, and at a
	// comma once it is this long.
	pauseEnds = 0.6
	commaEnds = 4.0
)

// chunkHeard is what the helper says of one chunk: its tokens, a token with a
// space (or ▁) before it starting a word, each with its start and length in
// seconds from the chunk's start.
type chunkHeard struct {
	Tokens     []string  `json:"tokens"`
	Timestamps []float64 `json:"timestamps"`
	Durations  []float64 `json:"durations"`
}

// heardWord is a word and when it was said, from the recording's start.
type heardWord struct {
	text       string
	start, end float64
}

// Transcribe writes the recording down in chunks of chunkSeconds, each
// overlapping the next by overlapSeconds so no word is lost at a cut. The
// language and the terms are not used: the model finds the language itself,
// and takes no words to expect.
func (l *local) Transcribe(ctx context.Context, audio string, _ TranscribeOptions) ([]Segment, Cost, error) {
	free := Cost{Known: true}
	ff, err := findFFmpeg()
	if err != nil {
		return nil, free, err
	}
	dir, err := os.MkdirTemp(filepath.Dir(audio), "heard-")
	if err != nil {
		return nil, free, err
	}
	defer os.RemoveAll(dir)
	// The helper hears 16-bit samples at 16 kHz; the chunks are cut from them
	// here, as WAVs, which is what the helper reads.
	raw := filepath.Join(dir, "all.pcm")
	if _, err := ff.run(ctx, "-i", audio, "-ac", "1", "-ar", "16000", "-f", "s16le", "-c:a", "pcm_s16le", raw); err != nil {
		return nil, free, fmt.Errorf("could not make the sound into samples for the model: %v", err)
	}
	pcm, err := os.ReadFile(raw)
	if err != nil {
		return nil, free, err
	}
	const perSecond = 16000 * 2
	var chunks []string
	for at := 0; at == 0 || at*perSecond < len(pcm); at += int(chunkStep) {
		from, to := at*perSecond, min((at+int(chunkSeconds))*perSecond, len(pcm))
		path := filepath.Join(dir, fmt.Sprintf("%04d.wav", len(chunks)))
		if err := os.WriteFile(path, wavOf(pcm[from:to], 16000, 1), 0o644); err != nil {
			return nil, free, err
		}
		chunks = append(chunks, path)
		if to == len(pcm) {
			break
		}
	}
	var heard []chunkHeard
	for i := 0; i < len(chunks); i += chunksPerRun {
		got, err := l.hear(ctx, chunks[i:min(i+chunksPerRun, len(chunks))])
		if err != nil {
			return nil, free, err
		}
		heard = append(heard, got...)
	}
	return phrases(placeWords(heard)), free, nil
}

// hear runs the helper once over some chunks, and reads its one line of JSON
// for each.
func (l *local) hear(ctx context.Context, chunks []string) ([]chunkHeard, error) {
	args := []string{
		"--model-type=nemo_transducer",
		"--encoder=" + l.in(parakeet, "encoder.int8.onnx"),
		"--decoder=" + l.in(parakeet, "decoder.int8.onnx"),
		"--joiner=" + l.in(parakeet, "joiner.int8.onnx"),
		"--tokens=" + l.in(parakeet, "tokens.txt"),
		"--num-threads=" + strconv.Itoa(min(runtime.NumCPU(), 8)),
	}
	cmd := exec.CommandContext(ctx, l.in(helpers[platform], "bin", "sherpa-onnx-offline"), append(args, chunks...)...)
	var out, said bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &said
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("the model on this machine stopped: %s", lastLine(said.String(), err.Error()))
	}
	var heard []chunkHeard
	for _, line := range strings.Split(out.String(), "\n") {
		if line = strings.TrimSpace(line); !strings.HasPrefix(line, "{") {
			continue
		}
		var c chunkHeard
		if err := json.Unmarshal([]byte(line), &c); err != nil {
			return nil, fmt.Errorf("the model on this machine answered with what is not its JSON: %v", err)
		}
		heard = append(heard, c)
	}
	if len(heard) != len(chunks) {
		return nil, fmt.Errorf("the model on this machine heard %d of %d chunks: %s", len(heard), len(chunks), lastLine(said.String(), ""))
	}
	return heard, nil
}

// placeWords joins the chunks' tokens into words at their times in the
// whole recording. Where two chunks overlap they meet in the middle: a word
// is taken from the chunk in which it starts further from the edge.
func placeWords(chunks []chunkHeard) []heardWord {
	var words []heardWord
	for k, c := range chunks {
		offset := float64(k) * chunkStep
		lo, hi := offset+overlapSeconds/2, offset+chunkStep+overlapSeconds/2
		if k == 0 {
			lo = 0
		}
		if k == len(chunks)-1 {
			hi = math.Inf(1)
		}
		var mine []heardWord
		for i, tok := range c.Tokens {
			if i >= len(c.Timestamps) {
				break
			}
			start := offset + c.Timestamps[i]
			end := start + 0.3
			if i < len(c.Durations) {
				end = start + c.Durations[i]
			} else if i+1 < len(c.Timestamps) {
				end = min(offset+c.Timestamps[i+1], start+0.5)
			}
			text := strings.TrimLeft(tok, " ▁")
			if text != tok || len(mine) == 0 {
				mine = append(mine, heardWord{text, start, end})
				continue
			}
			mine[len(mine)-1].text += text
			mine[len(mine)-1].end = end
		}
		for _, w := range mine {
			if w.start >= lo && w.start < hi && w.text != "" {
				words = append(words, w)
			}
		}
	}
	return words
}

// phrases groups words into the transcript's lines: a line ends at a
// sentence's end, at a pause, or at a comma once it is commaEnds long.
func phrases(words []heardWord) []Segment {
	var segs []Segment
	var cur []string
	start := 0.0
	for i, w := range words {
		if len(cur) == 0 {
			start = w.start
		}
		cur = append(cur, w.text)
		last := w.text[len(w.text)-1]
		end := i+1 == len(words) || strings.ContainsRune(".?!", rune(last)) ||
			words[i+1].start-w.end >= pauseEnds || strings.ContainsRune(",;:", rune(last)) && w.end-start >= commaEnds
		if end {
			segs = append(segs, Segment{start, w.end, strings.Join(cur, " ")})
			cur = nil
		}
	}
	return segs
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
