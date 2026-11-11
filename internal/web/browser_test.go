package web

import "testing"

// Headless chromium introduces itself as HeadlessChrome, and that one word is
// what a site that turns away automation reads first. Everything else in the
// string is true of the browser that is running, so the word is all that goes.
// The version is the installed one: a Chrome string that disagrees with the
// browser behind it is a second tell rather than a fix for the first.
func TestTheUserAgentIsTheInstalledChromeWithoutHeadless(t *testing.T) {
	const want = "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 " +
		"(KHTML, like Gecko) Chrome/140.0.0.0 Safari/537.36"
	for _, c := range []struct{ name, banner string }{
		{"debian's banner", "Chromium 140.0.7339.185 built on Debian GNU/Linux 12 (bookworm)\n"},
		{"a bare version", "Chromium 140.0.7339.185"},
		{"google's own build", "Google Chrome 140.0.7339.80 "},
	} {
		t.Run(c.name, func(t *testing.T) {
			if got := userAgentFor(c.banner); got != want {
				t.Errorf("userAgentFor(%q) = %q, want %q", c.banner, got, want)
			}
		})
	}
}

// A banner that names no version gives no string at all, and the browser keeps
// its own. Inventing a version would be guessing at the one thing the string is
// supposed to agree with.
func TestABannerWithNoVersionGivesNoUserAgent(t *testing.T) {
	for _, banner := range []string{"", "chromium: command not found", "Chromium built on Debian"} {
		if got := userAgentFor(banner); got != "" {
			t.Errorf("userAgentFor(%q) = %q, want nothing", banner, got)
		}
	}
}
