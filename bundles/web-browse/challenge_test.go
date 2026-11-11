package main

import (
	"strings"
	"testing"
)

// What www.sonasbathrooms.com rendered as, trimmed to what matters: the page a
// browser settles on while Cloudflare decides whether it is a person.
const cloudflareChallenge = `<!DOCTYPE html><html lang="en-US"><head><title>Just a moment...</title></head><body>
<div class="main-wrapper" role="main"><div class="main-content">
<h1 class="zone-name-title h1">www.sonasbathrooms.com</h1>
<h2 class="h2">Performing security verification</h2>
<p>This website uses a security service to protect against malicious bots. This page is displayed while the website verifies you are not a bot.</p>
<div id="challenge-success-text" class="h2">Verification successful. Waiting for www.sonasbathrooms.com to respond</div>
</div></div>
<script>(function(){window._cf_chl_opt={cvId:'3',cZone:'www.sonasbathrooms.com',cType:'managed'};var a=document.createElement('script');a.src='/cdn-cgi/challenge-platform/h/g/orchestrate/chl_page/v1?ray=a3b259c52c788709';document.getElementsByTagName('head')[0].appendChild(a);}());</script>
<div class="footer"><div>Ray ID: <code>a3b259c52c788709</code></div><div>Performance and Security by Cloudflare</div></div>
</body></html>`

// A bot check is a page, as far as a browser is concerned: it loads, its
// scripts run, and it has text. Returned as the answer, it reads as a site that
// says "Just a moment" about a bathroom valve. So the one thing this must do is
// tell the check from the page it stands in front of, and say whose it is.
func TestABotCheckIsNamedByWhoseCheckItIs(t *testing.T) {
	for _, c := range []struct{ name, dom, want string }{
		{"a Cloudflare challenge", cloudflareChallenge, "Cloudflare"},
		{"a DataDome captcha", `<html><head><title>example.com</title></head><body>
<p>Please enable JS and disable any ad blocker</p>
<script>var dd={'rt':'c','cid':'AHrlqAAAAAMA','hsh':'0B2C','host':'geo.captcha-delivery.com'}</script>
<script src="https://ct.captcha-delivery.com/c.js"></script></body></html>`, "DataDome"},
		{"a HUMAN press-and-hold", `<html><head><title>Access to this page has been denied</title></head><body>
<h1>Press &amp; Hold to confirm you are a human (and not a bot).</h1>
<div id="px-captcha"></div><script src="/px/captcha.js"></script></body></html>`, "HUMAN (PerimeterX)"},
	} {
		t.Run(c.name, func(t *testing.T) {
			if got := BotCheck(c.dom); got != c.want {
				t.Errorf("BotCheck = %q, want %q", got, c.want)
			}
		})
	}
}

// The opposite costs more. A page wrongly reported as a bot check is a page the
// model is told it cannot read, and it stops looking. The markers these vendors
// leave on ordinary pages are the cases to get right.
func TestAnOrdinaryPageIsNotMistakenForABotCheck(t *testing.T) {
	for _, c := range []struct{ name, dom string }{
		// Cloudflare's bot detection injects this script into pages it serves
		// normally; only the challenge page carries the challenge options.
		{"an article on a site behind Cloudflare", `<html><body><h1>Alita round valve</h1><p>` +
			strings.Repeat("A dual outlet horizontal valve in chrome, with a round trim plate. ", 25) +
			`</p><script src="/cdn-cgi/challenge-platform/scripts/jsd/main.js"></script></body></html>`},
		// Turnstile guards a form on a page that was served. The form is the
		// page, and a person would be looking at it.
		{"a sign-in form with a Turnstile widget", `<html><body><form><h1>Sign in</h1>
<label>Email <input name="email"></label><div class="cf-turnstile" data-sitekey="0x4AAA"></div>
<button>Continue</button></form>
<script src="https://challenges.cloudflare.com/turnstile/v0/api.js"></script></body></html>`},
		{"a short page that talks about captchas", `<html><body>
<p>We removed the captcha from checkout. Just a moment of your time: tell us what you think.</p></body></html>`},
	} {
		t.Run(c.name, func(t *testing.T) {
			if got := BotCheck(c.dom); got != "" {
				t.Errorf("an ordinary page was reported as %q's bot check", got)
			}
		})
	}
}
