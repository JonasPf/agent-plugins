package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/JonasPf/agent-plugins/internal/servetest"
)

func TestFetchingAsAHostCallsIt(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/page":
			w.Header().Set("Content-Type", "text/html")
			w.Write([]byte("<html><body><h1>Valves</h1><p>The valve is on the left.</p></body></html>"))
		case "/api":
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`{"valve": "left"}`))
		case "/shell":
			w.Header().Set("Content-Type", "text/html")
			w.Write([]byte(`<html><body><div id="root"></div><script src="app.js"></script></body></html>`))
		default:
			w.Header().Set("Content-Type", "text/html")
			w.WriteHeader(404)
			w.Write([]byte("<p>No such page here.</p>"))
		}
	}))
	defer srv.Close()
	cs := servetest.Client(t, manifest, tools, runs)
	fetch := func(url string) servetest.Result {
		args, _ := json.Marshal(map[string]string{"url": url})
		return servetest.Call(t, cs, "web_fetch", string(args))
	}

	if res := fetch(srv.URL + "/page"); res.Error || res.Text != "Valves\nThe valve is on the left." {
		t.Errorf("a page = %+v", res)
	}
	if res := fetch(srv.URL + "/api"); res.Error || res.Text != `{"valve": "left"}` {
		t.Errorf("a JSON body = %+v, want it verbatim", res)
	}
	if res := fetch(srv.URL + "/shell"); res.Error || !strings.Contains(res.Text, "Call web_browse on the same URL") {
		t.Errorf("a page its scripts write = %+v, want it to name web_browse", res)
	}
	if res := fetch(srv.URL + "/gone"); !res.Error || !strings.Contains(res.Text, "404") ||
		!strings.Contains(res.Text, "No such page here.") || !strings.Contains(res.Text, "web_browse") {
		t.Errorf("a missing page = %+v, want the status, the body, and what to try next", res)
	}
	if res := fetch(" "); !res.Error || res.Text != "url is required" {
		t.Errorf("no url = %+v", res)
	}
}
