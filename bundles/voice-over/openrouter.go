package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"mime"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

// The cloud backend: OpenRouter, a gateway to many providers' models, called
// with the bundle's own key. It is ported from the agent's call_model, which
// the voice-over skill reached it through, and keeps what that learned.

const (
	// keyName and urlName are the variables the host fills from the bundle's
	// settings. The server never reads OPENROUTER_API_KEY, so it cannot pick
	// up a host's own key by accident.
	keyName = "VOICEOVER_OPENROUTER_KEY"
	urlName = "VOICEOVER_OPENROUTER_URL"

	defaultGateway     = "https://openrouter.ai/api/v1"
	defaultTranscriber = "openai/whisper-large-v3-turbo"
	defaultSpeech      = "heygen/heygen-voice-1-turbo"
	defaultVoice       = "gareth"

	// A gateway that says to slow down (429) is waited out: as long as it
	// asks, or this long when it does not say, and at most this many times.
	rateLimitAttempts = 3
	rateLimitWait     = 30 * time.Second

	// The gateway records what speech cost some fifteen seconds after it
	// answered, so the cost is asked for again until it is there, for up to
	// this long. Past that it is unknown, not free.
	costWithin = 60 * time.Second

	maxSpeech = 100 << 20
)

type openRouter struct {
	key, base string
	client    *http.Client
	// costEvery is the wait between asks for a generation's cost.
	costEvery time.Duration
}

// newOpenRouter is the backend as the bundle's settings have it. A key is
// the operator's to set, so the refusal is addressed to them and says where.
func newOpenRouter() (*openRouter, error) {
	key := setting(keyName)
	if key == "" {
		return nil, fmt.Errorf("no OpenRouter API key: %s is not set, so nothing can be transcribed or spoken. "+
			"The operator sets the key in this bundle's settings; until then, voiceover_frames still works", keyName)
	}
	base := setting(urlName)
	if base == "" {
		base = defaultGateway
	}
	return &openRouter{key: key, base: strings.TrimRight(base, "/"),
		client: &http.Client{Timeout: 10 * time.Minute}, costEvery: 3 * time.Second}, nil
}

func (o *openRouter) Name() string { return "OpenRouter" }

// call sends one request, waiting out a gateway that says to slow down.
func (o *openRouter) call(ctx context.Context, method, path string, body any) (*http.Response, error) {
	var payload []byte
	if body != nil {
		payload, _ = json.Marshal(body)
	}
	for attempt := 1; ; attempt++ {
		var r io.Reader
		if payload != nil {
			r = bytes.NewReader(payload)
		}
		req, err := http.NewRequestWithContext(ctx, method, o.base+path, r)
		if err != nil {
			return nil, err
		}
		req.Header.Set("Authorization", "Bearer "+o.key)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("HTTP-Referer", "https://github.com/JonasPf/agent-plugins")
		req.Header.Set("X-Title", "voice-over")
		resp, err := o.client.Do(req)
		if err != nil {
			return nil, fmt.Errorf("the gateway could not be reached: %v", err)
		}
		if resp.StatusCode != http.StatusTooManyRequests || attempt == rateLimitAttempts {
			return resp, nil
		}
		wait := rateLimitWait
		if s, err := strconv.Atoi(resp.Header.Get("Retry-After")); err == nil && s >= 0 {
			wait = time.Duration(s) * time.Second
		}
		resp.Body.Close()
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(wait):
		}
	}
}

// gatewayError reads a refusal: the gateway's own words where it gave some.
func gatewayError(resp *http.Response) error {
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	var e struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	said := strings.TrimSpace(string(b))
	if json.Unmarshal(b, &e) == nil && e.Error.Message != "" {
		said = e.Error.Message
	}
	if resp.StatusCode == http.StatusTooManyRequests {
		return fmt.Errorf("the gateway went on limiting requests (429: %s) after %d attempts: wait a minute and call again",
			said, rateLimitAttempts)
	}
	return fmt.Errorf("the gateway refused (%d): %s", resp.StatusCode, said)
}

