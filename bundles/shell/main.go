// Command shell is the bundle that runs shell commands in the directory it was
// started in.
package main

import (
	_ "embed"

	"github.com/JonasPf/agent-plugins/internal/serve"
)

//go:embed manifest.json
var manifest []byte

//go:embed tools.json
var tools []byte

var runs = map[string]serve.Run{"bash": bash}

func main() { serve.Main(manifest, tools, runs) }
