// Command files is the bundle that reads, writes and edits files in the
// directory it was started in.
package main

import (
	_ "embed"

	"github.com/JonasPf/agent-plugins/internal/serve"
)

//go:embed manifest.json
var manifest []byte

//go:embed tools.json
var tools []byte

var runs = map[string]serve.Run{"read": read, "write": write, "edit": edit}

func main() { serve.Main(manifest, tools, runs) }
