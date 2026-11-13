package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/JonasPf/agent-plugins/internal/web"
	"github.com/coder/websocket"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// What the review page's test runs on: a real browser, spoken to directly
// over the DevTools protocol, and a host shaped like the agent's, which shows
// the page in a sandboxed box under the same policy and passes its messages
// to the bundle's server.

// appPolicy is the agent's (web/apphost.js): what is in the page's own text,
// and nothing from anywhere else.
const appPolicy = "default-src 'none'; script-src 'unsafe-inline'; style-src 'unsafe-inline'; " +
	"img-src data: blob:; media-src data: blob:; font-src data:; connect-src 'none'; " +
	"object-src 'none'; form-action 'none'; base-uri 'none'"

// hostPage shows the page as the agent does: in a box with scripts and
// nothing else, the policy at the top of its document. It answers the
// page's messages, and is told nothing of the page but what it says.
const hostPage = `<!doctype html><meta charset=utf-8><meta name=viewport content="width=device-width,initial-scale=1">
<title>host</title><style>html,body{margin:0;height:100%}iframe{display:block;width:100%;height:100vh;border:0}</style>
<iframe sandbox="allow-scripts" id="app"></iframe>
<script>
const frame = document.getElementById('app');
const args = { video: new URLSearchParams(location.search).get('video') };
fetch('/page').then(r => r.text()).then(html => {
  frame.srcdoc = '<!doctype html><meta http-equiv="Content-Security-Policy" content="' + POLICY + '">' + html;
});
let started = false;
window.addEventListener('message', async e => {
  if (e.source !== frame.contentWindow) return;
  const m = e.data, send = x => frame.contentWindow.postMessage(x, '*');
  if (!m || m.jsonrpc !== '2.0') return;
  if (m.id === undefined || m.id === null) {
    if (m.method === 'ui/notifications/initialized' && !started) {
      started = true;
      send({ jsonrpc: '2.0', method: 'ui/notifications/tool-input', params: { arguments: args } });
      send({ jsonrpc: '2.0', method: 'ui/notifications/tool-result', params: { content: [{ type: 'text', text: 'opened' }] } });
    }
    return;
  }
  try {
    let result;
    if (m.method === 'ui/initialize') result = { protocolVersion: '2026-01-26', hostInfo: { name: 'test host', version: '0' },
      hostCapabilities: { serverTools: {} }, hostContext: { displayMode: 'fullscreen', availableDisplayModes: ['fullscreen'] } };
    else if (m.method === 'tools/call') result = await (await fetch('/call', { method: 'POST', body: JSON.stringify(m.params) })).json();
    else if (m.method === 'ui/message') { await fetch('/message', { method: 'POST', body: JSON.stringify(m.params) }); result = {}; }
    else throw Object.assign(new Error(m.method + ' is not something this host does'), { code: -32601 });
    send({ jsonrpc: '2.0', id: m.id, result });
  } catch (err) {
    send({ jsonrpc: '2.0', id: m.id, error: { code: err.code || -32000, message: err.message } });
  }
});
</script>`

// appHost serves the host, the page, and the two things a page can cause: a
// call of one of its server's tools, and a message to the conversation.
type appHost struct {
	srv      *httptest.Server
	mu       sync.Mutex
	calls    []string
	messages []string
	data     string // what the last voiceover_review_data answered
}

func newAppHost(t *testing.T, cs *mcp.ClientSession) *appHost {
	h := &appHost{}
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/html")
		_, _ = io.WriteString(w, strings.Replace(hostPage, "POLICY", fmt.Sprintf("%q", appPolicy), 1))
	})
	mux.HandleFunc("/page", func(w http.ResponseWriter, r *http.Request) {
		res, err := cs.ReadResource(r.Context(), &mcp.ReadResourceParams{URI: reviewURI})
		if err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		_, _ = io.WriteString(w, res.Contents[0].Text)
	})
	mux.HandleFunc("/call", func(w http.ResponseWriter, r *http.Request) {
		var p mcp.CallToolParams
		if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		res, err := cs.CallTool(r.Context(), &p)
		if err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		b, _ := json.Marshal(res)
		h.mu.Lock()
		h.calls = append(h.calls, p.Name)
		if p.Name == "voiceover_review_data" && !res.IsError {
			h.data = res.Content[0].(*mcp.TextContent).Text
		}
		h.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(b)
	})
	mux.HandleFunc("/message", func(w http.ResponseWriter, r *http.Request) {
		var m struct {
			Content struct {
				Text string `json:"text"`
			} `json:"content"`
		}
		_ = json.NewDecoder(r.Body).Decode(&m)
		h.mu.Lock()
		h.messages = append(h.messages, m.Content.Text)
		h.mu.Unlock()
		_, _ = io.WriteString(w, "{}")
	})
	h.srv = httptest.NewServer(mux)
	t.Cleanup(h.srv.Close)
	return h
}

