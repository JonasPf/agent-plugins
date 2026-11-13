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

var runs = map[string]serve.Run{"voiceover_frames": frames}

func main() { serve.Main(manifest, tools, runs) }
