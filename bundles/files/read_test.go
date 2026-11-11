package main

import (
	"strings"
	"testing"
)

// Reading past the end of a file is a fact about the file, not an error, so a
// window outside it is empty rather than a refusal.
func TestWindow(t *testing.T) {
	lines := []string{"a", "b", "c", "d", "e"}
	for _, c := range []struct {
		name          string
		offset, limit int
		want          string
	}{
		{"no window is the whole file", 0, 0, "a b c d e"},
		{"an offset skips the start", 2, 0, "c d e"},
		{"a limit stops early", 0, 2, "a b"},
		{"both together", 1, 2, "b c"},
		{"an offset past the end is empty", 99, 0, ""},
		{"a limit past the end is the rest", 3, 99, "d e"},
		{"negative values are ignored", -1, -1, "a b c d e"},
	} {
		t.Run(c.name, func(t *testing.T) {
			if got := strings.Join(Window(lines, c.offset, c.limit), " "); got != c.want {
				t.Errorf("Window(%d, %d) = %q, want %q", c.offset, c.limit, got, c.want)
			}
		})
	}
}