func (h *appHost) saved() []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	var out []string
	for _, c := range h.calls {
		if c == "voiceover_save_review" {
			out = append(out, c)
		}
	}
	return out
}

func (h *appHost) said() []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]string(nil), h.messages...)
}

func (h *appHost) forget() {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.calls, h.messages = nil, nil
}

// chromePath is the browser the bundle's tests use: the one the agent's image
// has, or, on a Mac, Chrome. A run that claims to test everything fails
// without one; any other run says it could not.
func chromePath(t *testing.T) string {
	if p, err := web.Browser(); err == nil {
		return p
	}
	mac := "/Applications/Google Chrome.app/Contents/MacOS/Google Chrome"
	if runtime.GOOS == "darwin" && web.Exists(mac) {
		return mac
	}
	if os.Getenv("PLUGINS_TEST_FULL") != "" {
		t.Fatalf("no browser at %s, and this run claims to test everything", web.BrowserPath)
	}
	t.Skipf("no browser at %s", web.BrowserPath)
	return ""
}

// browser is a headless Chrome and the one tab the test drives.
type browser struct {
	t       *testing.T
	ctx     context.Context
	conn    *websocket.Conn
	mu      sync.Mutex
	next    int64
	waiting map[int64]chan cdpReply
	page    string // the tab's session
	// children are the sessions of frames in their own process, and
	// contexts every script context seen, the box's among them.
	children []string
	contexts []cdpContext
	noise    []string
}

type cdpReply struct {
	Result json.RawMessage
	Error  *struct{ Message string }
}

type cdpContext struct {
	ID      int `json:"id"`
	AuxData struct {
		FrameID   string `json:"frameId"`
		IsDefault bool   `json:"isDefault"`
	} `json:"auxData"`
}

