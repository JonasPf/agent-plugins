package main

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/JonasPf/agent-plugins/internal/servetest"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// The review page in a real browser, in a box shaped like the agent's, driven
// with real mouse, keyboard and touch input. Ported from the voice-over
// skill's voice_over_page_browser.mjs: the page is the same, and what was the
// ask_operator panel's — Send, and what it says Send will do — is the page's
// own now.

// allVoices is every voice of the two models offered, as the gateway listed
// them on 2026-11-13.
const allVoices = `{"data":[
	{"id":"heygen/heygen-voice-1-turbo","name":"HeyGen: HeyGen Voice Turbo","architecture":{"output_modalities":["speech"]},
	 "supported_voices":["bram","gareth","lucian","meredith","nadine","noelle","sabrina","tabitha","vincent","weston"],
	 "pricing":{"prompt":"0.000004"}},
	{"id":"elevenlabs/eleven-flash-v2.5","name":"ElevenLabs: Eleven Flash v2.5","architecture":{"output_modalities":["speech"]},
	 "supported_voices":["adam","alice","bella","bill","brian","callum","charlie","chris","daniel","eric","george","harry",
	 "jessica","laura","liam","lily","matilda","river","roger","sarah","will"],"pricing":{"prompt":"0.00004"}}]}`

var sample = []scriptLine{
	{3, "This short demo shows how an expense report moves from submitted to paid."},
	{12.5, "Here is the list of open reports. Each one shows who sent it and how much it is for."},
	{34, "I'll open the report from the Berlin trip."},
	{44, "The totals are added up for me, and each receipt is attached to its line."},
	{63, "When I submit it, the policy check runs straight away."},
	{78.5, "It flags one item: the hotel is over the nightly limit."},
	{122, "As the manager, I can see the flag and the reason for it."},
	{135, "I'll approve it with a short note, and that note stays on the report so anyone can read it later."},
	{178, "And here it is, marked as paid."},
	{190, `A tag </textarea><img src=x onerror="document.title='pwned'"> stays words.`},
}

// lengths are the same lines once spoken.
var lengths = []float64{4.0, 7.0, 4.5, 6.0, 5.0, 6.5, 5.5, 9.0, 4.0, 5.0}

type pageLineShown struct{ Time, Words, Warn string }

type noteShown struct {
	Shown      bool
	Warn, Next string
	InSight    bool
}

func seconds(s string) float64 {
	m, r, _ := strings.Cut(s, ":")
	a, _ := strconv.ParseFloat(m, 64)
	b, _ := strconv.ParseFloat(r, 64)
	return a*60 + b
}

// reviewHost is what shows the page: the stand-in for the agent's box, or the
// bundle's own server behind a link.
type reviewHost interface {
	open(b *browser, video string)
	saved() int     // how many times Send saved
	said() []string // what Send gave the conversation
	forget()
	data() string // what the page was given to show
}

func (h *appHost) open(b *browser, video string) { b.openURL(h.srv.URL + "/?video=" + video) }
func (h *appHost) saved() int                    { return len(h.savedCalls()) }
func (h *appHost) data() string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.data_
}

// linkPage is a host that shows no pages: the page comes from a link the
// review tool gives, opened straight in the browser, and Send's message is
// the operator's to paste back, so what was said is what the page offers.
type linkPage struct {
	t  *testing.T
	cs *mcp.ClientSession
	b  *browser
}

func (l *linkPage) open(b *browser, video string) {
	l.t.Helper()
	res := servetest.Call(l.t, l.cs, "voiceover_review", `{"video":"`+video+`"}`)
	link := reviewLink.FindString(res.Text)
	if link == "" {
		l.t.Fatalf("voiceover_review gave no link: %+v", res)
	}
	l.b = b
	b.openURL(link)
}
func (l *linkPage) said() []string {
	if !l.b.inBool(`!document.getElementById('back').hidden`) {
		return nil
	}
	return []string{l.b.inStr(`document.getElementById('pasteBack').textContent`)}
}
func (l *linkPage) saved() int { return len(l.said()) }
func (l *linkPage) forget()    {}
func (l *linkPage) data() string {
	return servetest.Call(l.t, l.cs, "voiceover_review_data", `{"video":"heard.mov"}`).Text
}

func TestTheReviewPageInABrowserAsAnApp(t *testing.T) {
	chromePath(t)
	dir := reviewFixtures(t)
	h := newAppHost(t, servetest.ClientWith(t, appsCaps(), manifest, tools, runs, pages...))
	checkReviewPage(t, newBrowser(t), h, dir, false)
}

func TestTheReviewPageInABrowserFromALink(t *testing.T) {
	chromePath(t)
	dir := reviewFixtures(t)
	t.Cleanup(closeReviewLink)
	l := &linkPage{t: t, cs: servetest.Client(t, manifest, tools, runs, pages...)}
	b := newBrowser(t)
	b.direct = true
	checkReviewPage(t, b, l, dir, true)
}

