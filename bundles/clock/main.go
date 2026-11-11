// Command clock is the bundle that says what the time is.
package main

import (
	_ "embed"

	"github.com/JonasPf/agent-plugins/internal/serve"
)

//go:embed manifest.json
var manifest []byte

//go:embed tools.json
var tools []byte

var runs = map[string]serve.Run{"clock": clock}

func main() { serve.Main(manifest, tools, runs) }
