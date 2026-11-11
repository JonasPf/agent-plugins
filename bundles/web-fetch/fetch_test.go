package main

import (
	"strings"
	"testing"

	"github.com/JonasPf/agent-plugins/internal/web"
)

// The whole reason a cheap fetch is safe to offer is that it can tell when it
// was the wrong tool. Its predecessor could not: it returned the shell a page
// ships as, and whoever asked could not tell that from a page that simply says
// little. So the one thing this must get right is noticing.
func TestAPageWhoseTextIsWrittenByScriptsIsRecognised(t *testing.T) {
	for _, c := range []struct {
		name string
		html string
	}{
		{"an empty root a framework fills", `<html><head><title>Shop</title>
<script src="/bundle.js"></script></head><body><div id="root"></div></body></html>`},
		{"a placeholder waiting to be replaced", `<html><body><div id="out">Loading…</div>
<script>fetch('/api').then(r => r.json()).then(d => out.textContent = d.text)</script></body></html>`},
	} {
		t.Run(c.name, func(t *testing.T) {
			if !NeedsScripts(c.html, web.ToText(c.html)) {
				t.Error("a page assembled by its scripts was not recognised as one")
			}
		})
	}
}

// The opposite matters just as much. A warning on a page that is genuinely
// finished teaches whoever reads it to ignore warnings, and sends them to a
// browser that costs a hundred times as much for nothing.
func TestAPageThatIsAlreadyReadableIsNotSecondGuessed(t *testing.T) {
	for _, c := range []struct {
		name string
		html string
	}{
		{"prose with a script beside it", `<html><head><script src="/analytics.js"></script></head><body>
<h1>Widgets</h1><p>` + strings.Repeat("The widget is a small device that does a job. ", 20) +
			`</p><p>` + strings.Repeat("It is sold in boxes of ten. ", 20) + `</p></body></html>`},
		{"plain prose, no scripts at all", `<html><body><h1>Notes</h1><p>` +
			strings.Repeat("A sentence that carries some actual meaning. ", 12) + `</p></body></html>`},
		{"a short page with nothing dynamic about it", `<html><body><p>Service is up.</p></body></html>`},
		// Documentation about HTML shows markup on purpose, and a page that
		// renders a single snippet of it has rendered perfectly well.
		{"documentation showing a snippet of markup", `<html><head><script src="/docs.js"></script></head><body>
<h1>Links</h1><p>` + strings.Repeat("Write the href attribute first, then the text it wraps. ", 20) +
			`</p><pre><code>&lt;a href="/help"&gt;Help&lt;/a&gt;</code></pre></body></html>`},
	} {
		t.Run(c.name, func(t *testing.T) {
			if NeedsScripts(c.html, web.ToText(c.html)) {
				t.Error("a page that was already readable was reported as needing a browser")
			}
		})
	}
}

// The character threshold measures the whole document, chrome included, so a
// page with a fat navigation bar clears it while its content is still unwritten.
// That is how a shop's item page came back carrying Angular's own template
// source with nothing to say it had not rendered. Template syntax surviving into
// the text is not a threshold but a proof: a rendered page has already replaced
// its own bindings.
func TestTemplateSyntaxSurvivingIntoTheTextIsRecognised(t *testing.T) {
	nav := "<nav>" + strings.Repeat("Residential Business About Us Contact Us Support Blog and News ", 12) + "</nav>"
	for _, c := range []struct {
		name string
		html string
	}{
		{"a binding the framework never filled in", `<html><head><script src="/main.js"></script></head><body>` +
			nav + `<div class="price">{{ item.price | currency }}</div></body></html>`},
		// An expression inside an attribute closes the tag at its own first ">",
		// so the rest of it lands in the page as words. One such fragment is a
		// code sample; a page full of them never rendered.
		{"attribute expressions leaking into the page", `<html><head><script src="/main.js"></script></head><body>` +
			nav + `<div *ngIf="cart.length > 1 && isSwitchingAccountAllowed()">Switch Account</div>
<div *ngIf="cart.length > 0 && hasOffers()">Offers</div>
<span data-show="items.length > 2 && ready()">More</span></body></html>`},
	} {
		t.Run(c.name, func(t *testing.T) {
			if !NeedsScripts(c.html, web.ToText(c.html)) {
				t.Error("a page still showing its own template syntax was not recognised as unrendered")
			}
		})
	}
}

// A body that is not a document is the case this tool is best at, and the one
// where a browser is pure cost. It is never second-guessed.
func TestANonDocumentIsNeverSentToABrowser(t *testing.T) {
	if NeedsScripts(`{"stock": 4, "sku": "W-1"}`, `{"stock": 4, "sku": "W-1"}`) {
		t.Error("a JSON body was reported as needing a browser")
	}
}
