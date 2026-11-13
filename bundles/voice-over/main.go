// Command voice-over is the bundle that puts a synthesized voice-over on a
// video. CONTRACT.md says what its tools take, write and return.
package main

import (
	_ "embed"

	"github.com/JonasPf/agent-plugins/internal/serve"
)

//go:embed manifest.json
var manifest []byte

//go:embed tools.json
var tools []byte

var runs = map[string]serve.Run{
	"voiceover_frames":     frames,
	"voiceover_transcribe": transcribe,
	"voiceover_script":     script,
	"voiceover_render":     renderVideo,
	// The review page, and the two tools only it calls.
	"voiceover_review":      review,
	"voiceover_review_data": reviewData,
	"voiceover_save_review": saveReview,
}

func main() { serve.Main(manifest, tools, runs, pages...) }
