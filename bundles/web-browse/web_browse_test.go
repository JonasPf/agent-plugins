package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/JonasPf/agent-plugins/internal/servetest"
	"github.com/JonasPf/agent-plugins/internal/web"
)

// The point of a browser: text a page's scripts write is text the caller gets.
// It needs the browser the bundle is for. Where PLUGINS_TEST_FULL is set, as it
// is in CI, a missing browser fails the test rather than skipping it.
func TestAPageIsReadAfterItsScriptsHaveRun(t *testing.T) {
	if !web.Exists(web.BrowserPath) {
		if os.Getenv("PLUGINS_TEST_FULL") != "" {
			t.Fatalf("no browser at %s, and this run claims to test everything", web.BrowserPath)
		}
		t.Skipf("no browser at %s", web.BrowserPath)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api" {
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`{"valve": "left"}`))
			return
		}
		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte(`<html><body><div id="root"></div>
<script>document.getElementById('root').textContent = 'written by ' + 'a script'</script></body></html>`))
	}))
	defer srv.Close()
	t.Setenv("TMPDIR", t.TempDir())
	cs := servetest.Client(t, manifest, tools, runs)
	browse := func(url string) servetest.Result {
		args, _ := json.Marshal(map[string]string{"url": url})
		return servetest.Call(t, cs, "web_browse", string(args))
	}
	if res := browse(srv.URL + "/"); res.Error || !strings.Contains(res.Text, "written by a script") {
		t.Errorf("a page its scripts write = %+v", res)
	}
	if res := browse(srv.URL + "/api"); res.Error || strings.TrimSpace(res.Text) != `{"valve": "left"}` {
		t.Errorf("a JSON body = %+v, want it verbatim", res)
	}
}

func TestBrowsingNeedsAURL(t *testing.T) {
	cs := servetest.Client(t, manifest, tools, runs)
	if res := servetest.Call(t, cs, "web_browse", `{"url":""}`); !res.Error || res.Text != "url is required" {
		t.Errorf("no url = %+v", res)
	}
}
