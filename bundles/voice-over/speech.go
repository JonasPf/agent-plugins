package main

import (
	"context"
	"fmt"
	"strings"
)

// A backend is what writes a recording down and what speaks a line. The tools
// reach them through these two interfaces only, so a backend that runs on the
// machine itself can stand beside the cloud one without a tool changing.

// Transcriber writes down what is said in a recording, with when.
type Transcriber interface {
	// Name is the backend, as a result names it.
	Name() string
	// Model is the model that writes it down, as a result names it.
	Model() string
	// Ready gets the backend ready to run, and says what that took: the local
	// backend downloads what it runs on, the first time.
	Ready(ctx context.Context) (string, error)
	// Transcribe reads the audio file and returns its phrases, timed from the
	// start of that file.
	Transcribe(ctx context.Context, audio string, o TranscribeOptions) ([]Segment, Cost, error)
}

// Speaker speaks a line in a voice.
type Speaker interface {
	Name() string
	// Ready gets the backend ready to speak, and says what that took.
	Ready(ctx context.Context) (string, error)
	// Voices is every voice there is to speak in; one is the default.
	Voices(ctx context.Context) ([]Voice, error)
	// Languages are those a script may be translated into for its voices,
	// as code and name.
	Languages() [][2]string
	// Speak writes the line spoken to out plus the extension of what came
	// back, and returns that path.
	Speak(ctx context.Context, text string, v Voice, out string) (string, Cost, error)
}

// TranscribeOptions is what a transcription can be told. Model is the
// backend's default when empty.
type TranscribeOptions struct {
	Model    string
	Language string
	// Terms are names and terms the recording is likely to use,
	// comma-separated. Whisper takes them as words already said, and spells
	// them that way.
	Terms string
}

// Segment is a phrase of a transcript and when it was said, in seconds.
type Segment struct {
	Start, End float64
	Text       string
}

// Voice is one voice of one model, and what a million characters cost in it.
type Voice struct {
	Name       string
	Model      string
	ModelName  string
	PerMillion float64
	// Default is the voice spoken in when none was chosen.
	Default bool
	// Language is the one language the voice speaks, as code and name, or
	// none when it speaks every language of its backend's.
	Language [2]string
}

// chosenBackend is where the bundle's settings say to transcribe and speak:
// openrouter or local. The setting is auto, openrouter or local, and auto is
// OpenRouter when a key is set and this machine when not.
func chosenBackend() (string, error) {
	switch b := strings.ToLower(setting(backendName)); b {
	case "openrouter", "local":
		return b, nil
	case "", "auto":
		if setting(keyName) != "" {
			return "openrouter", nil
		}
		return "local", nil
	default:
		return "", fmt.Errorf("the backend setting (%s) is %q: it is auto, openrouter or local. The operator sets it in this bundle's settings", backendName, b)
	}
}

func newTranscriber() (Transcriber, error) {
	b, err := chosenBackend()
	if err != nil {
		return nil, err
	}
	if b == "local" {
		return newLocal(forHearing, helpers[platform], parakeet)
	}
	return newOpenRouter()
}

func newSpeaker() (Speaker, error) {
	b, err := chosenBackend()
	if err != nil {
		return nil, err
	}
	if b == "local" {
		return newLocal(forSpeaking, helpers[platform], kokoro)
	}
	return newOpenRouter()
}

// Cost is what a call cost, in dollars. A backend that cannot say leaves it
// unknown rather than calling it free.
type Cost struct {
	Dollars float64
	Known   bool
}

// Plus is the cost of two calls: known only if both are.
func (c Cost) Plus(o Cost) Cost {
	return Cost{c.Dollars + o.Dollars, c.Known && o.Known}
}

func (c Cost) String() string {
	if !c.Known {
		return "cost unknown"
	}
	return dollars(c.Dollars)
}

// dollars shows small sums to the tenth of a cent and below, where speech and
// transcription costs are.
func dollars(d float64) string {
	if d > 0 && d < 0.00005 {
		return "under $0.0001"
	}
	if d < 0.01 {
		return fmt.Sprintf("$%.4f", d)
	}
	return fmt.Sprintf("$%.2f", d)
}
