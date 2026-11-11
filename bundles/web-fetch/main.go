// Command web-fetch is the bundle that fetches a URL over HTTP.
package main

import (
	_ "embed"

	"github.com/JonasPf/agent-plugins/internal/serve"
)

//go:embed manifest.json
var manifest []byte

//go:embed tools.json
var tools []byte

var runs = map[string]serve.Run{"web_fetch": webFetch}

func main() { serve.Main(manifest, tools, runs) }