// reviewFixtures is a 205-second video three times over: with a script not
// spoken yet, with the same script spoken in gareth, and with no script.
func reviewFixtures(t *testing.T) string {
	dir := t.TempDir()
	servetest.InDir(t, dir)
	if out, err := exec.Command("ffmpeg", "-y", "-hide_banner", "-loglevel", "error", "-f", "lavfi", "-i", "testsrc2=s=1832x1522:r=5:d=205",
		"-pix_fmt", "yuv420p", "-c:v", "libx264", "-preset", "ultrafast", "-g", "100", "demo.mov").CombinedOutput(); err != nil {
		t.Fatalf("could not make the video: %v\n%s", err, out)
	}
	video, _ := os.ReadFile("demo.mov")
	for _, name := range []string{"spoken.mov", "heard.mov", "silent.mov"} {
		if err := os.WriteFile(name, video, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range []string{"spoken.mov", "heard.mov"} {
		p, _ := openProject(name)
		if err := p.writeScript(sample); err != nil {
			t.Fatal(err)
		}
	}
	gareth := Voice{Name: "gareth", Model: defaultSpeech}
	for i, l := range sample {
		spokenLine(t, "heard.mov", gareth, l.Text, fmt.Sprint(lengths[i]))
	}
	heard, _ := openProject("heard.mov")
	if err := heard.writeChoices(choices{Voice: "gareth", Model: defaultSpeech}); err != nil {
		t.Fatal(err)
	}
	g := withGateway(t)
	g.on("/models", answer(200, allVoices))
	return dir
}

// checkReviewPage is every check of the page, as a person uses it. Direct is
// a page opened from a link, with no box around it.
func checkReviewPage(t *testing.T, b *browser, h reviewHost, dir string, direct bool) {
	check := func(what string, ok bool, detail ...any) {
		t.Helper()
		if !ok {
			t.Errorf("%s: %v", what, detail)
		}
	}
	lines := func() []pageLineShown {
		var out []pageLineShown
		b.in(`[...document.querySelectorAll('.line')].map(c => ({ Time: c.querySelector('.time').textContent, Words: c.querySelector('.words').value, Warn: c.querySelector('.warn').textContent }))`, &out)
		return out
	}
	status := func() []string {
		var out []string
		b.in(`[...document.querySelectorAll('.stat')].map(s => s.textContent)`, &out)
		return out
	}
	moving := func() string { return b.inStr(`document.querySelector('.line.moving .time')?.textContent || ''`) }
	covered := func() float64 {
		return b.inNum(`(() => { const r = [...document.querySelectorAll('.line')].map(c => c.getBoundingClientRect()); let n = 0; r.forEach((a, i) => r.slice(i + 1).forEach(b => { if (Math.min(a.right, b.right) - Math.max(a.left, b.left) > 1 && Math.min(a.bottom, b.bottom) - Math.max(a.top, b.top) > 1) n++; })); return n; })()`)
	}
	clipped := func() float64 {
		return b.inNum(`[...document.querySelectorAll('.words')].filter(t => t.scrollHeight > t.clientHeight + 2).length`)
	}
	pick := func(value string) {
		b.in(fmt.Sprintf(`(() => { const s = document.getElementById('voice'); s.value = %q; s.dispatchEvent(new Event('change', {bubbles:true})); })()`, value), nil)
	}
	language := func(code string) {
		b.in(fmt.Sprintf(`(() => { const s = document.getElementById('lang'); s.value = %q; s.dispatchEvent(new Event('change', {bubbles:true})); })()`, code), nil)
	}
	note := func() noteShown {
		var n noteShown
		b.in(`(() => { const n = document.getElementById('guessNote'), r = n.getBoundingClientRect(); return { Shown: !n.hidden, Warn: n.querySelector('strong').textContent, Next: n.querySelector('span').textContent, InSight: r.height > 0 && r.top >= 0 && r.bottom <= innerHeight + 1 }; })()`, &n)
		return n
	}
	off := func(id string) bool { return b.inBool(fmt.Sprintf(`document.getElementById(%q).disabled`, id)) }
	sounding := func() bool {
		return b.inBool(`(() => { const a = document.getElementById('sample'); return !a.error && !a.paused && a.currentTime > 0.05; })()`)
	}
	guesses := func() float64 { return b.inNum(`document.querySelectorAll('.line.guess').length`) }
	proposal := func() string { return b.inStr(`document.getElementById('send').title`) }
	held := func() string {
		return b.inStr(`(() => { const p = document.getElementById('held'); return p.hidden ? '' : p.textContent; })()`)
	}
	sendNow := func() { b.in(`document.getElementById('send').click()`, nil) }
	pageData := func() pageSays {
		var d pageSays
		raw := h.data()
		if err := json.Unmarshal([]byte(raw), &d); err != nil {
			t.Fatalf("the page's data: %v", err)
		}
		return d
	}
	sendInSight := func() bool {
		return b.inBool(`(() => { const r = document.getElementById('send').getBoundingClientRect(); return r.height > 0 && r.top >= 0 && r.bottom <= innerHeight; })()`)
	}

	// ---- desktop, a script not spoken yet
	b.viewport(1280, 1200, false)
	h.open(b, "spoken.mov")
	if !direct {
		var sandbox string
		b.top(`document.querySelector('iframe').getAttribute('sandbox')`, &sandbox)
		check("the page runs in the box", sandbox == "allow-scripts", sandbox)
	}
	check("every screenshot loads under the policy", b.inBool(`[...document.images].length === 7 && [...document.images].every(i => i.complete && i.naturalWidth >= 700)`))
	all := lines()
	scale := b.inNum(`document.querySelector('.slot[data-t="30"]').getBoundingClientRect().top - document.querySelector('.slot[data-t="0"]').getBoundingClientRect().top`) / 30
	var spots, tall []float64
	b.in(`[...document.querySelectorAll('.line')].map(c => c.getBoundingClientRect().top - document.querySelector('.slot').getBoundingClientRect().top)`, &spots)
	atMoment := len(all) == 10
	for i := range all {
		atMoment = atMoment && math.Abs(spots[i]-seconds(all[i].Time)*scale) < 1.5
	}
	check("each line sits on the timeline at its moment", atMoment, spots, scale)
	b.in(`[...document.querySelectorAll('.line')].map(c => c.getBoundingClientRect().height)`, &tall)
	saying := float64(len(strings.Fields(all[7].Words))) / 2.3 * scale
	check("a line is as tall as it takes to say", math.Abs(tall[7]-saying) < 2 && tall[7] > tall[2], tall, saying)
	check("and stands beside the screenshot of its moment", b.inBool(`(() => { const b = document.querySelector('.slot[data-t="120"]').getBoundingClientRect(), c = document.querySelectorAll('.line')[6].getBoundingClientRect(); return c.top >= b.top && c.top < b.bottom && b.right <= c.left; })()`))
	check("no line hides another", covered() == 0, covered())
	first := status()
	check("the status line counts lines and cost", strings.HasPrefix(first[0], "10 lines") && strings.Contains(first[1], "$0.00"), first)
	var title string
	b.top(`document.title`, &title)
	check("a tag in a line stays words", strings.Contains(all[9].Words, "<img src=x") && b.inNum(`document.querySelectorAll('.line img').length`) == 0 &&
		b.inNum(`document.images.length`) == 7 && (title == "host" || direct && title == "Voice-over review"), all[9], title)
	b.shot("desktop")
	check("no line is cut off", clipped() == 0, clipped())
	b.viewport(560, 1200, false)
	check("no line is cut off after the window narrows", clipped() == 0, clipped())
	b.viewport(200, 1200, false)
	b.viewport(1280, 1200, false)
	var shares []float64
	b.in(`[...document.querySelectorAll('.line')].map(c => c.getBoundingClientRect().width / document.getElementById('track').getBoundingClientRect().width)`, &shares)
	spread := true
	for _, w := range shares {
		spread = spread && w > 0.99
	}
	check("lines spread out again when the window widens", spread, shares)

	// drag the second line down the timeline, with a real mouse, letting the page scroll
	from := b.at(".line:nth-of-type(2) .grip", 0.5)
	b.mouse("mouseMoved", from)
	b.press("mousePressed", from)
	time.Sleep(20 * time.Millisecond)
	b.press("mouseMoved", point{from.X, from.Y + 40})
	time.Sleep(40 * time.Millisecond)
	check("a line moves with the pointer and says where it is", moving() == "0:15.0", moving())
	var edge float64
	edge = b.bottom() - 30
	b.press("mouseMoved", point{from.X, edge})
	time.Sleep(100 * time.Millisecond)
	early := moving()
	time.Sleep(300 * time.Millisecond)
	check("held at the window edge, the page keeps going", seconds(moving()) > seconds(early), early, moving())
	b.press("mouseMoved", point{from.X, edge - 300})
	time.Sleep(120 * time.Millisecond)
	hovering := moving()
	b.mouse("mouseReleased", point{from.X, edge - 300})
	time.Sleep(50 * time.Millisecond)
	all = lines()
	var moved pageLineShown
	inOrder := true
	for i, l := range all {
		if strings.HasPrefix(l.Words, "Here is the list") {
			moved = l
		}
		inOrder = inOrder && (i == 0 || seconds(l.Time) >= seconds(all[i-1].Time))
	}
	check("the dragged line lands where the drag said", moved.Time == hovering && seconds(moved.Time) > 15, moved, hovering)
	check("nothing stays marked as moving", !b.inBool(`!!document.querySelector('.line.moving')`))
	check("lines stay in time order", inOrder, all)
	check("every screenshot is still loaded after a move", b.inBool(`[...document.images].every(i => i.naturalWidth > 0)`))
	b.click("#undo")
	for _, l := range lines() {
		if strings.HasPrefix(l.Words, "Here is the list") {
			check("Undo puts it back", l.Time == "0:12.5", l)
		}
	}

	// the ends of the video
	b.drag(".line:nth-of-type(1) .grip", -200, 0)
	check("a line stops at the start of the video", lines()[0].Time == "0:00.0", lines()[0])
	b.click("#undo")
	b.drag(".line:nth-of-type(10) .grip", 400, 900*time.Millisecond)
	check("a line stops at the end of the video", lines()[9].Time == "3:24.5", lines()[9])
	b.click("#undo")

	// a line dragged onto another
	b.drag(".line:nth-of-type(2) .grip", -7.5*scale, 0)
	all = lines()
	check("the line it lands on says it will be cut off", strings.Contains(all[0].Warn, "cuts off"), all[0])
	check("the status line counts it", strings.Contains(status()[2], "cut off"), status())
	check("both stay in full view, side by side", covered() == 0, covered())
	b.shot("overlap")
	b.click("#undo")
	check("and the warning goes when they part", lines()[0].Warn == "", lines()[0])

	// the keyboard
	before := lines()[0].Time
	b.click(".line:nth-of-type(1) .grip")
	b.key("ArrowDown")
	check("the down arrow moves a line later", seconds(lines()[0].Time) == seconds(before)+0.5, lines()[0].Time, before)
	check("focus stays on the handle", b.inBool(`document.activeElement.classList.contains('grip')`))
	b.key("ArrowUp")
	check("the up arrow moves it back", lines()[0].Time == before, lines()[0])
	b.click("#undo")
	b.click("#undo")

	// words
	b.replaceText(".line:nth-of-type(1) .words", "A much shorter opening.")
	all = lines()
	check("words can be changed", all[0].Words == "A much shorter opening.", all[0])
	check("the count follows the words", status()[0] != first[0], status())
	b.click(".line:nth-of-type(2) .words")
	b.insert(" Added.")
	b.click("#undo")
	all = lines()
	check("Undo takes back one edit, not every letter", !strings.Contains(all[1].Words, "Added") && all[0].Words == "A much shorter opening.", all[1])
	b.click("#undo")
	check("and the edit before it", strings.HasPrefix(lines()[0].Words, "This short demo"))

	// remove, add
	count := len(lines())
	b.click(".line:nth-of-type(1) .del")
	check("Remove takes the line out", len(lines()) == count-1)
	b.click("#undo")
	b.click(`.slot[data-t="60"] .add`)
	check("Add puts the cursor in a new line", b.inBool(`document.activeElement.classList.contains('words')`))
	b.insert("A new line.")
	var added *pageLineShown
	for _, l := range lines() {
		if l.Words == "A new line." {
			l := l
			added = &l
		}
	}
	check("a new line lands clear of what is there", added != nil && seconds(added.Time) >= 60, added)
	b.click("#undo")
	b.click("#undo")
	gone := true
	for _, l := range lines() {
		gone = gone && l.Words != "A new line." && l.Words != ""
	}
	check("Undo takes a new line out again", gone, lines())
	b.click(`.slot[data-t="60"] .add`)
	check("an empty line is left out of the count", strings.HasPrefix(status()[0], "10 lines") && len(lines()) == 11, status())
	b.click("#undo")
	check("and is gone again", len(lines()) == 10, lines())

	// warnings
	b.replaceText(".line:nth-of-type(1) .words", strings.Repeat("word ", 30))
	check("a line that runs into the next says it will be cut off", strings.Contains(lines()[0].Warn, "cuts off"), lines()[0])
	check("the status line says so too", strings.Contains(status()[2], "cut off"), status())
	b.click("#undo")
	check("the warning goes with the words", lines()[0].Warn == "", lines()[0])
	b.replaceText(".line:nth-of-type(10) .words", strings.Repeat("word ", 60))
	check("a last line that runs past the end says the video ends first", strings.Contains(lines()[9].Warn, "video ends"), lines()[9])
	check("and is counted as cut off, as voiceover_render will cut it", strings.Contains(status()[2], "cut off"), status())
	b.click("#undo")

	// voice and language
	cheap := status()[1]
	check("no sample plays until a voice is picked", b.inBool(`(() => { const a = document.getElementById('sample'); return a.paused && !a.currentSrc; })()`))
	samples := pageData().Samples
	playing := func(model, voice string) bool {
		return b.inBool(fmt.Sprintf(`document.getElementById('sample').currentSrc.endsWith(%q)`, samples[model][voice]))
	}
	pick("1|brian")
	b.until("the sample of the voice picked playing", sounding)
	check("picking a voice plays its sample", playing("elevenlabs/eleven-flash-v2.5", "brian"))
	d := b.inNum(`document.getElementById('sample').duration`)
	check("a sample is a few seconds long", d > 0.8 && d < 6, d)
	pick("1|alice")
	b.until("the next sample playing", sounding)
	check("picking another plays that one instead", playing("elevenlabs/eleven-flash-v2.5", "alice"))
	dear := status()[1]
	price := func(s string) float64 {
		i := strings.Index(s, "$")
		v, _ := strconv.ParseFloat(strings.TrimRight(s[i+1:], " ,."), 64)
		return v
	}
	check("the cost follows the voice", price(dear) > 5*price(cheap), cheap, dear)
	check("all voices of both models are offered", b.inNum(`document.querySelectorAll('#voice option').length`) == 31 &&
		b.inNum(`document.querySelectorAll('#voice optgroup').length`) == 2)
	said := note()
	check("a page not spoken yet says every line is a guess, and that Send speaks them", said.Shown && said.Warn == "10 lines are guesses." &&
		strings.HasPrefix(said.Next, "Press Send to speak them, about $") && strings.Contains(said.Next, "with alice") && strings.Contains(said.Next, "real lengths"), said)
	b.in(`document.scrollingElement.scrollTop = 1500`, nil)
	time.Sleep(40 * time.Millisecond)
	check("and says it in sight however far the page is scrolled", note().InSight && b.inNum(`document.scrollingElement.scrollTop`) > 1000, note())
	check("Send stays in sight however far the page is scrolled", sendInSight())
	language("de")
	said = note()
	check("a new language warns first, in bold, that every length is a guess until it is spoken", said.Warn == "Every length here is a guess until German is spoken.", said)
	check("then why, and what Send does", strings.Contains(said.Next, "German can run longer or shorter") && strings.Contains(said.Next, "Press Send to translate and speak every line"), said)
	b.shot("language")
	language("")
	check("and stops saying it", note().Warn == "10 lines are guesses.", note())
	b.in(`document.scrollingElement.scrollTop = 0`, nil)

	// the zoom
	b.click(`.slot[data-t="30"] .zoom`)
	check("tapping a screenshot shows it larger", b.inBool(`document.querySelector('.slot[data-t="30"]').classList.contains('big')`))
	wide := b.inNum(`document.querySelector('.slot[data-t="30"] img').getBoundingClientRect().width`)
	check("larger means most of the width", wide > 900, wide)
	b.click(`.slot[data-t="30"] .zoom`)

	// Send, the one approval
	b.until("what Send will do", func() bool { return strings.Contains(proposal(), "10 lines") })
	check("Send says what it will do", strings.Contains(proposal(), "10 lines") && strings.Contains(proposal(), "alice"), proposal())
	check("no Approve on the page", !b.inBool(`[...document.querySelectorAll('button')].some(b => /approve/i.test(b.textContent))`))
	spokenScript := mustRead(t, filepath.Join(dir, "spoken.voiceover", "script.json"))
	check("nothing is saved before Send", h.saved() == 0, h.saved())
	b.shot("send")
	pick("0|gareth")
	b.until("Send follows the voice", func() bool { return strings.Contains(proposal(), "gareth") })
	pick("1|alice")
	language("de")
	b.until("Send follows the language", func() bool { return strings.Contains(proposal(), "German") })
	check("Send says a translation comes first", strings.Contains(proposal(), "translated into German"), proposal())
	language("")
	b.drag(".line:nth-of-type(1) .grip", 3*scale, 0)
	b.until("the line moved", func() bool { return seconds(lines()[0].Time) > 3 })
	b.click("#undo")

	// Send pressed in the same breath as the last change
	b.replaceText(".line:nth-of-type(1) .words", "Changed my mind.")
	sendNow()
	b.until("Send", func() bool { return len(h.said()) == 1 })
	check("Send saves once, then speaks", h.saved() == 1, h.saved())
	got := readScriptFile(t, filepath.Join(dir, "spoken.voiceover", "script.json"))
	check("the script holds every line, each with a time", len(got) == 10 && got[0].At == 3 && got[9].At == 190, got)
	check("Send carries the last change, however quick", got[0].Text == "Changed my mind.", got[0])
	check("the script changed only by Send", spokenScript != mustRead(t, filepath.Join(dir, "spoken.voiceover", "script.json")))
	var ch choices
	_ = json.Unmarshal([]byte(mustRead(t, filepath.Join(dir, "spoken.voiceover", "project.json"))), &ch)
	check("the voice picked is the project's", ch == choices{Voice: "alice", Model: "elevenlabs/eleven-flash-v2.5"}, ch)
	msg := h.said()[0]
	check("Send tells the conversation what was settled and what comes next", strings.Contains(msg, "10 lines") &&
		strings.Contains(msg, "alice") && strings.Contains(msg, "voiceover_render"), msg)
	b.until("the page says it was sent", func() bool { return b.inStr(`document.getElementById('send').textContent`) == "Sent" })
	check("and Send is done", off("send") && strings.Contains(b.inStr(`document.getElementById('done').textContent`), "Sent."))
	if direct {
		// A page in a browser cannot reach the conversation: it gives the
		// message to paste there, and copies it.
		check("the page gives Send's message to paste back", b.inStr(`document.getElementById('pasteBack').textContent`) == msg &&
			strings.Contains(b.inStr(`document.getElementById('done').textContent`), "Paste this"))
		b.call("Browser.grantPermissions", map[string]any{"permissions": []string{"clipboardReadWrite", "clipboardSanitizedWrite"}}, "", nil)
		b.click("#copy")
		b.until("copied", func() bool { return b.inStr(`document.getElementById('copy').textContent`) == "Copied" })
		check("Copy puts it on the clipboard", b.inStr(`navigator.clipboard.readText()`) == msg)
	}

	// a line too long to speak
	h.forget()
	h.open(b, "spoken.mov")
	b.replaceText(".line:nth-of-type(1) .words", strings.Repeat("x", 5001))
	b.until("the hold", func() bool { return strings.Contains(held(), "too long") })
	sendNow()
	time.Sleep(100 * time.Millisecond)
	check("Send will not take a line too long to speak", off("send") && h.saved() == 0 && len(h.said()) == 0, h.said())
	b.click("#undo")
	b.until("the hold lifted", func() bool { return held() == "" && !off("send") })

	// ---- the same lines, spoken: each as long as its speech, and heard
	h.forget()
	h.open(b, "heard.mov")
	data := pageData()
	chars, secs := 0.0, 0.0
	for _, l := range data.Lines {
		chars += float64(len(strings.Join(strings.Fields(l.Text), " ")))
		if l.Length != nil {
			secs += *l.Length
		}
	}
	pace := chars / secs
	var heights []float64
	b.in(`[...document.querySelectorAll('.line')].map(c => c.getBoundingClientRect().height)`, &heights)
	asSpoken := len(heights) == 10
	for i := range heights {
		asSpoken = asSpoken && math.Abs(heights[i]-lengths[i]*scale) < 2
	}
	check("a spoken line is as tall as its speech, not as its words", asSpoken, heights)
	check("no spoken line is a guess, and nothing is left to speak", guesses() == 0 && status()[1] == "all spoken", status())
	check("so no note says one is", !note().Shown, note())
	b.shot("heard")
	check("a spoken line shows ▶ on its handle", b.inNum(`[...document.querySelectorAll('.cue')].filter(c => getComputedStyle(c).display !== 'none').length`) == 10)
	b.click(".line:nth-of-type(2) .grip")
	b.until("a line's speech playing", sounding)
	check("a tap on the handle plays that line's own speech", b.inBool(fmt.Sprintf(`document.getElementById('sample').currentSrc.endsWith(%q)`, data.Lines[1].Audio)))
	check("and offers to stop it", strings.Contains(b.inStr(`document.querySelector('.line:nth-of-type(2) .grip').getAttribute('aria-label')`), "stop it") &&
		b.inStr(`document.querySelector('.line:nth-of-type(2) .cue').textContent`) == "■")
	b.click(".line:nth-of-type(2) .grip")
	check("tapped again, it stops", b.inBool(`document.getElementById('sample').paused`))
	b.key("Enter")
	time.Sleep(200 * time.Millisecond)
	check("Enter on the handle plays it too", sounding())
	b.key("Enter")
	before2 := lines()[1].Time
	b.drag(".line:nth-of-type(2) .grip", 3*scale, 0)
	check("a drag moves the line and plays nothing", lines()[1].Time != before2 && b.inBool(`document.getElementById('sample').paused`), lines()[1])
	b.click("#undo")

	shorter := "Here is the list of open reports, each with who sent it."
	b.in(`document.querySelectorAll('.line')[1].querySelector('.words').scrollIntoView({ block: 'center' })`, nil)
	time.Sleep(40 * time.Millisecond)
	trackBefore := b.inNum(`document.getElementById('track').getBoundingClientRect().top`)
	b.replaceText(".line:nth-of-type(2) .words", shorter)
	said = note()
	check("a changed line brings the note: one guess, and Send to speak it", said.Shown && said.Warn == "1 line is a guess." &&
		strings.HasPrefix(said.Next, "Press Send to speak it, about $") && strings.Contains(said.Next, "with gareth"), said)
	check("which moves nothing on the page", b.inNum(`document.getElementById('track').getBoundingClientRect().top`) == trackBefore)
	var changed struct {
		Guess bool
		Cue   string
		H     float64
	}
	b.in(`(() => { const c = document.querySelectorAll('.line')[1]; return { Guess: c.classList.contains('guess'), Cue: getComputedStyle(c.querySelector('.cue')).display, H: c.getBoundingClientRect().height }; })()`, &changed)
	check("a line whose words changed is a guess until it is spoken, with nothing to play", changed.Guess && changed.Cue == "none", changed)
	check("as long as its words take at the pace of the lines spoken", math.Abs(changed.H-float64(len(shorter))/pace*scale) < 2, changed, float64(len(shorter))/pace*scale)
	check("the status line counts it to speak", strings.HasPrefix(status()[1], "1 to speak"), status())
	b.until("Send names what is to speak", func() bool { return strings.Contains(proposal(), "1 to speak") })
	b.click("#undo")
	check("Undo makes it spoken again", guesses() == 0 && status()[1] == "all spoken", status())
	check("and takes the note away", !note().Shown, note())
	pick("0|meredith")
	check("another voice makes every line a guess", guesses() == 10 && strings.HasPrefix(status()[1], "10 to speak"), status())
	said = note()
	check("and the note says so, and that Send speaks them all in it", said.Warn == "Every length here is a guess until meredith speaks it." &&
		strings.HasPrefix(said.Next, "Press Send to speak every line in meredith, about $"), said)
	tallNow := b.inNum(`document.querySelector('.line').getBoundingClientRect().height`)
	check("as long as its words take at the usual pace", math.Abs(tallNow-float64(len(strings.Fields(sample[0].Text)))/2.3*scale) < 2, tallNow)
	pick("0|gareth")
	check("the voice spoken makes them spoken again", guesses() == 0 && !note().Shown)
	language("de")
	check("so does a language, the other way", guesses() == 10)
	language("")

	// join a line and the next
	check("Split and Join wait for a line to write in", off("split") && off("join"))
	opening := lines()[2].Words
	b.click(".line:nth-of-type(3) .words")
	b.until("Join ready", func() bool { return !off("join") })
	b.click("#join")
	all = lines()
	check("Join makes one line of a line and the next", len(all) == 9 && all[2].Words == opening+" "+sample[3].Text && all[2].Time == "0:34.0", all[2])
	check("which is to be spoken", guesses() == 1 && b.inBool(`document.querySelectorAll('.line')[2].classList.contains('guess')`))
	check("the cursor stands where they met", b.inBool(fmt.Sprintf(`document.activeElement === document.querySelectorAll('.line')[2].querySelector('.words') && document.activeElement.selectionStart === %d`, len(opening))))
	b.click("#undo")
	check("Undo parts them again, both spoken", len(lines()) == 10 && guesses() == 0)
	b.click(".line:nth-of-type(10) .words")
	b.in(`(() => { const t = document.activeElement; t.setSelectionRange(t.value.length, t.value.length); })()`, nil)
	time.Sleep(60 * time.Millisecond)
	check("Split is off with the cursor at an end of the words, and Join on the last line", off("split") && off("join"))
	b.click(".line:nth-of-type(1) .grip")
	check("both are off once no line is being written in", off("split") && off("join"))

	// split a line in two where the cursor is
	whole := lines()[7].Words
	cut := strings.Index(whole, " and that")
	b.click(".line:nth-of-type(8) .words")
	b.in(fmt.Sprintf(`document.activeElement.setSelectionRange(%d, %d)`, cut, cut), nil)
	b.until("Split ready", func() bool { return !off("split") })
	b.click("#split")
	all = lines()
	check("Split makes two lines of one, where the cursor was", len(all) == 11 && all[7].Words == whole[:cut] && all[8].Words == whole[cut+1:], all[7:9])
	ends := 135 + float64(len(whole[:cut]))/pace
	check("the first keeps its start, and the second starts as the first ends, not cutting it off",
		all[7].Time == "2:15.0" && seconds(all[8].Time) >= ends && seconds(all[8].Time)-ends < 0.1 && all[7].Warn == "", all[7], all[8].Time, ends)
	check("both are to be spoken", guesses() == 2 && strings.HasPrefix(status()[1], "2 to speak"), status())
	check("the cursor carries on at the start of the second", b.inBool(`document.activeElement === document.querySelectorAll('.line')[8].querySelector('.words') && document.activeElement.selectionStart === 0`))
	b.until("Send names the lines", func() bool { return strings.Contains(proposal(), "11 lines") })
	check("Send says how many lines are to be spoken", strings.Contains(proposal(), "2 to speak"), proposal())
	sendNow()
	b.until("Send", func() bool { return len(h.said()) == 1 })
	msg = h.said()[0]
	check("only the two halves are spoken again; every other line keeps its speech",
		strings.Contains(msg, "11 lines") && strings.Contains(msg, "2 to speak") && strings.Contains(msg, "9 keep their speech"), msg)

	// ---- a video with no words at all
	h.forget()
	h.open(b, "silent.mov")
	check("an empty page asks for the script", b.inBool(`!document.getElementById('paste').hidden`))
	b.until("the hold", func() bool { return strings.Contains(held(), "at least one line") })
	sendNow()
	time.Sleep(100 * time.Millisecond)
	check("Send with nothing says so and does nothing", h.saved() == 0 && len(h.said()) == 0, h.said())
	b.replaceText("#pasteText", "Version 2.3 of the report opens here. Then the receipts follow!\nAnd the last line ends it.")
	b.click("#pasteGo")
	all = lines()
	check("each sentence becomes a line, and a version number stays whole", len(all) == 3 &&
		all[0].Words == "Version 2.3 of the report opens here." && all[2].Words == "And the last line ends it.", all)
	check("they spread across the video", len(all) == 3 && seconds(all[0].Time) < 10 && seconds(all[2].Time) > 120 && seconds(all[2].Time) < 204, all)
	check("the paste box goes away", b.inBool(`document.getElementById('paste').hidden`))
	b.until("Send names the lines", func() bool { return strings.Contains(proposal(), "3 lines") })
	sendNow()
	b.until("Send", func() bool { return len(h.said()) == 1 })
	silent := readScriptFile(t, filepath.Join(dir, "silent.voiceover", "script.json"))
	check("a silent video reaches speech the same way", len(silent) == 3 && strings.Contains(h.said()[0], "gareth"), h.said())

	// ---- a phone, with a finger
	b.viewport(390, 844, true)
	h.open(b, "spoken.mov")
	b.shot("phone")
	b.in(`document.scrollingElement.scrollTop = 99999`, nil)
	time.Sleep(40 * time.Millisecond)
	check("Send stays in sight on a phone with the screen scrolled to the end", b.inNum(`document.scrollingElement.scrollTop`) > 0 && sendInSight())
	var foot struct{ Top, Bottom, Tall float64 }
	b.in(`(() => { const r = document.getElementById('guessNote').getBoundingClientRect(); return { Top: r.top, Bottom: r.bottom, Tall: r.height }; })()`, &foot)
	screen := b.inNum(`innerHeight`)
	check("the note stands on the screen of a phone, at the foot, taking little of it",
		foot.Tall > 0 && foot.Tall <= 110 && foot.Top >= 0 && foot.Bottom <= screen+1, foot, screen)
	b.in(`document.scrollingElement.scrollTop = 0`, nil)
	sticking := func() any {
		var s any
		b.in(`[...new Set([...document.querySelectorAll('body *')].filter(e => e.getBoundingClientRect().right > document.documentElement.clientWidth + 1).map(e => e.className || e.tagName))]`, &s)
		return s
	}
	overflow := b.inNum(`document.documentElement.scrollWidth - document.documentElement.clientWidth`)
	check("nothing runs off the side on a phone", overflow <= 0, overflow, sticking())
	check("the screenshots stay beside the lines on a phone", b.inBool(`document.querySelector('.slot').getBoundingClientRect().right <= document.querySelector('.line').getBoundingClientRect().left`))
	check("no line hides another on a phone", covered() == 0, covered())
	check("no line is cut off on a phone", clipped() == 0, clipped())
	bar := b.inNum(`document.querySelector('.status').offsetHeight`)
	check("the top bar stays small on a phone", bar <= 64, bar)
	firstShot := b.inNum(`document.querySelector('.shot').getBoundingClientRect().top`)
	check("a screenshot shows without scrolling on a phone", firstShot < 300, firstShot)
	grip := b.at(".line:nth-of-type(3) .grip", 0.5)
	beforeTouch := lines()[2]
	b.touch("touchStart", grip)
	time.Sleep(20 * time.Millisecond)
	for i := 1; i < 12; i++ {
		b.touch("touchMove", point{grip.X, grip.Y + float64(i*12)})
	}
	time.Sleep(120 * time.Millisecond)
	hoverPhone := moving()
	b.touch("touchEnd", point{})
	time.Sleep(50 * time.Millisecond)
	var afterTouch pageLineShown
	for _, l := range lines() {
		if l.Words == beforeTouch.Words {
			afterTouch = l
		}
	}
	check("a finger can drag a line", afterTouch.Time == hoverPhone && afterTouch.Time != beforeTouch.Time, beforeTouch, afterTouch, hoverPhone)
	// five lines on one moment, on a phone: narrow columns, but none squeezed away
	for i := 1; i < 5; i++ {
		var gap []float64
		for _, l := range lines() {
			gap = append(gap, seconds(l.Time))
		}
		box := b.at(fmt.Sprintf(".line:nth-of-type(%d) .grip", i+1), 0.5)
		up := (gap[i] - gap[0]) * scale
		b.touch("touchStart", box)
		time.Sleep(20 * time.Millisecond)
		b.touch("touchMove", point{box.X, box.Y - up})
		time.Sleep(60 * time.Millisecond)
		b.touch("touchEnd", point{})
		time.Sleep(40 * time.Millisecond)
	}
	b.at(".line:nth-of-type(1) .grip", 0.5)
	b.shot("phone-crowded")
	narrowest := b.inNum(`Math.min(...[...document.querySelectorAll('.line')].slice(0, 5).map(c => c.querySelector('.words').getBoundingClientRect().width))`)
	check("lines crowded on a phone keep room for their words", narrowest >= 40, narrowest)
	check("and nothing runs off the side", b.inNum(`document.documentElement.scrollWidth - document.documentElement.clientWidth`) <= 0, sticking())

	// a spoken page on a phone, and a line split with a finger
	h.open(b, "heard.mov")
	check("a spoken page fits a phone: nothing runs off the side, hides or is cut off",
		b.inNum(`document.documentElement.scrollWidth - document.documentElement.clientWidth`) <= 0 && covered() == 0 && clipped() == 0,
		sticking(), covered(), clipped())
	tap := func(selector string) {
		p := b.at(selector, 0.5)
		b.touch("touchStart", p)
		time.Sleep(20 * time.Millisecond)
		b.touch("touchEnd", point{})
		time.Sleep(60 * time.Millisecond)
	}
	tap(".line:nth-of-type(2) .words")
	b.in(`document.querySelectorAll('.line')[1].querySelector('.words').setSelectionRange(33, 33)`, nil)
	b.until("Split ready on a phone", func() bool { return !off("split") })
	tap("#split")
	// Send saved the 11 lines of the split above, and the page shows what is saved: a split here makes 12.
	check("a finger can split a line", len(lines()) == 12 && lines()[1].Words == sample[1].Text[:33], len(lines()), lines()[1:3])
	check("the top bar stays small with Split and Join in it", b.inNum(`document.querySelector('.status').offsetHeight`) <= 64)

	// ---- dark
	b.viewport(1280, 900, false)
	b.call("Emulation.setEmulatedMedia", map[string]any{"features": []map[string]string{{"name": "prefers-color-scheme", "value": "dark"}}}, b.page, nil)
	h.open(b, "spoken.mov")
	b.shot("dark")
	check("no line is cut off in the dark", clipped() == 0, clipped())

	b.mu.Lock()
	var real []string
	for _, n := range b.noise {
		if !strings.Contains(n, "favicon") && !strings.Contains(n, "404") {
			real = append(real, n)
		}
	}
	b.mu.Unlock()
	check("no error and no policy refusal in the whole run", len(real) == 0, real)
}

func readScriptFile(t *testing.T, path string) []scriptLine {
	t.Helper()
	var lines []scriptLine
	if err := json.Unmarshal([]byte(mustRead(t, path)), &lines); err != nil {
		t.Fatal(err)
	}
	return lines
}
