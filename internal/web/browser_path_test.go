package web

import (
	"strings"
	"testing"
)

// There is one browser, it is installed in the image, and it is at a path the
// sandbox already grants. Discovery was six candidate paths, a Playwright cache
// scan and an override variable, and every one of them existed because the
// browser might be somewhere else. It is not: CI and the image install the same
// package, and a laptop that wants the browser tools runs the container.
func TestTheBrowserIsOnePathAndNotASearch(t *testing.T) {
	if BrowserPath != "/usr/bin/chromium" {
		t.Errorf("BrowserPath = %q, want the path the image installs", BrowserPath)
	}
}

// A missing browser is a broken image, not a condition to handle: the tool says
// so and stops, rather than quietly returning something a caller cannot tell
// from a rendered page.
func TestAMissingBrowserIsNamedRatherThanWorkedAround(t *testing.T) {
	_, err := lookupBrowser(func(string) bool { return false })
	if err == nil {
		t.Fatal("a missing browser was accepted")
	}
	if !strings.Contains(err.Error(), BrowserPath) {
		t.Errorf("error %q does not name the path that is missing", err)
	}
}
