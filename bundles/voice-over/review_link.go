package main

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/JonasPf/agent-plugins/internal/serve"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// The review page for a host that shows no pages, such as a coding agent in
// a terminal: the bundle's own server serves it on this machine, and the tool
// gives the model a link to pass on. It is the same page; only how it reaches
// its two tools differs. A host that shows pages never starts this.

// reviewName is the setting that says which way the page is shown: auto (as
// the host can), apps, or browser.
const reviewName = "VOICEOVER_REVIEW"

// reviewIdle is how long a link waits for its page before it stops.
var reviewIdle = 2 * time.Hour

// linkPolicy is what the page may do in a browser: what is in its own text,
// and calls to the server that served it.
const linkPolicy = "default-src 'none'; script-src 'unsafe-inline'; style-src 'unsafe-inline'; " +
	"img-src data: blob:; media-src data: blob:; font-src data:; connect-src 'self'; " +
	"object-src 'none'; form-action 'none'; base-uri 'none'; frame-ancestors 'none'"

// reviewLink is the one page served at a time.
type link struct {
	srv  *http.Server
	once sync.Once
	// idle is the timer that stops a link left alone; mu orders the timer's
	// own goroutine after the one that set it.
	mu   sync.Mutex
	idle *time.Timer
}

// touch puts off the link's stopping for another reviewIdle.
func (l *link) touch() {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.idle != nil {
		l.idle.Reset(reviewIdle)
	}
}

var (
	linkMu  sync.Mutex
	current *link
)

// showsAsLink is whether this call's page is a link rather than an App.
func showsAsLink(ctx context.Context) (bool, error) {
	switch setting(reviewName) {
	case "", "auto":
		return !serve.HostShowsApps(ctx), nil
	case "apps":
		return false, nil
	case "browser":
		return true, nil
	}
	return false, fmt.Errorf("the bundle's review setting is %q: it is auto, apps or browser", setting(reviewName))
}

// openReviewLink serves the page for one video and returns its link. A link
// already open stops: one page at a time.
func openReviewLink(video string) (string, error) {
	closeReviewLink()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return "", fmt.Errorf("could not open the page on this machine: %v", err)
	}
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		ln.Close()
		return "", err
	}
	token := hex.EncodeToString(b)
	host := ln.Addr().String()
	l := &link{}
	cfg, _ := json.Marshal(map[string]string{"video": video, "token": token})
	page := "<script>window.REVIEW_LINK = " + strings.ReplaceAll(string(cfg), "<", `<`) + ";</script>\n" + reviewPage
	pageTools := map[string]serve.Run{"voiceover_review_data": reviewData, "voiceover_save_review": saveReview}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Content-Security-Policy", linkPolicy)
		_, _ = io.WriteString(w, page)
	})
	mux.HandleFunc("POST /call", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Name      string          `json:"name"`
			Arguments json.RawMessage `json:"arguments"`
		}
		if err := json.NewDecoder(io.LimitReader(r.Body, 16<<20)).Decode(&in); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		run, ok := pageTools[in.Name]
		if !ok {
			http.Error(w, in.Name+" is not a tool this page may call", http.StatusForbidden)
			return
		}
		res := &mcp.CallToolResult{}
		text, err := run(r.Context(), in.Arguments)
		if err != nil {
			res.IsError, text = true, err.Error()
		}
		res.Content = []mcp.Content{&mcp.TextContent{Text: text}}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(res)
	})
	mux.HandleFunc("POST /sent", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, "{}")
		// Send was the page's last word: the link stops once this is answered.
		go func() {
			time.Sleep(100 * time.Millisecond)
			l.close()
		}()
	})
	l.srv = &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// The page's own address and token, or nothing: a name that resolves
		// here by trickery, or a page that does not hold the token, is refused.
		if r.Host != host || subtle.ConstantTimeCompare([]byte(r.URL.Query().Get("t")), []byte(token)) != 1 {
			http.Error(w, "this is a voice-over review page's address; ask the agent for its link", http.StatusForbidden)
			return
		}
		l.touch()
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		mux.ServeHTTP(w, r)
	}), ReadHeaderTimeout: 10 * time.Second}
	l.mu.Lock()
	l.idle = time.AfterFunc(reviewIdle, l.close)
	l.mu.Unlock()
	linkMu.Lock()
	current = l
	linkMu.Unlock()
	go func() { _ = l.srv.Serve(ln) }()
	return "http://" + host + "/?t=" + token, nil
}

func (l *link) close() {
	l.once.Do(func() {
		l.mu.Lock()
		l.idle.Stop()
		l.mu.Unlock()
		_ = l.srv.Close()
		linkMu.Lock()
		if current == l {
			current = nil
		}
		linkMu.Unlock()
	})
}

// closeReviewLink stops the page served now, if there is one.
func closeReviewLink() {
	linkMu.Lock()
	l := current
	linkMu.Unlock()
	if l != nil {
		l.close()
	}
}
