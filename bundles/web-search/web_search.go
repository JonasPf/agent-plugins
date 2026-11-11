// web_search puts a query to a search API and returns the hits. It used to
// drive a headless browser against a search engine's own results page; that
// engine now answers an automated client with a page of unrelated results
// rather than declining, and every other engine reachable from here refuses
// outright (ADR-059). An API answers the query it was asked.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/JonasPf/agent-plugins/internal/serve"
)

type args struct {
	Query string `json:"query"`
	Limit int    `json:"limit"`
}

// setting is a variable the host fills from the bundle's settings. A host
// that was given no value may leave the placeholder it would have replaced,
// and a placeholder is not a value.
func setting(name string) string {
	v := strings.TrimSpace(os.Getenv(name))
	if strings.HasPrefix(v, "${") {
		return ""
	}
	return v
}

func webSearch(_ context.Context, raw json.RawMessage) (string, error) {
	var a args
	if err := serve.Args(raw, &a); err != nil {
		return "", err
	}
	q := strings.TrimSpace(a.Query)
	if q == "" {
		return "", errors.New("query is required")
	}
	limit := a.Limit
	if limit <= 0 {
		limit = 10
	}

	// The key is the operator's and is set once, in the bundle's settings. A
	// model cannot set it, so the message is addressed to the person who can,
	// and says where it goes rather than only that it is missing.
	key := setting(KeyName)
	if key == "" {
		return "", fmt.Errorf("no search key: %s is not set, so this agent cannot search. "+
			"The operator sets the Tavily API key in this plugin's settings; until then, fetch a page "+
			"you can name with web_fetch or web_browse.", KeyName)
	}

	results, err := Search(key, q, limit)
	if err != nil {
		return "", err
	}
	if len(results) == 0 {
		return fmt.Sprintf("no results for %q", q), nil
	}
	return Format(results), nil
}
