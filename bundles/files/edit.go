package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/JonasPf/agent-plugins/internal/serve"
	"github.com/JonasPf/agent-plugins/internal/workspace"
)

type editArgs struct {
	Path       string `json:"path"`
	Old        string `json:"old"`
	New        string `json:"new"`
	ReplaceAll bool   `json:"replace_all"`
}

// Replace applies the edit, or says why it will not. An old string that matches
// more than once is refused unless the caller asked for every match: replacing
// the first of several is the edit that silently does the wrong thing.
func Replace(body, old, new string, all bool, name string) (string, int, error) {
	if old == "" {
		return "", 0, fmt.Errorf("old is required")
	}
	n := strings.Count(body, old)
	switch {
	case n == 0:
		return "", 0, notFound(body, old, name)
	case n > 1 && !all:
		return "", 0, fmt.Errorf("old string appears %d times in %s; pass replace_all or give more context", n, name)
	case all:
		return strings.ReplaceAll(body, old, new), n, nil
	default:
		return strings.Replace(body, old, new, 1), n, nil
	}
}

// notFound says why an old string matched nothing, and where it nearly did.
// The model writes old from what it remembers of the file, and the misses seen
// were near ones — a word or a space off — each followed by a read of the whole
// file to find out which. Quoting the line it meant makes the retry one call.
func notFound(body, old, name string) error {
	if s := squash(old); s != "" && strings.Contains(squash(body), s) {
		return fmt.Errorf("old string not found in %s: the text is there, but its spaces or line "+
			"breaks differ. Copy it exactly as the file has it", name)
	}
	if n, line, ok := closestLine(body, old); ok {
		return fmt.Errorf("old string not found in %s. The closest is line %d:\n%s", name, n, line)
	}
	return fmt.Errorf("old string not found in %s. Read the file and copy the text exactly", name)
}

func squash(s string) string { return strings.Join(strings.Fields(s), " ") }

// closestLine finds the line of body most like the first line of old, by the
// share of two-letter pairs they have in common. A line sharing fewer than
// half is no hint at all, and quoting it would send the retry the wrong way.
func closestLine(body, old string) (int, string, bool) {
	want := ""
	for _, l := range strings.Split(old, "\n") {
		if want = strings.TrimSpace(l); want != "" {
			break
		}
	}
	if len(want) < 4 {
		return 0, "", false
	}
	pairs := bigrams(want)
	best, bestN, bestLine := 0.5, 0, ""
	for i, l := range strings.Split(body, "\n") {
		t := strings.TrimSpace(l)
		if t == "" {
			continue
		}
		if score := dice(pairs, bigrams(t)); score > best {
			best, bestN, bestLine = score, i+1, l
		}
	}
	if bestN == 0 {
		return 0, "", false
	}
	if len(bestLine) > 300 {
		bestLine = bestLine[:300] + "…"
	}
	return bestN, bestLine, true
}

func bigrams(s string) map[string]int {
	m := map[string]int{}
	for i := 0; i+2 <= len(s); i++ {
		m[s[i:i+2]]++
	}
	return m
}

// dice is the share of pairs two strings have in common, from 0 to 1.
func dice(a, b map[string]int) float64 {
	total, common := 0, 0
	for k, n := range a {
		total += n
		common += min(n, b[k])
	}
	for _, n := range b {
		total += n
	}
	if total == 0 {
		return 0
	}
	return 2 * float64(common) / float64(total)
}

// edit replaces a string in a file in the working directory.
func edit(_ context.Context, raw json.RawMessage) (string, error) {
	var a editArgs
	if err := serve.Args(raw, &a); err != nil {
		return "", err
	}
	path, err := workspace.Inside(a.Path)
	if err != nil {
		return "", err
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	out, n, err := Replace(string(b), a.Old, a.New, a.ReplaceAll, a.Path)
	if err != nil {
		return "", err
	}
	if err := os.WriteFile(path, []byte(out), 0o644); err != nil {
		return "", err
	}
	return fmt.Sprintf("edited %s (%d replacement(s))", a.Path, n), nil
}