func newBrowser(t *testing.T) *browser {
	path := chromePath(t)
	profile := t.TempDir()
	args := []string{"--headless=new", "--remote-debugging-port=0", "--user-data-dir=" + profile, "--no-first-run",
		"--no-default-browser-check", "--disable-gpu", "--autoplay-policy=no-user-gesture-required", "about:blank"}
	if os.Getuid() == 0 {
		// As root, in a container, chromium will not start with its own sandbox.
		args = append([]string{"--no-sandbox"}, args...)
	}
	cmd := exec.Command(path, args...)
	if err := cmd.Start(); err != nil {
		t.Fatalf("the browser did not start: %v", err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait() })
	var port []string
	for i := 0; i < 200 && port == nil; i++ {
		time.Sleep(50 * time.Millisecond)
		if b, err := os.ReadFile(filepath.Join(profile, "DevToolsActivePort")); err == nil {
			if lines := strings.Split(string(b), "\n"); len(lines) >= 2 && lines[1] != "" {
				port = lines
			}
		}
	}
	if port == nil {
		t.Fatal("the browser never said where to reach it")
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	conn, _, err := websocket.Dial(ctx, "ws://127.0.0.1:"+port[0]+port[1], nil)
	if err != nil {
		t.Fatal(err)
	}
	conn.SetReadLimit(256 << 20)
	b := &browser{t: t, ctx: ctx, conn: conn, waiting: map[int64]chan cdpReply{}}
	go b.read()
	var target struct{ TargetID string }
	b.call("Target.createTarget", map[string]any{"url": "about:blank"}, "", &target)
	var attached struct{ SessionID string }
	b.call("Target.attachToTarget", map[string]any{"targetId": target.TargetID, "flatten": true}, "", &attached)
	b.page = attached.SessionID
	for _, m := range []string{"Page.enable", "Runtime.enable", "Log.enable"} {
		b.call(m, nil, b.page, nil)
	}
	b.call("Target.setAutoAttach", map[string]any{"autoAttach": true, "waitForDebuggerOnStart": false, "flatten": true}, b.page, nil)
	return b
}

func (b *browser) read() {
	for {
		_, data, err := b.conn.Read(b.ctx)
		if err != nil {
			return
		}
		var m struct {
			ID     int64           `json:"id"`
			Method string          `json:"method"`
			Params json.RawMessage `json:"params"`
			cdpReply
		}
		if json.Unmarshal(data, &m) != nil {
			continue
		}
		b.mu.Lock()
		if ch, ok := b.waiting[m.ID]; ok && m.ID != 0 {
			delete(b.waiting, m.ID)
			b.mu.Unlock()
			ch <- m.cdpReply
			continue
		}
		switch m.Method {
		case "Target.attachedToTarget":
			var p struct {
				SessionID  string `json:"sessionId"`
				TargetInfo struct {
					Type string `json:"type"`
				} `json:"targetInfo"`
			}
			_ = json.Unmarshal(m.Params, &p)
			if p.TargetInfo.Type == "iframe" {
				b.children = append(b.children, p.SessionID)
				go func() {
					b.send("Runtime.enable", nil, p.SessionID)
					b.send("Log.enable", nil, p.SessionID)
				}()
			}
		case "Runtime.executionContextCreated":
			var p struct{ Context cdpContext }
			_ = json.Unmarshal(m.Params, &p)
			b.contexts = append(b.contexts, p.Context)
		case "Runtime.exceptionThrown":
			b.noise = append(b.noise, string(m.Params))
		case "Log.entryAdded":
			var p struct {
				Entry struct{ Level, Text string }
			}
			_ = json.Unmarshal(m.Params, &p)
			if p.Entry.Level == "error" || p.Entry.Level == "warning" {
				b.noise = append(b.noise, p.Entry.Text)
			}
		}
		b.mu.Unlock()
	}
}

// send sends one command and waits for its answer.
func (b *browser) send(method string, params any, session string) (json.RawMessage, error) {
	b.mu.Lock()
	b.next++
	id := b.next
	ch := make(chan cdpReply, 1)
	b.waiting[id] = ch
	b.mu.Unlock()
	msg := map[string]any{"id": id, "method": method, "params": params}
	if params == nil {
		msg["params"] = map[string]any{}
	}
	if session != "" {
		msg["sessionId"] = session
	}
	raw, _ := json.Marshal(msg)
	if err := b.conn.Write(b.ctx, websocket.MessageText, raw); err != nil {
		return nil, err
	}
	select {
	case r := <-ch:
		if r.Error != nil {
			return nil, fmt.Errorf("%s: %s", method, r.Error.Message)
		}
		return r.Result, nil
	case <-time.After(30 * time.Second):
		return nil, fmt.Errorf("%s: no answer", method)
	}
}

func (b *browser) call(method string, params any, session string, out any) {
	b.t.Helper()
	raw, err := b.send(method, params, session)
	if err != nil {
		b.t.Fatal(err)
	}
	if out != nil {
		if err := json.Unmarshal(raw, out); err != nil {
			b.t.Fatalf("%s: %v", method, err)
		}
	}
}

// eval runs an expression and decodes its value into out.
func (b *browser) eval(expr string, contextID int, session string, out any) error {
	params := map[string]any{"expression": expr, "returnByValue": true, "awaitPromise": true}
	if contextID != 0 {
		params["contextId"] = contextID
	}
	raw, err := b.send("Runtime.evaluate", params, session)
	if err != nil {
		return err
	}
	var r struct {
		Result struct {
			Value json.RawMessage `json:"value"`
		} `json:"result"`
		ExceptionDetails *struct {
			Text      string
			Exception struct{ Description string }
		} `json:"exceptionDetails"`
	}
	if err := json.Unmarshal(raw, &r); err != nil {
		return err
	}
	if r.ExceptionDetails != nil {
		return fmt.Errorf("%s %s", r.ExceptionDetails.Text, r.ExceptionDetails.Exception.Description)
	}
	if out == nil || len(r.Result.Value) == 0 {
		return nil
	}
	return json.Unmarshal(r.Result.Value, out)
}

// top evaluates in the host's own page.
func (b *browser) top(expr string, out any) {
	b.t.Helper()
	if err := b.eval(expr, 0, b.page, out); err != nil {
		b.t.Fatalf("%s: %v", expr, err)
	}
}

// frame evaluates inside the box: in the context of the page's frame, or in
// the frame's own session when the browser put it in a process of its own.
func (b *browser) frame(expr string, out any) error {
	var tree struct {
		FrameTree struct {
			ChildFrames []struct {
				Frame struct{ ID string } `json:"frame"`
			} `json:"childFrames"`
		} `json:"frameTree"`
	}
	raw, err := b.send("Page.getFrameTree", nil, b.page)
	if err != nil {
		return err
	}
	_ = json.Unmarshal(raw, &tree)
	b.mu.Lock()
	ctx, child := 0, ""
	if len(tree.FrameTree.ChildFrames) > 0 {
		id := tree.FrameTree.ChildFrames[0].Frame.ID
		for i := len(b.contexts) - 1; i >= 0; i-- {
			if c := b.contexts[i]; c.AuxData.FrameID == id && c.AuxData.IsDefault {
				ctx = c.ID
				break
			}
		}
	}
	if ctx == 0 && len(b.children) > 0 {
		child = b.children[len(b.children)-1]
	}
	b.mu.Unlock()
	if ctx != 0 {
		return b.eval(expr, ctx, b.page, out)
	}
	if child == "" {
		return fmt.Errorf("no frame to evaluate in")
	}
	return b.eval(expr, 0, child, out)
}

func (b *browser) in(expr string, out any) {
	b.t.Helper()
	if err := b.frame(expr, out); err != nil {
		b.t.Fatalf("%s: %v", expr, err)
	}
}

func (b *browser) inBool(expr string) bool {
	b.t.Helper()
	var v bool
	b.in(expr, &v)
	return v
}

func (b *browser) inNum(expr string) float64 {
	b.t.Helper()
	var v float64
	b.in(expr, &v)
	return v
}

func (b *browser) inStr(expr string) string {
	b.t.Helper()
	var v string
	b.in(expr, &v)
	return v
}

// until waits for a condition, failing the test if it never holds.
func (b *browser) until(what string, ok func() bool) {
	b.t.Helper()
	for i := 0; i < 200; i++ {
		if func() (held bool) {
			defer func() { _ = recover() }()
			return ok()
		}() {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	b.t.Fatalf("timed out: %s", what)
}

// tryBool is a check that may run before the frame is there.
func (b *browser) tryBool(expr string) bool {
	var v bool
	return b.frame(expr, &v) == nil && v
}

func (b *browser) open(h *appHost, video string) {
	b.t.Helper()
	b.mu.Lock()
	b.contexts, b.children = nil, nil
	b.mu.Unlock()
	b.call("Page.navigate", map[string]any{"url": "about:blank"}, b.page, nil)
	time.Sleep(50 * time.Millisecond)
	b.call("Page.navigate", map[string]any{"url": h.srv.URL + "/?video=" + video}, b.page, nil)
	b.until("the page drawn", func() bool { return b.tryBool(`document.querySelectorAll('.slot').length > 0`) })
}

func (b *browser) viewport(width, height int, mobile bool) {
	scale := 1
	if mobile {
		scale = 2
	}
	b.call("Emulation.setDeviceMetricsOverride", map[string]any{"width": width, "height": height, "deviceScaleFactor": scale, "mobile": mobile}, b.page, nil)
	b.call("Emulation.setTouchEmulationEnabled", map[string]any{"enabled": mobile}, b.page, nil)
	time.Sleep(150 * time.Millisecond)
}

type point struct{ X, Y float64 }

// at is a point in the top page for what a selector finds in the box, after
// bringing it into view; across is how far along it, from its left.
func (b *browser) at(selector string, across float64) point {
	b.t.Helper()
	sel, _ := json.Marshal(selector)
	b.in(fmt.Sprintf(`document.querySelectorAll(%s)[0].scrollIntoView({block:'center'})`, sel), nil)
	time.Sleep(40 * time.Millisecond)
	var box, r struct{ X, Y, W, H float64 }
	b.top(`(() => { const b = document.querySelector('iframe').getBoundingClientRect(); return {X:b.x,Y:b.y}; })()`, &box)
	b.in(fmt.Sprintf(`(() => { const b = document.querySelectorAll(%s)[0].getBoundingClientRect(); return {X:b.x,Y:b.y,W:b.width,H:b.height}; })()`, sel), &r)
	return point{box.X + r.X + r.W*across, box.Y + r.Y + r.H/2}
}

func (b *browser) mouse(kind string, p point) {
	buttons := 1
	if kind == "mouseReleased" || kind == "mouseMoved" {
		buttons = 0
	}
	b.call("Input.dispatchMouseEvent", map[string]any{"type": kind, "x": p.X, "y": p.Y, "button": "left", "buttons": buttons, "clickCount": 1}, b.page, nil)
}

// press moves the mouse with its button down.
func (b *browser) press(kind string, p point) {
	b.call("Input.dispatchMouseEvent", map[string]any{"type": kind, "x": p.X, "y": p.Y, "button": "left", "buttons": 1, "clickCount": 1}, b.page, nil)
}

func (b *browser) click(selector string) {
	b.t.Helper()
	p := b.at(selector, 0.5)
	b.mouse("mouseMoved", p)
	b.press("mousePressed", p)
	b.mouse("mouseReleased", p)
	time.Sleep(30 * time.Millisecond)
}

func (b *browser) insert(text string) {
	b.call("Input.insertText", map[string]any{"text": text}, b.page, nil)
}

func (b *browser) replaceText(selector, text string) {
	b.t.Helper()
	b.click(selector)
	b.in(`document.activeElement.select()`, nil)
	b.insert(text)
}

func (b *browser) key(name string) {
	code := map[string]int{"Tab": 9, "Enter": 13, "ArrowUp": 38, "ArrowDown": 40}[name]
	for _, kind := range []string{"keyDown", "keyUp"} {
		b.call("Input.dispatchKeyEvent", map[string]any{"type": kind, "key": name, "code": name, "windowsVirtualKeyCode": code}, b.page, nil)
	}
	time.Sleep(30 * time.Millisecond)
}

// drag holds a handle, moves it by so many pixels, and lets go.
func (b *browser) drag(selector string, by float64, hold time.Duration) {
	b.t.Helper()
	from := b.at(selector, 0.5)
	var bottom float64
	b.top(`document.querySelector('iframe').getBoundingClientRect().bottom - 5`, &bottom)
	to := point{from.X, min(from.Y+by, bottom)}
	b.mouse("mouseMoved", from)
	b.press("mousePressed", from)
	b.press("mouseMoved", to)
	time.Sleep(40*time.Millisecond + hold)
	b.mouse("mouseReleased", to)
	time.Sleep(40 * time.Millisecond)
}

func (b *browser) touch(kind string, p point) {
	points := []map[string]any{{"x": p.X, "y": p.Y}}
	if kind == "touchEnd" {
		points = []map[string]any{}
	}
	b.call("Input.dispatchTouchEvent", map[string]any{"type": kind, "touchPoints": points}, b.page, nil)
}

// shot saves a picture of the screen where REVIEW_SHOTS names a folder, for a
// person to look at; otherwise it does nothing.
func (b *browser) shot(name string) {
	dir := os.Getenv("REVIEW_SHOTS")
	if dir == "" {
		return
	}
	var r struct{ Data []byte }
	b.call("Page.captureScreenshot", map[string]any{"format": "png"}, b.page, &r)
	_ = os.WriteFile(filepath.Join(dir, name+".png"), r.Data, 0o644)
}
