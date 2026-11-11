package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/JonasPf/agent-plugins/internal/serve"
	"github.com/JonasPf/agent-plugins/internal/workspace"
)

type readArgs struct {
	Path   string `json:"path"`
	Offset int    `json:"offset"`
	Limit  int    `json:"limit"`
}

// Window applies offset and limit to a file's lines. Both are optional and
// either may be past the end, which is an empty answer rather than an error:
// reading past the end of a file is a fact about the file.
func Window(lines []string, offset, limit int) []string {
	if offset > 0 {
		if offset >= len(lines) {
			return nil
		}
		lines = lines[offset:]
	}
	if limit > 0 && limit < len(lines) {
		lines = lines[:limit]
	}
	return lines
}

// skillFile names the skill_read call for a path in the folder of a skill this
// conversation has, or returns "". A skill's scripts run from its folder, so the
// agent sees that path and reaches for read; told only that the path was
// outside the workspace, it found skill_read on its next call, in conversation
// after conversation. A host that has skills says where their folders are
// with each call, in AGENT_SKILL_DIRS: skill name to folder. Without it there
// is no hint.
func skillFile(path string) string {
	var dirs map[string]string
	if json.Unmarshal([]byte(os.Getenv("AGENT_SKILL_DIRS")), &dirs) != nil {
		return ""
	}
	full := filepath.Clean(path)
	if !filepath.IsAbs(full) {
		full = filepath.Join(workspace.Dir(), full)
	}
	if real, err := filepath.EvalSymlinks(full); err == nil {
		full = real
	}
	for name, dir := range dirs {
		if real, err := filepath.EvalSymlinks(dir); err == nil {
			dir = real
		}
		if rel, err := filepath.Rel(dir, full); err == nil && rel != "." && !strings.HasPrefix(rel, "..") {
			return fmt.Sprintf("it is a file of the skill %s: read it with skill_read {\"name\": %q, \"file\": %q}",
				name, name, filepath.ToSlash(rel))
		}
	}
	return ""
}

// read returns the contents of a file in the working directory.
func read(_ context.Context, raw json.RawMessage) (string, error) {
	var a readArgs
	if err := serve.Args(raw, &a); err != nil {
		return "", err
	}
	path, err := workspace.Inside(a.Path)
	if err != nil {
		if strings.TrimSpace(a.Path) != "" {
			if hint := skillFile(a.Path); hint != "" {
				return "", fmt.Errorf("%s; %s", err, hint)
			}
		}
		return "", err
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return strings.Join(Window(strings.Split(string(b), "\n"), a.Offset, a.Limit), "\n"), nil
}
