package web

import (
	"html"
	"regexp"
	"strings"
)

// Turning HTML into text a model can read. Pure functions, no network and no
// subprocess, so they can be tested directly.
//
// Both web tools need this and must agree: web_fetch reads the bytes a server
// sent, web_browse reads the DOM after the scripts have run, and the same page
// reached either way should read the same. Two copies of this would drift.

// A body the browser could not render as a document — JSON, plain text, a
// stylesheet — comes back wrapped in a minimal shell: a head of nothing but
// metadata, and the bytes themselves inside one <pre>. Unwrapped verbatim,
// because the point of fetching an API is the exact bytes it returned.
var plain = regexp.MustCompile(`(?is)\A\s*(?:<!doctype[^>]*>\s*)?<html[^>]*>\s*` +
	`<head>(?:\s*<meta[^>]*>)*\s*</head>\s*` +
	`<body[^>]*>\s*<pre[^>]*>(.*)</pre>\s*</body>\s*</html>\s*\z`)

var (
	drop     = regexp.MustCompile(`(?is)<(script|style|noscript|template)[^>]*>.*?</(?:script|style|noscript|template)\s*>|<!--.*?-->`)
	tags     = regexp.MustCompile(`(?s)<[^>]*>`)
	inlineWS = regexp.MustCompile(`[^\S\n]+`)
	newlines = regexp.MustCompile(`\n+`)
)

// block are the tags that end a line on screen. A rendered DOM arrives with its
// source whitespace already squeezed out — often as a single enormous line — so
// the only remaining evidence of where one thing ends and the next begins is
// these.
var block = regexp.MustCompile(`(?i)</?(?:p|div|br|hr|li|ul|ol|dl|dt|dd|tr|td|th|table|thead|tbody` +
	`|h[1-6]|section|article|header|footer|nav|aside|main|form|fieldset` +
	`|blockquote|pre|figure|figcaption|address|details|summary)\b[^>]*>`)

// UnwrapPlain returns the body of a non-document response, and false if this is
// a document.
func UnwrapPlain(dom string) (string, bool) {
	m := plain.FindStringSubmatch(dom)
	if m == nil {
		return "", false
	}
	return html.UnescapeString(m[1]), true
}

// ToText recovers readable text from a rendered DOM.
func ToText(dom string) string {
	s := drop.ReplaceAllString(dom, " ")
	s = block.ReplaceAllString(s, "\n")
	s = tags.ReplaceAllString(s, " ")
	s = html.UnescapeString(s)
	// Go's \s is ASCII only, so a non-breaking space survives the collapse below
	// and reaches the model as U+00A0 — invisible in the output and different
	// from the space a reader sees on the page.
	s = strings.ReplaceAll(s, "\u00a0", " ")
	lines := strings.Split(s, "\n")
	for i, line := range lines {
		lines[i] = strings.TrimSpace(inlineWS.ReplaceAllString(line, " "))
	}
	return strings.TrimSpace(newlines.ReplaceAllString(strings.Join(lines, "\n"), "\n"))
}
