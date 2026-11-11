// web_browse opens a URL the way a person does — scripts run, the DOM settles —
// and returns the page's text.
//
// There is one way through here. The tool used to fall back to a plain HTTP GET
// when no browser was installed, which meant a page that needed its scripts came
// back as the shell it ships as, indistinguishable from a page that simply says
// little. A caller could not tell which it had. The browser is now in the image,
// so its absence is a broken image and says so. A plain fetch is a different
// thing to want, and when it is wanted it should be a tool of its own that the
// model chooses on purpose.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/JonasPf/agent-plugins/internal/serve"
	"github.com/JonasPf/agent-plugins/internal/web"
)

type args struct {
	URL string `json:"url"`
}

const limit = 40000

// inBrowser runs the page and reads the settled DOM. A bot check served in the
// page's place is reported as one: nothing here answers or evades it, and its
// text returned as the page would be an answer to a question nobody asked.
func inBrowser(url string) (string, error) {
	dom, err := web.Render(url, 15)
	if err != nil {
		return "", err
	}
	if body, ok := web.UnwrapPlain(dom); ok {
		return body, nil
	}
	if vendor := BotCheck(dom); vendor != "" {
		return "", fmt.Errorf("%s served a bot check (%s) instead of the page. Nothing here answers "+
			"or evades one, and a browser started any other way meets the same check: find "+
			"the information somewhere else, or ask the user to open the page.", url, vendor)
	}
	return web.ToText(dom), nil
}

func webBrowse(_ context.Context, raw json.RawMessage) (string, error) {
	var a args
	if err := serve.Args(raw, &a); err != nil {
		return "", err
	}
	url := strings.TrimSpace(a.URL)
	if url == "" {
		return "", errors.New("url is required")
	}
	out, err := inBrowser(url)
	if err != nil {
		return "", err
	}
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}
