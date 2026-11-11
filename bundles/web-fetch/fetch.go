package main

import (
	"regexp"
	"strings"
)

// Deciding whether the bytes a server sent are the page, or only the shell it
// ships while its scripts build the page. Pure, so it can be tested directly.

var (
	hasScript = regexp.MustCompile(`(?is)<script\b`)
	// The roots a client-side framework mounts into. An element that exists only
	// to be filled says plainly that the page is not finished.
	emptyRoot = regexp.MustCompile(`(?is)<(?:div|main|section)[^>]*\bid=["']?(?:root|app|__next|application)["']?[^>]*>\s*</(?:div|main|section)>`)
	// Text a page shows while it waits. On its own this means nothing; with
	// almost no other text around it, it is the whole visible page.
	waiting = regexp.MustCompile(`(?i)\b(loading|please wait|just a moment|one moment)\b`)
	// A binding the framework was going to replace. A page that rendered has
	// none left: whatever stood between the braces is the value by the time a
	// person reads it.
	binding = regexp.MustCompile(`\{\{\s*[\w$.\[\]]+[^{}]{0,200}\}\}`)
	// An expression inside an attribute closes the tag at its own first ">", so
	// the rest of it lands in the page as words — `1 && isAllowed()">`. One such
	// fragment is a page documenting HTML; several is a page that never ran.
	leaked = regexp.MustCompile(`["']\s*>`)
)

// leaks is how many stray attribute fragments it takes to mean the page did not
// render. Documentation shows markup on purpose, and showing one snippet of it
// is not a symptom.
const leaks = 3

// thin is the amount of readable text below which a document with scripts in it
// is more likely a shell than a short page. A genuinely brief page — a status
// line, a 404 — sits under this too, which is why a script tag is also required:
// a page with no scripts cannot be waiting on any.
const thin = 400

// NeedsScripts reports whether a fetched body looks like a page whose text has
// not been written yet — the case where these bytes answer a different question
// from the one that was asked, and web_browse should be used instead.
//
// It is deliberately conservative. A false positive sends the reader to a
// browser that costs a hundred times as much and returns the same thing; worse,
// a warning that fires on finished pages is a warning nobody reads. So it fires
// only when a page both runs scripts and has next to nothing to say without
// them.
//
// text is the body's text as web.ToText recovers it. The caller has already
// recovered it to answer with, and recovering it again cost as much as the
// first time — the largest piece of work in a fetch.
func NeedsScripts(body, text string) bool {
	if !hasScript.MatchString(body) {
		return false
	}
	if emptyRoot.MatchString(body) {
		return true
	}
	// Template syntax left in the text is proof rather than a threshold, so it
	// is asked first. The length test below measures the whole document —
	// navigation, cookie banner, footer — which a shell clears comfortably while
	// its content is still unwritten; that is how a shop's item page came back
	// carrying Angular's own source with nothing to say it had not rendered.
	if binding.MatchString(text) || len(leaked.FindAllString(text, leaks)) >= leaks {
		return true
	}
	if len(text) >= thin {
		return false
	}
	// Under the threshold, with scripts present: a page that is visibly waiting
	// is a shell, and a page that says almost nothing at all probably is too.
	return waiting.MatchString(text) || len(strings.TrimSpace(text)) == 0
}
