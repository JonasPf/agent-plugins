package main

import (
	"encoding/json"
	"io"
	"net/http"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/JonasPf/agent-plugins/internal/serve"
	"github.com/JonasPf/agent-plugins/internal/servetest"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// appsCaps is what a host that shows pages says when it connects.
func appsCaps() *mcp.ClientCapabilities {
	c := &mcp.ClientCapabilities{}
	c.AddExtension("io.modelcontextprotocol/ui", map[string]any{"mimeTypes": []string{serve.AppMIME}})
	return c
}

var reviewLink = regexp.MustCompile(`http://127\.0\.0\.1:\d+/\?t=[0-9a-f]{32}`)

// linkClient is a host that shows no pages, such as a coding agent in a
// terminal, and the review page it is given a link to.
func linkClient(t *testing.T) *mcp.ClientSession {
	t.Cleanup(closeReviewLink)
	return servetest.Client(t, manifest, tools, runs, pages...)
}

func openLink(t *testing.T, cs *mcp.ClientSession) string {
	t.Helper()
	res := servetest.Call(t, cs, "voiceover_review", `{"video":"clip.mov"}`)
	link := reviewLink.FindString(res.Text)
	if res.Error || link == "" {
		t.Fatalf("voiceover_review = %+v, want a link to the page on this machine", res)
	}
	return link
}

func fetch(t *testing.T, method, url, host, body string) (int, string) {
	t.Helper()
	req, _ := http.NewRequest(method, url, strings.NewReader(body))
	if host != "" {
		req.Host = host
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return 0, err.Error()
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b)
}

// "A host that shows no pages is given a link to the page, served on this
// machine by the bundle's own server, with a token every request must carry,
// under a policy that lets it reach nothing but that server."
func TestReviewWithoutAppsGivesALinkToThePageOnThisMachine(t *testing.T) {
	_, _ = scripted(t, [3]float64{1, 440, 1})
	cs := linkClient(t)

	res := servetest.Call(t, cs, "voiceover_review", `{"video":"clip.mov"}`)
	link := reviewLink.FindString(res.Text)
	if res.Error || link == "" || !strings.Contains(res.Text, "End your turn") || !strings.Contains(res.Text, "paste") {
		t.Fatalf("voiceover_review = %+v", res)
	}
	req, _ := http.NewRequest("GET", link, nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	page, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	csp := resp.Header.Get("Content-Security-Policy")
	if resp.StatusCode != 200 || !strings.Contains(string(page), "voiceover_save_review") || !strings.Contains(string(page), `"video":"clip.mov"`) {
		t.Errorf("the link gave %d, %d bytes", resp.StatusCode, len(page))
	}
	if !strings.Contains(csp, "connect-src 'self'") || !strings.Contains(csp, "default-src 'none'") || !strings.Contains(csp, "frame-ancestors 'none'") {
		t.Errorf("Content-Security-Policy = %q, want the page to reach its own server and nothing else", csp)
	}
	base := strings.Split(link, "?")[0]
	for what, code := range map[string]int{
		"no token":          first(fetch(t, "GET", base, "", "")),
		"a wrong token":     first(fetch(t, "GET", base+"?t=00000000000000000000000000000000", "", "")),
		"another host name": first(fetch(t, "GET", link, "rebound.example:80", "")),
	} {
		if code != 403 {
			t.Errorf("%s: %d, want 403", what, code)
		}
	}
}

func first(code int, _ string) int { return code }

// "Through the link the page calls its own two tools and nothing else."
func TestTheLinkCallsOnlyThePagesTools(t *testing.T) {
	_, g := scripted(t, [3]float64{1, 440, 1})
	cs := linkClient(t)
	link := openLink(t, cs)
	call := strings.Replace(link, "/?t=", "/call?t=", 1)

	code, body := fetch(t, "POST", call, "", `{"name":"voiceover_review_data","arguments":{"video":"clip.mov"}}`)
	var res struct {
		IsError bool `json:"isError"`
		Content []struct{ Text string }
	}
	if err := json.Unmarshal([]byte(body), &res); code != 200 || err != nil || res.IsError || !strings.Contains(res.Content[0].Text, `"duration"`) {
		t.Errorf("the page's data through the link: %d %.200s", code, body)
	}
	for _, name := range []string{"voiceover_render", "voiceover_transcribe", "voiceover_review", "nothing"} {
		if code, _ := fetch(t, "POST", call, "", `{"name":"`+name+`","arguments":{"video":"clip.mov"}}`); code != 403 {
			t.Errorf("%s through the link: %d, want 403", name, code)
		}
	}
	if code, _ := fetch(t, "GET", call, "", ""); code != 405 {
		t.Errorf("a GET of /call: %d, want 405", code)
	}
	if n := len(g.asked("/audio/speech")); n != 0 {
		t.Errorf("%d lines were spoken through the link", n)
	}
}

// "Send's message is given back to the page to show, and the link stops."
func TestSendEndsTheLink(t *testing.T) {
	_, _ = scripted(t, [3]float64{1, 440, 1})
	cs := linkClient(t)
	link := openLink(t, cs)
	sent := strings.Replace(link, "/?t=", "/sent?t=", 1)

	if code, body := fetch(t, "POST", sent, "", `{"text":"Sent from the review page of clip.mov: 1 line."}`); code != 200 {
		t.Fatalf("/sent: %d %s", code, body)
	}
	if !stops(t, link) {
		t.Error("the link still answers after Send")
	}
}

// stops reports whether a link stops answering within a second.
func stops(t *testing.T, link string) bool {
	t.Helper()
	for i := 0; i < 20; i++ {
		if code, _ := fetch(t, "GET", link, "", ""); code == 0 {
			return true
		}
		time.Sleep(50 * time.Millisecond)
	}
	return false
}

// "One page at a time: a new review replaces the old link, and a link left
// alone stops after a while."
func TestALinkIsReplacedAndStopsWhenLeftAlone(t *testing.T) {
	_, _ = scripted(t, [3]float64{1, 440, 1})
	cs := linkClient(t)
	old := openLink(t, cs)
	fresh := openLink(t, cs)
	if old == fresh || !stops(t, old) {
		t.Errorf("the first link still answers after a second review (%s, %s)", old, fresh)
	}
	if code, _ := fetch(t, "GET", fresh, "", ""); code != 200 {
		t.Errorf("the new link: %d", code)
	}
	before := reviewIdle
	reviewIdle = 200 * time.Millisecond
	t.Cleanup(func() { reviewIdle = before })
	idle := openLink(t, cs)
	time.Sleep(400 * time.Millisecond)
	if !stops(t, idle) {
		t.Error("a link left alone did not stop")
	}
}

// "Which way the page is shown is the host's to say, unless the bundle's
// settings say otherwise."
func TestTheReviewSettingChoosesTheWay(t *testing.T) {
	_, _ = scripted(t, [3]float64{1, 440, 1})
	t.Cleanup(closeReviewLink)
	apps := servetest.ClientWith(t, appsCaps(), manifest, tools, runs, pages...)
	plain := servetest.Client(t, manifest, tools, runs, pages...)
	for _, c := range []struct {
		setting string
		cs      *mcp.ClientSession
		link    bool
	}{
		{"", apps, false}, {"auto", apps, false}, {"", plain, true},
		{"browser", apps, true}, {"apps", plain, false}, {"${user_config.review}", plain, true},
	} {
		t.Setenv(reviewName, c.setting)
		res := servetest.Call(t, c.cs, "voiceover_review", `{"video":"clip.mov"}`)
		if res.Error || (reviewLink.MatchString(res.Text) != c.link) {
			t.Errorf("review %q: %+v, want a link: %v", c.setting, res, c.link)
		}
		closeReviewLink()
	}
	t.Setenv(reviewName, "popup")
	if res := servetest.Call(t, plain, "voiceover_review", `{"video":"clip.mov"}`); !res.Error || !strings.Contains(res.Text, "auto, apps or browser") {
		t.Errorf("an unknown setting: %+v", res)
	}
}
