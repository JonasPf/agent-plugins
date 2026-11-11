package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"syscall"
	"time"

	"github.com/JonasPf/agent-plugins/internal/serve"
)

type args struct {
	Command        string `json:"command"`
	TimeoutSeconds int    `json:"timeout_seconds"`
}

const maxOutput = 20000

// defaultTimeout is what a command gets when it asks for nothing, and
// maxTimeout the most it may ask for. The most is long enough to wait for
// something in one call: held to two minutes, a session that started a long
// script asked whether it had finished 265 times, a model call each time.
const (
	defaultTimeout = 120
	maxTimeout     = 900
)

// Allowed is the timeout a command runs under. Asking for more than the most
// is held to it rather than refused, so the overrun is still reported by the
// tool and not ended by the host's kill.
func Allowed(requested int) int {
	switch {
	case requested <= 0:
		return defaultTimeout
	case requested > maxTimeout:
		return maxTimeout
	default:
		return requested
	}
}

// backgroundGrace is how long output is still read after the shell exits, for
// what a process left running in the background writes on its way out.
const backgroundGrace = 2 * time.Second

// Clip caps a body and reports how much it dropped. An unannounced truncation
// is worse than a short answer: the model reasons from what it was handed as
// though that were all there was, and nothing in the result says otherwise.
func Clip(body string, max int) (string, int) {
	if len(body) <= max {
		return body, 0
	}
	return body[:max], len(body) - max
}

// bash runs a shell command in the working directory. It is a shell, so it
// does what it is told; the boundary around it is the host's.
func bash(parent context.Context, raw json.RawMessage) (string, error) {
	var a args
	if err := serve.Args(raw, &a); err != nil {
		return "", err
	}
	if strings.TrimSpace(a.Command) == "" {
		return "", errors.New("command is required")
	}
	timeout := Allowed(a.TimeoutSeconds)
	ctx, cancel := context.WithTimeout(parent, time.Duration(timeout)*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "/bin/sh", "-c", a.Command)
	// The command runs in a process group of its own, so a timeout ends
	// everything it started rather than the shell alone, which left its children
	// holding the output open and the call waiting on them.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }
	// A command that leaves something running in the background — a server, a
	// watcher — is finished when its shell is. Whatever it left running keeps
	// the output open, so the output is read for a moment longer and then the
	// call returns rather than waiting out its timeout on a process nobody asked
	// it to wait for. What it left running carries on.
	cmd.WaitDelay = backgroundGrace
	out, err := cmd.CombinedOutput()
	if errors.Is(err, exec.ErrWaitDelay) {
		err = nil
	}
	if ctx.Err() == context.DeadlineExceeded {
		return "", fmt.Errorf("timed out after %ds", timeout)
	}
	body, dropped := Clip(string(out), maxOutput)
	if dropped > 0 {
		body += fmt.Sprintf("\n\n[output truncated: %d of %d characters shown, %d dropped]",
			maxOutput, maxOutput+dropped, dropped)
	}
	if err != nil {
		return "", fmt.Errorf("%v\n%s", err, body)
	}
	if body == "" {
		body = "(no output)"
	}
	return body, nil
}
