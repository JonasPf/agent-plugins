package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/JonasPf/agent-plugins/internal/servetest"
)

func TestSearchingAsAHostCallsIt(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer tvly-test" {
			w.WriteHeader(401)
			return
		}
		w.Write([]byte(reply))
	}))
	defer srv.Close()
	t.Setenv(URLName, srv.URL)
	t.Setenv(KeyName, "tvly-test")
	cs := servetest.Client(t, manifest, tools, runs)

	res := servetest.Call(t, cs, "web_search", `{"query":"landlock go","limit":2}`)
	if res.Error || !strings.HasPrefix(res.Text, "1. Landlock for Go\n   https://pkg.go.dev/landlock") {
		t.Errorf("search = %+v", res)
	}
	if res := servetest.Call(t, cs, "web_search", `{"query":" "}`); !res.Error || res.Text != "query is required" {
		t.Errorf("no query = %+v", res)
	}
}

// The key is a setting of the bundle. With none given, a host either leaves
// the variable out or leaves the placeholder it would have filled; both are a
// search with no key, which says so to the person who can fix it.
func TestWithoutAKeyASearchSaysWhoCanFixIt(t *testing.T) {
	cs := servetest.Client(t, manifest, tools, runs)
	for _, key := range []string{"", "${user_config.tavily_api_key}"} {
		t.Setenv(KeyName, key)
		res := servetest.Call(t, cs, "web_search", `{"query":"anything"}`)
		if !res.Error || !strings.Contains(res.Text, "no search key") || !strings.Contains(res.Text, "plugin's settings") {
			t.Errorf("with the key %q, search = %+v", key, res)
		}
	}
}
