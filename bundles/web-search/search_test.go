package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

const reply = `{"query":"landlock go","results":[
  {"title":"Landlock for Go","url":"https://pkg.go.dev/landlock","content":"Bindings for the kernel's LSM.","score":0.97},
  {"title":"The kernel's docs","url":"https://docs.kernel.org/landlock.html","content":"A process restricts itself.","score":0.9}
]}`

func TestParseAnswer(t *testing.T) {
	got, err := ParseAnswer([]byte(reply))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d results, want 2: %+v", len(got), got)
	}
	if got[0].Title != "Landlock for Go" || got[0].URL != "https://pkg.go.dev/landlock" {
		t.Errorf("first result = %+v", got[0])
	}
	if got[0].Snippet != "Bindings for the kernel's LSM." {
		t.Errorf("snippet = %q", got[0].Snippet)
	}
}

// A hit the model cannot follow is a line of text pretending to be a source.
func TestAResultWithoutAURLIsDropped(t *testing.T) {
	got, err := ParseAnswer([]byte(`{"results":[{"title":"Nowhere","url":"","content":"x"},
		{"title":"Somewhere","url":"https://x.test/","content":"y"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].URL != "https://x.test/" {
		t.Errorf("got %+v, want only the hit with a URL", got)
	}
}

func TestNoResultsIsEmptyNotAnError(t *testing.T) {
	got, err := ParseAnswer([]byte(`{"query":"x","results":[]}`))
	if err != nil || len(got) != 0 {
		t.Errorf("got %+v, %v; want nothing and no error", got, err)
	}
}

func TestAnAnswerOfTheWrongShapeIsAnError(t *testing.T) {
	if _, err := ParseAnswer([]byte(`<html>not json</html>`)); err == nil {
		t.Error("HTML was read as an answer")
	}
}

// A status code alone does not tell a rejected key from a spent allowance, and
// those ask different things of the operator.
func TestReasonCarriesWhatTheAPISaid(t *testing.T) {
	for _, c := range []struct{ name, raw, want string }{
		{"nested object", `{"detail":{"error":"Unauthorized: invalid API key"}}`, "Unauthorized: invalid API key"},
		{"bare string", `{"detail":"Your account has run out of credits"}`, "run out of credits"},
		{"neither", `something went wrong`, "something went wrong"},
	} {
		t.Run(c.name, func(t *testing.T) {
			if got := Reason([]byte(c.raw)); !strings.Contains(got, c.want) {
				t.Errorf("Reason(%s) = %q, want it to carry %q", c.raw, got, c.want)
			}
		})
	}
}

func TestEndpointPrefersTheEnvironment(t *testing.T) {
	t.Setenv(URLName, "")
	if got := Endpoint(); got != tavilyURL {
		t.Errorf("Endpoint() = %q, want the constant %q", got, tavilyURL)
	}
	t.Setenv(URLName, "http://127.0.0.1:1/search")
	if got := Endpoint(); got != "http://127.0.0.1:1/search" {
		t.Errorf("Endpoint() = %q, want what the environment named", got)
	}
}

// The key rides in a header, and the limit is what the API is asked for rather
// than something trimmed off the answer.
func TestSearchSendsTheKeyAndTheLimit(t *testing.T) {
	var auth, body string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth = r.Header.Get("Authorization")
		raw, _ := io.ReadAll(r.Body)
		body = string(raw)
		_, _ = w.Write([]byte(reply))
	}))
	defer srv.Close()
	os.Setenv(URLName, srv.URL)
	defer os.Unsetenv(URLName)

	got, err := Search("tvly-key", "landlock go", 4)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Errorf("got %d results, want 2", len(got))
	}
	if auth != "Bearer tvly-key" {
		t.Errorf("Authorization = %q", auth)
	}
	var sent request
	if err := json.Unmarshal([]byte(body), &sent); err != nil {
		t.Fatalf("the request was not JSON: %s", body)
	}
	if sent.Query != "landlock go" || sent.MaxResults != 4 {
		t.Errorf("sent %+v, want the query and the limit", sent)
	}
}

func TestSearchReportsAnUnsuccessfulReply(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(432)
		_, _ = w.Write([]byte(`{"detail":{"error":"Your account has run out of credits"}}`))
	}))
	defer srv.Close()
	os.Setenv(URLName, srv.URL)
	defer os.Unsetenv(URLName)

	_, err := Search("tvly-key", "anything", 5)
	if err == nil {
		t.Fatal("a refusal was read as an answer")
	}
	for _, want := range []string{"432", "run out of credits"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not carry %q", err, want)
		}
	}
}

func TestFormatIsOneHitPerNumberedLine(t *testing.T) {
	got := Format([]Result{
		{Title: "First", URL: "https://a.test/", Snippet: "about the first"},
		{Title: "", URL: "https://b.test/"},
	})
	if !strings.Contains(got, "1. First\n   https://a.test/\n   about the first") {
		t.Errorf("a hit is not laid out as expected:\n%s", got)
	}
	// A hit with no title still has to be followable.
	if !strings.Contains(got, "2. https://b.test/") {
		t.Errorf("a result without a title lost its URL:\n%s", got)
	}
}
