package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// gateway is a stand-in for OpenRouter: each path answers with the next of
// the handlers given for it, the last one again once they run out, and every
// request is kept for the test to read.
type gateway struct {
	t        *testing.T
	mu       sync.Mutex
	answers  map[string][]http.HandlerFunc
	requests []recorded
}

type recorded struct {
	path, query, auth string
	body              map[string]any
}

func newGateway(t *testing.T) (*gateway, *httptest.Server) {
	g := &gateway{t: t, answers: map[string][]http.HandlerFunc{}}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		b, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(b, &body)
		r.Body = io.NopCloser(bytes.NewReader(b))
		g.mu.Lock()
		g.requests = append(g.requests, recorded{r.URL.Path, r.URL.RawQuery, r.Header.Get("Authorization"), body})
		hs := g.answers[r.URL.Path]
		var h http.HandlerFunc
		if len(hs) > 0 {
			h = hs[0]
			if len(hs) > 1 {
				g.answers[r.URL.Path] = hs[1:]
			}
		}
		g.mu.Unlock()
		if h == nil {
			t.Errorf("the gateway was asked for %s, which this test does not answer", r.URL.Path)
			w.WriteHeader(404)
			return
		}
		h(w, r)
	}))
	t.Cleanup(srv.Close)
	return g, srv
}

func (g *gateway) on(path string, hs ...http.HandlerFunc) { g.answers[path] = hs }

func (g *gateway) asked(path string) []recorded {
	g.mu.Lock()
	defer g.mu.Unlock()
	var out []recorded
	for _, r := range g.requests {
		if r.path == path {
			out = append(out, r)
		}
	}
	return out
}

func answer(code int, body string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(code)
		_, _ = io.WriteString(w, body)
	}
}

// sound answers with audio of a media type, naming the generation it was.
func sound(mediaType string, audio []byte, generation string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", mediaType)
		w.Header().Set("X-Generation-Id", generation)
		_, _ = w.Write(audio)
	}
}

// testRouter is the backend pointed at a stand-in gateway, with no waits.
func testRouter(t *testing.T, base string) *openRouter {
	t.Setenv(keyName, "sk-test-secret")
	t.Setenv(urlName, base)
	o, err := newOpenRouter()
	if err != nil {
		t.Fatal(err)
	}
	o.costEvery = 0
	return o
}

func at(m map[string]any, path ...string) any {
	var v any = m
	for _, p := range path {
		mm, ok := v.(map[string]any)
		if !ok {
			return nil
		}
		v = mm[p]
	}
	return v
}

// "Transcribing asks for the times of every phrase, sends the recording as
// it is, and gives Whisper the terms as words already said, through the
// provider that reads them."
func TestTranscribeAsksForTimesAndSpellsTheTermsItIsGiven(t *testing.T) {
	g, srv := newGateway(t)
	g.on("/audio/transcriptions", answer(200, `{"text":"So in this case we use Blockdaemon.",
		"segments":[{"start":0,"end":2.5,"text":" So in this case"},{"start":2.5,"end":4.1,"text":" we use Blockdaemon."}],
		"usage":{"cost":0.0003}}`))
	o := testRouter(t, srv.URL)
	audio := filepath.Join(t.TempDir(), "audio.mp3")
	if err := os.WriteFile(audio, []byte("ID3 not really mp3"), 0o644); err != nil {
		t.Fatal(err)
	}

	segs, cost, err := o.Transcribe(context.Background(), audio, TranscribeOptions{Language: "en", Terms: "Blockdaemon, PII"})
	if err != nil {
		t.Fatal(err)
	}
	if len(segs) != 2 || segs[1].Start != 2.5 || segs[1].End != 4.1 || segs[1].Text != "we use Blockdaemon." {
		t.Errorf("segments = %+v", segs)
	}
	if !cost.Known || cost.Dollars != 0.0003 {
		t.Errorf("cost = %+v, want 0.0003", cost)
	}
	req := g.asked("/audio/transcriptions")[0]
	if req.auth != "Bearer sk-test-secret" {
		t.Errorf("Authorization = %q", req.auth)
	}
	data, _ := base64.StdEncoding.DecodeString(at(req.body, "input_audio", "data").(string))
	for what, ok := range map[string]bool{
		"the default model":       at(req.body, "model") == defaultTranscriber,
		"times":                   at(req.body, "response_format") == "verbose_json",
		"times of phrases":        strings.Contains(strings.Join(strs(at(req.body, "timestamp_granularities")), ","), "segment"),
		"the recording as it is":  string(data) == "ID3 not really mp3",
		"its format":              at(req.body, "input_audio", "format") == "mp3",
		"the language":            at(req.body, "language") == "en",
		"the provider that reads": strings.Join(strs(at(req.body, "provider", "order")), ",") == "deepinfra",
		"the terms as words said": at(req.body, "provider", "options", "deepinfra", "prompt") == "Blockdaemon, PII",
		"the terms for the other": at(req.body, "provider", "options", "groq", "prompt") == "Blockdaemon, PII",
	} {
		if !ok {
			t.Errorf("the request does not carry %s: %v", what, req.body)
		}
	}
}