// Transcribe asks for the time of every phrase. A model that writes the
// words down without them is refused rather than used: a line cannot be
// placed without times. (gpt-4o-mini-transcribe gives none.)
func (o *openRouter) Transcribe(ctx context.Context, audio string, opt TranscribeOptions) ([]Segment, Cost, error) {
	model := opt.Model
	if model == "" {
		model = defaultTranscriber
	}
	data, err := os.ReadFile(audio)
	if err != nil {
		return nil, Cost{}, err
	}
	body := map[string]any{
		"model":                   model,
		"input_audio":             map[string]string{"data": base64.StdEncoding.EncodeToString(data), "format": strings.TrimPrefix(filepath.Ext(audio), ".")},
		"response_format":         "verbose_json",
		"timestamp_granularities": []string{"segment"},
	}
	if opt.Language != "" {
		body["language"] = opt.Language
	}
	// Whisper reads a prompt as words said before the recording, which is how
	// it learns to spell a name: without one, "Blockdaemon" came back as
	// "block demand". The gateway passes a prompt on only as a provider's own
	// option. Of Whisper turbo's providers on 2026-11-03, DeepInfra read it
	// and Groq did not, so DeepInfra is asked first.
	if terms := strings.TrimSpace(opt.Terms); terms != "" {
		words := map[string]string{"prompt": terms}
		body["provider"] = map[string]any{"order": []string{"deepinfra"},
			"options": map[string]any{"deepinfra": words, "groq": words}}
	}
	resp, err := o.call(ctx, "POST", "/audio/transcriptions", body)
	if err != nil {
		return nil, Cost{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, Cost{}, fmt.Errorf("%s did not transcribe it: %v", model, gatewayError(resp))
	}
	var r struct {
		Text     string `json:"text"`
		Segments []struct {
			Start float64 `json:"start"`
			End   float64 `json:"end"`
			Text  string  `json:"text"`
		} `json:"segments"`
		Usage *struct {
			Cost float64 `json:"cost"`
		} `json:"usage"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&r); err != nil {
		return nil, Cost{}, fmt.Errorf("%s answered with something that is not a transcript: %v", model, err)
	}
	var cost Cost
	if r.Usage != nil {
		cost = Cost{r.Usage.Cost, true}
	}
	if len(r.Segments) == 0 && strings.TrimSpace(r.Text) != "" {
		return nil, cost, fmt.Errorf("%s wrote the words down with no times, and a line cannot be placed without them: "+
			"transcribe with %s", model, defaultTranscriber)
	}
	segs := make([]Segment, 0, len(r.Segments))
	for _, s := range r.Segments {
		segs = append(segs, Segment{s.Start, s.End, strings.TrimSpace(s.Text)})
	}
	return segs, cost, nil
}

// Voices is every voice of every paid model the gateway lists as speaking.
// A million characters is about 1,700 minutes of speech.
func (o *openRouter) Voices(ctx context.Context) ([]Voice, error) {
	resp, err := o.call(ctx, "GET", "/models?output_modalities=speech", nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("the voices could not be listed: %v", gatewayError(resp))
	}
	var payload struct {
		Data []struct {
			ID           string `json:"id"`
			Name         string `json:"name"`
			Architecture struct {
				Outputs []string `json:"output_modalities"`
			} `json:"architecture"`
			Voices  []string `json:"supported_voices"`
			Pricing struct {
				Prompt string `json:"prompt"`
			} `json:"pricing"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return nil, fmt.Errorf("the voices could not be listed: %v", err)
	}
	var out []Voice
	for _, m := range payload.Data {
		if strings.HasSuffix(m.ID, ":free") || !speaks(m.Architecture.Outputs) {
			continue
		}
		perChar, _ := strconv.ParseFloat(m.Pricing.Prompt, 64)
		// The gateway gives a price per character; rounded to a millionth of
		// a dollar, so $4 a million reads as 4 and not 3.9999999999999996.
		per := math.Round(perChar*1e12) / 1e6
		for _, v := range m.Voices {
			out = append(out, Voice{Name: v, Model: m.ID, ModelName: m.Name, PerMillion: per})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Model != out[j].Model {
			return out[i].Model < out[j].Model
		}
		return out[i].Name < out[j].Name
	})
	return out, nil
}

func speaks(outputs []string) bool {
	for _, o := range outputs {
		if o == "speech" {
			return true
		}
	}
	return false
}

// Speak asks for MP3. A model that gives only raw samples refuses that by
// naming pcm, and is asked again for them. What comes back is saved under the
// name of what it is, whatever was asked for: HeyGen's samples are made a WAV,
// ElevenLabs' MP3 stays MP3.
func (o *openRouter) Speak(ctx context.Context, text string, v Voice, out string) (string, Cost, error) {
	if strings.HasSuffix(v.Model, ":free") {
		return "", Cost{}, fmt.Errorf("%s is a free model: its rate limit once turned 27 lines into 43 rounds of waiting, "+
			"which cost more than a paid voice. Choose a voice of a paid model", v.Model)
	}
	audio, mediaType, gen, err := o.speakAs(ctx, text, v, "mp3")
	if err != nil && strings.Contains(err.Error(), "pcm") {
		audio, mediaType, gen, err = o.speakAs(ctx, text, v, "pcm")
	}
	if err != nil {
		return "", Cost{}, err
	}
	ext, data, err := speechFile(audio, mediaType)
	if err != nil {
		return "", Cost{}, fmt.Errorf("%s in %s: %v", v.Name, v.Model, err)
	}
	path := out + ext
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return "", Cost{}, err
	}
	return path, o.generationCost(ctx, gen), nil
}

