package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/JonasPf/agent-plugins/internal/serve"
	"github.com/JonasPf/agent-plugins/internal/workspace"
)

type writeArgs struct {
	Path    string `json:"path"`
	Content string `json:"content"`
}

// write creates or replaces a file in the working directory.
func write(_ context.Context, raw json.RawMessage) (string, error) {
	var a writeArgs
	if err := serve.Args(raw, &a); err != nil {
		return "", err
	}
	path, err := workspace.Inside(a.Path)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", err
	}
	if err := os.WriteFile(path, []byte(a.Content), 0o644); err != nil {
		return "", err
	}
	return fmt.Sprintf("wrote %s (%d bytes)", a.Path, len(a.Content)), nil
}
