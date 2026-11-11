package main

import (
	"strings"
	"testing"
)

// Replacing the first of several matches is the edit that silently does the
// wrong thing, so an ambiguous old string is refused until the caller says
// which they meant.
func TestReplace(t *testing.T) {
	t.Run("one match is replaced", func(t *testing.T) {
		got, n, err := Replace("hello world", "world", "there", false, "f.txt")
		if err != nil || got != "hello there" || n != 1 {
			t.Errorf("got %q, %d, %v", got, n, err)
		}
	})
	t.Run("several matches are refused", func(t *testing.T) {
		_, _, err := Replace("a a a", "a", "b", false, "f.txt")
		if err == nil {
			t.Fatal("an ambiguous edit was applied")
		}
		if !strings.Contains(err.Error(), "3 times") || !strings.Contains(err.Error(), "replace_all") {
			t.Errorf("the refusal does not say how to proceed: %v", err)
		}
	})
	t.Run("replace_all takes every match", func(t *testing.T) {
		got, n, err := Replace("a a a", "a", "b", true, "f.txt")
		if err != nil || got != "b b b" || n != 3 {
			t.Errorf("got %q, %d, %v", got, n, err)
		}
	})
	t.Run("no match names the file", func(t *testing.T) {
		_, _, err := Replace("hello", "absent", "x", false, "notes.txt")
		if err == nil || !strings.Contains(err.Error(), "notes.txt") {
			t.Errorf("err = %v, want it to name the file", err)
		}
	})
	t.Run("a near miss names the line it nearly matched", func(t *testing.T) {
		body := "date,meal,item\n2026-10-11,breakfast,gouda,on crispbread\n2026-10-11,lunch,rice,plain\n"
		_, _, err := Replace(body, "2026-10-11,breakfast,gouda,on rye crispbread", "x", false, "log/food.csv")
		if err == nil {
			t.Fatal("an edit that matches nothing was applied")
		}
		if !strings.Contains(err.Error(), "line 2") ||
			!strings.Contains(err.Error(), "2026-10-11,breakfast,gouda,on crispbread") {
			t.Errorf("err = %v, want it to quote line 2, the one it nearly matched", err)
		}
	})
	t.Run("a difference only in spacing says so", func(t *testing.T) {
		body := "## Plan\n\nWeek one:  oats\nand rice.\n"
		_, _, err := Replace(body, "Week one: oats and rice.", "x", false, "plan.md")
		if err == nil || !strings.Contains(err.Error(), "spaces or line breaks") {
			t.Errorf("err = %v, want it to say the text is there with different spacing", err)
		}
	})
	t.Run("a miss like nothing in the file quotes nothing", func(t *testing.T) {
		_, _, err := Replace("alpha\nbeta\n", "a sentence that is not here at all", "x", false, "f.txt")
		if err == nil || strings.Contains(err.Error(), "closest") {
			t.Errorf("err = %v, want no closest line when none resembles it", err)
		}
	})
	t.Run("an empty old string is refused", func(t *testing.T) {
		// strings.Count("abc", "") is 4, so without this an empty old string
		// would report a nonsense count and rewrite the file.
		if _, _, err := Replace("abc", "", "x", false, "f.txt"); err == nil {
			t.Error("an empty old string was accepted")
		}
	})
	t.Run("a deletion is an edit", func(t *testing.T) {
		got, _, err := Replace("keep drop", " drop", "", false, "f.txt")
		if err != nil || got != "keep" {
			t.Errorf("got %q, %v", got, err)
		}
	})
}
