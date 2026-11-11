// Command web-search is the bundle that searches the web through a search API.
package main

import (
	_ "embed"

	"github.com/JonasPf/agent-plugins/internal/serve"
)

//go:embed manifest.json
var manifest []byte

//go:embed tools.json
var tools []byte

var runs = map[string]serve.Run{"web_search": webSearch}

func main() { serve.Main(manifest, tools, runs) }