func strs(v any) []string {
	var out []string
	for _, x := range v.([]any) {
		out = append(out, x.(string))
	}
	return out
}

// "A model that writes down the words but not when they were said is refused
// with that reason: a line cannot be placed without times."
func TestTranscribeRefusesAModelThatGivesNoTimes(t *testing.T) {
	g, srv := newGateway(t)
	g.on("/audio/transcriptions", answer(200, `{"text":"So in this case we use Blockdaemon.","usage":{"cost":0.0001}}`))
	o := testRouter(t, srv.URL)
	audio := filepath.Join(t.TempDir(), "audio.mp3")
	_ = os.WriteFile(audio, []byte("x"), 0o644)

	_, _, err := o.Transcribe(context.Background(), audio, TranscribeOptions{})
	if err == nil || !strings.Contains(err.Error(), "no times") {
		t.Fatalf("err = %v, want a refusal saying it gave no times", err)
	}
}

// "A gateway that says to slow down is waited out and asked again; one that
// keeps saying it is reported, not asked forever."
func TestAGatewayThatLimitsIsWaitedOutAndAskedAgain(t *testing.T) {
	limited := func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Retry-After", "0")
		w.WriteHeader(429)
		_, _ = io.WriteString(w, `{"error":{"message":"Rate limit exceeded"}}`)
	}
	g, srv := newGateway(t)
	g.on("/audio/transcriptions", limited, limited, answer(200, `{"text":"hi","segments":[{"start":0,"end":1,"text":"hi"}]}`))
	o := testRouter(t, srv.URL)
	audio := filepath.Join(t.TempDir(), "audio.mp3")
	_ = os.WriteFile(audio, []byte("x"), 0o644)

	if _, _, err := o.Transcribe(context.Background(), audio, TranscribeOptions{}); err != nil {
		t.Fatalf("after two 429s and an answer: %v", err)
	}
	if n := len(g.asked("/audio/transcriptions")); n != 3 {
		t.Errorf("asked %d times, want 3", n)
	}

	g.on("/audio/transcriptions", limited)
	_, _, err := o.Transcribe(context.Background(), audio, TranscribeOptions{})
	if err == nil || !strings.Contains(err.Error(), "429") || !strings.Contains(err.Error(), "Rate limit exceeded") {
		t.Fatalf("err = %v, want the gateway's own words and its 429", err)
	}
	if n := len(g.asked("/audio/transcriptions")); n != 3+rateLimitAttempts {
		t.Errorf("a gateway that kept limiting was asked %d more times, want %d", n-3, rateLimitAttempts)
	}
}

// "Speech is saved under the name of what came back, whatever was asked for:
// HeyGen gives raw samples, which are made a WAV; ElevenLabs gives MP3; and a
// provider that gives MP3 where samples were asked for is saved as MP3, not
// wrapped in a WAV header it is not."
func TestSpeakNamesTheFileByWhatCameBack(t *testing.T) {
	samples := make([]byte, 4800) // 0.1 s of silence at 24 kHz, 16 bits
	mp3 := append([]byte("ID3\x04\x00\x00\x00\x00\x00\x00"), make([]byte, 64)...)
	pcmOnly := answer(400, `{"error":{"message":"response_format must be pcm for this model"}}`)
	for _, c := range []struct {
		name   string
		voice  Voice
		speech []http.HandlerFunc
		ext    string
		asked  []string
	}{
		{"raw samples become a WAV", Voice{Name: "gareth", Model: "heygen/heygen-voice-1-turbo"},
			[]http.HandlerFunc{pcmOnly, sound("audio/pcm;rate=24000;channels=1", samples, "gen-1")}, ".wav", []string{"mp3", "pcm"}},
		{"MP3 stays MP3", Voice{Name: "brian", Model: "elevenlabs/eleven-flash-v2.5"},
			[]http.HandlerFunc{sound("audio/mpeg", mp3, "gen-1")}, ".mp3", []string{"mp3"}},
		{"MP3 where samples were asked for", Voice{Name: "gareth", Model: "heygen/heygen-voice-1-turbo"},
			[]http.HandlerFunc{pcmOnly, sound("audio/pcm", mp3, "gen-1")}, ".mp3", []string{"mp3", "pcm"}},
	} {
		t.Run(c.name, func(t *testing.T) {
			g, srv := newGateway(t)
			g.on("/audio/speech", c.speech...)
			g.on("/generation", answer(404, `{"error":{"message":"not found"}}`), answer(200, `{"data":{"usage":0.0000216}}`))
			o := testRouter(t, srv.URL)
			out := filepath.Join(t.TempDir(), "line")

			path, cost, err := o.Speak(context.Background(), "Hello there.", c.voice, out)
			if err != nil {
				t.Fatal(err)
			}
			if path != out+c.ext {
				t.Errorf("saved as %s, want %s", path, out+c.ext)
			}
			b, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if c.ext == ".wav" {
				if string(b[:4]) != "RIFF" || string(b[8:12]) != "WAVE" || binary.LittleEndian.Uint32(b[24:]) != 24000 {
					t.Errorf("not a 24 kHz WAV: % x", b[:44])
				}
				if len(b) != 44+len(samples) {
					t.Errorf("the WAV holds %d bytes, want the header and every sample", len(b))
				}
			} else if string(b[:3]) != "ID3" {
				t.Errorf("the MP3 was changed: % x", b[:12])
			}
			var formats []string
			for _, r := range g.asked("/audio/speech") {
				formats = append(formats, at(r.body, "response_format").(string))
				if at(r.body, "input") != "Hello there." || at(r.body, "voice") != c.voice.Name || at(r.body, "model") != c.voice.Model {
					t.Errorf("the request does not carry the line, the voice and the model: %v", r.body)
				}
			}
			if strings.Join(formats, ",") != strings.Join(c.asked, ",") {
				t.Errorf("asked for %v, want %v", formats, c.asked)
			}
			if !cost.Known || cost.Dollars != 0.0000216 {
				t.Errorf("cost = %+v: the generation's cost, asked again until the gateway has it", cost)
			}
			if q := g.asked("/generation")[0].query; q != "id=gen-1" {
				t.Errorf("the cost was asked for %q", q)
			}
		})
	}
}