func (o *openRouter) speakAs(ctx context.Context, text string, v Voice, format string) ([]byte, string, string, error) {
	resp, err := o.call(ctx, "POST", "/audio/speech",
		map[string]any{"model": v.Model, "voice": v.Name, "input": text, "response_format": format})
	if err != nil {
		return nil, "", "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, "", "", fmt.Errorf("%s in %s did not speak: %v", v.Name, v.Model, gatewayError(resp))
	}
	audio, err := io.ReadAll(io.LimitReader(resp.Body, maxSpeech))
	if err != nil {
		return nil, "", "", err
	}
	return audio, resp.Header.Get("Content-Type"), resp.Header.Get("X-Generation-Id"), nil
}

// speechFile is what audio is, by its first bytes before its media type: a
// provider's label has been wrong, and a file named for what it is not plays
// as noise or not at all.
func speechFile(b []byte, mediaType string) (string, []byte, error) {
	switch {
	case len(b) >= 12 && string(b[:4]) == "RIFF" && string(b[8:12]) == "WAVE":
		return ".wav", b, nil
	case len(b) >= 3 && string(b[:3]) == "ID3":
		return ".mp3", b, nil
	case strings.HasPrefix(mediaType, "audio/pcm") || strings.HasPrefix(mediaType, "audio/L16"):
		rate, channels := pcmFormat(mediaType)
		return ".wav", wavOf(b, rate, channels), nil
	case strings.HasPrefix(mediaType, "audio/mpeg") || (len(b) >= 2 && b[0] == 0xFF && b[1]&0xE0 == 0xE0):
		return ".mp3", b, nil
	}
	return "", nil, fmt.Errorf("the speech that came back is not MP3, WAV or raw samples (%q, %d bytes)", mediaType, len(b))
}

// pcmFormat reads the sample rate and channels from a raw-audio media type,
// audio/pcm;rate=24000;channels=1. What it leaves out is what the speech
// endpoints default to: 24 kHz, one channel.
func pcmFormat(mediaType string) (rate, channels int) {
	rate, channels = 24000, 1
	_, params, err := mime.ParseMediaType(mediaType)
	if err != nil {
		return rate, channels
	}
	if n, err := strconv.Atoi(params["rate"]); err == nil && n > 0 {
		rate = n
	}
	if n, err := strconv.Atoi(params["channels"]); err == nil && n > 0 {
		channels = n
	}
	return rate, channels
}

// wavOf wraps 16-bit little-endian samples in the header that makes them a
// WAV file anything plays.
func wavOf(pcm []byte, rate, channels int) []byte {
	const bits = 16
	block := channels * bits / 8
	h := make([]byte, 44)
	le32 := func(at, v int) { binary.LittleEndian.PutUint32(h[at:], uint32(v)) }
	le16 := func(at, v int) { binary.LittleEndian.PutUint16(h[at:], uint16(v)) }
	copy(h[0:], "RIFF")
	le32(4, 36+len(pcm))
	copy(h[8:], "WAVEfmt ")
	le32(16, 16)
	le16(20, 1)
	le16(22, channels)
	le32(24, rate)
	le32(28, rate*block)
	le16(32, block)
	le16(34, bits)
	copy(h[36:], "data")
	le32(40, len(pcm))
	return append(h, pcm...)
}

// generationCost asks what a generation cost until the gateway has it.
func (o *openRouter) generationCost(ctx context.Context, id string) Cost {
	if id == "" {
		return Cost{}
	}
	ctx, cancel := context.WithTimeout(ctx, costWithin)
	defer cancel()
	for attempt := 0; ; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return Cost{}
			case <-time.After(o.costEvery):
			}
		}
		resp, err := o.call(ctx, "GET", "/generation?id="+url.QueryEscape(id), nil)
		if err != nil {
			if ctx.Err() != nil {
				return Cost{}
			}
			continue
		}
		// The cost is under "usage"; "total_cost" is read too, as the name
		// the gateway's documentation has used.
		var g struct {
			Data struct {
				Usage     *float64 `json:"usage"`
				TotalCost *float64 `json:"total_cost"`
			} `json:"data"`
		}
		ok := resp.StatusCode == http.StatusOK && json.NewDecoder(resp.Body).Decode(&g) == nil
		resp.Body.Close()
		switch {
		case ok && g.Data.Usage != nil:
			return Cost{*g.Data.Usage, true}
		case ok && g.Data.TotalCost != nil:
			return Cost{*g.Data.TotalCost, true}
		}
	}
}
