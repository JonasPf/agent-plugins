package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Talking to the search API. Building the request and reading the answer are
// separate from making the call, so both can be tested without a network.

// tavilyURL is where the search goes. It is a constant with an override rather
// than configuration: the operator sets a key and nothing else, and the name
// exists so the suite can put a server of its own in the way — the one
// substitution R5 allows, alongside the model provider.
const tavilyURL = "https://api.tavily.com/search"

// KeyName and URLName are the variables the manifest fills from the bundle's
// settings: search is something an agent either has or has not, like the model
// it runs on.
const (
	KeyName = "TAVILY_API_KEY"
	URLName = "TAVILY_URL"
)

// Result is one hit, in the shape the tool prints.
type Result struct {
	Title   string
	URL     string
	Snippet string
}

// request is what the API is asked. The limit is passed through rather than
// applied to the answer: fetching ten to show three costs three times as much
// for the same three.
type request struct {
	Query      string `json:"query"`
	MaxResults int    `json:"max_results"`
}

// answer is the part of the API's reply this tool reads. Everything else it
// sends — scores, timings, follow-up questions — is ignored rather than
// forwarded: a field nobody asked for is a field the model has to discount.
type answer struct {
	Results []struct {
		Title   string `json:"title"`
		URL     string `json:"url"`
		Content string `json:"content"`
	} `json:"results"`
}

// apiError is the shape the API reports a refusal in. It is nested and it is
// sometimes a bare string, so both are read; what matters is that the reason
// reaches the model rather than a status code on its own.
type apiError struct {
	Detail json.RawMessage `json:"detail"`
}

// Endpoint is where a search goes: the constant, unless the environment names
// somewhere else.
func Endpoint() string {
	if u := setting(URLName); u != "" {
		return u
	}
	return tavilyURL
}

// Search puts the query to the API and returns what it answered.
func Search(key, query string, limit int) ([]Result, error) {
	body, err := json.Marshal(request{Query: query, MaxResults: limit})
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequest("POST", Endpoint(), bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+key)
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("the search API could not be reached: %w", err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, fmt.Errorf("the search API's answer could not be read: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("the search API answered %d: %s", resp.StatusCode, Reason(raw))
	}
	return ParseAnswer(raw)
}

// Reason is what an unsuccessful reply said, in as few words as carry the
// meaning. A status code alone does not distinguish a rejected key from a
// spent allowance, and those call for different things from the operator.
func Reason(raw []byte) string {
	var e apiError
	if err := json.Unmarshal(raw, &e); err == nil && len(e.Detail) > 0 {
		var s string
		if json.Unmarshal(e.Detail, &s) == nil && s != "" {
			return s
		}
		var obj struct {
			Error string `json:"error"`
		}
		if json.Unmarshal(e.Detail, &obj) == nil && obj.Error != "" {
			return obj.Error
		}
	}
	return Clip(strings.TrimSpace(string(raw)), 300)
}

// ParseAnswer reads the results out of a successful reply. A result without a
// URL is dropped: the model's next move is to fetch one, and a hit it cannot
// follow is a line of text pretending to be a source.
func ParseAnswer(raw []byte) ([]Result, error) {
	var a answer
	if err := json.Unmarshal(raw, &a); err != nil {
		return nil, fmt.Errorf("the search API's answer was not the shape this tool reads: %w", err)
	}
	var out []Result
	for _, r := range a.Results {
		if strings.TrimSpace(r.URL) == "" {
			continue
		}
		out = append(out, Result{
			Title:   strings.TrimSpace(r.Title),
			URL:     strings.TrimSpace(r.URL),
			Snippet: strings.TrimSpace(r.Content),
		})
	}
	return out, nil
}

// Clip caps a body without pretending it was whole.
func Clip(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

// Format is the answer as the model reads it: numbered, each hit a title, a URL
// it can pass straight to web_fetch, and whatever the API said about it.
func Format(results []Result) string {
	var lines []string
	for i, r := range results {
		title := r.Title
		if title == "" {
			title = r.URL
		}
		lines = append(lines, fmt.Sprintf("%d. %s\n   %s", i+1, title, r.URL))
		if r.Snippet != "" {
			lines = append(lines, "   "+Clip(r.Snippet, 500))
		}
	}
	return strings.Join(lines, "\n")
}
