package main

import (
	"context"
	"fmt"
)

// A backend is what writes a recording down and what speaks a line. The tools
// reach them through these two interfaces only, so a backend that runs on the
// machine itself can stand beside the cloud one without a tool changing.

// Transcriber writes down what is said in a recording, with when.
type Transcriber interface {
	// Name is the backend, as a result names it.
	Name() string
	// Transcribe reads the audio file and returns its phrases, timed from the
	// start of that file.
	Transcribe(ctx context.Context, audio string, o TranscribeOptions) ([]Segment, Cost, error)
}

// Speaker speaks a line in a voice.
type Speaker interface {
	Name() string
	// Voices is every voice there is to speak in.
	Voices(ctx context.Context) ([]Voice, error)
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
