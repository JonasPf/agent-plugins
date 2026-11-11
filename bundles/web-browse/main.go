// Command web-browse is the bundle that opens a URL in a real browser.
package main

import (
	_ "embed"

	"github.com/JonasPf/agent-plugins/internal/serve"
)

//go:embed manifest.json
var manifest []byte

//go:embed tools.json
var tools []byte

var runs = map[string]serve.Run{"web_browse": webBrowse}

func main() { serve.Main(manifest, tools, runs) }
