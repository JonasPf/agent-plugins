package web

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// Most of the web arrives empty: the HTML a server sends is a shell, and the
// text a person reads is written into it by scripts once the page is running.
// Reading it therefore means running it, which means a browser.

// BrowserPath is the browser, and there is only one. The image installs
// chromium from Debian and CI installs the same package, so the path is the
// same everywhere the browser tools are expected to work. It sits under /usr,
// which the sandbox already grants every tool, so nothing has to be told about
// it — no override variable, no cache to scan, no candidate list, and no way
// for a machine to be subtly different from the one the tests ran on.
//
// A laptop is not one of those places. Off Linux there is no sandbox either
// (ADR-039), and the answer to both is the same: run the container.
const BrowserPath = "/usr/bin/chromium"

// lookupBrowser is BrowserPath checked for existence. exists is a parameter so
// the missing case can be tested on a machine where the browser is present.
func lookupBrowser(exists func(string) bool) (string, error) {
	if !exists(BrowserPath) {
		return "", fmt.Errorf("no browser at %s. The image installs chromium there; "+
			"an image without it is broken rather than degraded", BrowserPath)
	}
	return BrowserPath, nil
}

// Exists reports whether a path is there.
func Exists(p string) bool { _, err := os.Stat(p); return err == nil }

// Browser resolves the one browser, or says why it cannot.
func Browser() (string, error) { return lookupBrowser(Exists) }

// lastLines is the tail of a stream, for an error message that has to fit in a
// tool result: what a program said just before it stopped is the useful part.
func lastLines(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return "…" + s[len(s)-n:]
}

// chromeVersion is the four-part version in a browser's --version banner.
var chromeVersion = regexp.MustCompile(`\b(\d+)\.\d+\.\d+\.\d+\b`)

// userAgentFor is the user agent of an ordinary desktop Chrome at the version a
// banner names, or nothing if it names none. The shape is the one Chrome sends
// since it froze the string: the Linux platform is fixed and everything after
// the major version is zero, so the only part that varies is the part that has
// to agree with the browser actually running.
func userAgentFor(banner string) string {
	m := chromeVersion.FindStringSubmatch(banner)
	if m == nil {
		return ""
	}
	return "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/" +
		m[1] + ".0.0.0 Safari/537.36"
}

// userAgent asks the browser its version. A browser that cannot say keeps its
// own string rather than one made up for it.
func userAgent(browser string, env []string) string {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, browser, "--version")
	cmd.Env = env
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return userAgentFor(string(out))
}

// Render returns the DOM of url after the page has loaded and run its scripts.
//
// The browser introduces itself as the Chrome it is, without the "Headless"
// that headless chromium adds to its user agent (ADR-046). Nothing else is
// altered: no bot check is answered or evaded, and a site that declines to
// serve the page is not worked around.
func Render(url string, seconds int) (string, error) {
	browser, err := Browser()
	if err != nil {
		return "", err
	}
	// A browser insists on a profile directory it can write. The system one is
	// outside what a tool may touch, so it goes in the temporary directory the
	// host gave, which a confining host puts inside the conversation's own.
	profile := filepath.Join(os.TempDir(), "browser-profile")
	if err := os.MkdirAll(profile, 0o755); err != nil {
		return "", fmt.Errorf("could not make a browser profile directory: %v", err)
	}
	// Two flags that only matter inside the sandbox, which is where this always
	// runs. A browser puts its shared memory in /dev/shm, and the sandbox gives
	// a minimal /dev with no shm in it — binding the host's would be a writable
	// channel between sessions, so the browser is told to keep that memory in
	// its own directory instead. Chrome's own sandbox is off for the same reason
	// bubblewrap is on: one confinement, enforced by the agent.
	args := []string{"--headless", "--disable-gpu", "--no-sandbox",
		"--disable-dev-shm-usage",
		// A browser started with a fresh profile wants to fetch components,
		// check for updates, and sync before it will settle — none of which the
		// tool asked for. It hung for 35 seconds on "failed to update on-device
		// model component" until this was turned off. Fetching a URL should make
		// the requests that URL needs and no others.
		"--disable-background-networking", "--disable-component-update",
		"--disable-sync", "--no-first-run", "--no-default-browser-check",
		"--user-data-dir=" + profile}
	// HOME is the agent's, and the sandbox does not bind it: a browser sent
	// there is being pointed at a directory that does not exist in its own
	// namespace. Its profile directory is somewhere it can actually write.
	env := append(os.Environ(), "HOME="+profile)
	if ua := userAgent(browser, env); ua != "" {
		args = append(args, "--user-agent="+ua)
	}
	args = append(args, fmt.Sprintf("--virtual-time-budget=%d", seconds*1000), "--dump-dom", url)
	cmd := exec.Command(browser, args...)
	cmd.Env = env
	var out, errb strings.Builder
	cmd.Stdout, cmd.Stderr = &out, &errb
	done := make(chan error, 1)
	if err := cmd.Start(); err != nil {
		return "", fmt.Errorf("could not start %s: %v", browser, err)
	}
	go func() { done <- cmd.Wait() }()
	select {
	case <-done:
	case <-time.After(time.Duration(seconds+20) * time.Second):
		_ = cmd.Process.Kill()
		// Whatever the browser said before it hung is the only evidence there
		// is; a bare timeout leaves nothing to act on.
		return "", fmt.Errorf("%s did not finish loading within %ds: %s", url, seconds+20,
			strings.TrimSpace(lastLines(errb.String(), 400)))
	}
	page := out.String()
	if strings.TrimSpace(page) == "" {
		return "", fmt.Errorf("%s rendered nothing (%s)", url, truncate(strings.TrimSpace(errb.String()), 200))
	}
	return page, nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}