// "A free model is refused before anything is asked: its rate limit turned
// 27 lines into 43 rounds of waiting, which cost more than the paid voice."
func TestSpeakRefusesAFreeModel(t *testing.T) {
	g, srv := newGateway(t)
	o := testRouter(t, srv.URL)
	_, _, err := o.Speak(context.Background(), "Hello.", Voice{Name: "x", Model: "somebody/voice:free"}, filepath.Join(t.TempDir(), "line"))
	if err == nil || !strings.Contains(err.Error(), "free") {
		t.Fatalf("err = %v, want a refusal of the free model", err)
	}
	if len(g.requests) != 0 {
		t.Errorf("the gateway was asked %d times", len(g.requests))
	}
}

// "The voices are every voice of every paid model that speaks, with what a
// million characters cost in it."
func TestVoicesListsEveryVoiceOfEveryPaidModelWithItsPrice(t *testing.T) {
	g, srv := newGateway(t)
	g.on("/models", answer(200, `{"data":[
		{"id":"heygen/heygen-voice-1-turbo","name":"HeyGen: Voice 1 Turbo","architecture":{"output_modalities":["speech"]},
		 "supported_voices":["gareth","bram"],"pricing":{"prompt":"0.000004"}},
		{"id":"elevenlabs/eleven-flash-v2.5","name":"ElevenLabs: Flash v2.5","architecture":{"output_modalities":["speech"]},
		 "supported_voices":["brian"],"pricing":{"prompt":"0.00004"}},
		{"id":"somebody/voice:free","name":"Free voice","architecture":{"output_modalities":["speech"]},
		 "supported_voices":["any"],"pricing":{"prompt":"0"}}]}`))
	o := testRouter(t, srv.URL)

	vs, err := o.Voices(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, v := range vs {
		got = append(got, v.Model+" "+v.Name)
	}
	want := "elevenlabs/eleven-flash-v2.5 brian,heygen/heygen-voice-1-turbo bram,heygen/heygen-voice-1-turbo gareth"
	if strings.Join(got, ",") != want {
		t.Errorf("voices = %v, want %s", got, want)
	}
	for _, v := range vs {
		if v.Name == "gareth" && (v.PerMillion != 4 || v.ModelName != "HeyGen: Voice 1 Turbo") {
			t.Errorf("gareth = %+v, want $4 per million characters and the model's name", v)
		}
	}
	if q := g.asked("/models")[0].query; q != "output_modalities=speech" {
		t.Errorf("the models were asked for with %q", q)
	}
}

// "Without a key nothing is asked, and the refusal says where the operator
// sets one; a placeholder the host left unfilled is no key."
func TestNoKeySaysWhereToSetOne(t *testing.T) {
	for _, v := range []string{"", "  ", "${user_config.openrouter_api_key}"} {
		t.Setenv(keyName, v)
		_, err := newOpenRouter()
		if err == nil || !strings.Contains(err.Error(), "OpenRouter API key") || !strings.Contains(err.Error(), "settings") {
			t.Errorf("key %q: err = %v, want a refusal saying where the key is set", v, err)
		}
	}
}

// "A cost too small to show at a hundredth of a cent is said to be under
// that, not shown as nothing: it was not free."
func TestASmallCostIsNotShownAsFree(t *testing.T) {
	for _, c := range []struct {
		cost Cost
		want string
	}{
		{Cost{0.000003, true}, "under $0.0001"},
		{Cost{0, true}, "$0.0000"},
		{Cost{0.0003, true}, "$0.0003"},
		{Cost{0.24, true}, "$0.24"},
		{Cost{}, "cost unknown"},
	} {
		if got := c.cost.String(); got != c.want {
			t.Errorf("%+v reads %q, want %q", c.cost, got, c.want)
		}
	}
}
