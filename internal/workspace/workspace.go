// Package workspace is the boundary the file tools keep to: the directory the
// server was started in, which is the conversation's own.
package workspace

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Dir is the working directory. It is asked for on each call rather than kept,
// so a host that keeps a server running sees where it is now.
func Dir() string {
	d, err := os.Getwd()
	if err != nil {
		return "."
	}
	return d
}

// Inside resolves a path against the working directory.
func Inside(path string) (string, error) { return ResolveInside(Dir(), path) }

// ResolveInside resolves a path and refuses anything outside root. Where the
// host confines the server, the operating system refuses it too — this is the
// error message, not the boundary.
func ResolveInside(root, path string) (string, error) {
	if strings.TrimSpace(path) == "" {
		return "", fmt.Errorf("path is required")
	}
	root, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	if real, err := filepath.EvalSymlinks(root); err == nil {
		root = real
	}
	full := path
	if !filepath.IsAbs(full) {
		full = filepath.Join(root, full)
	}
	full = filepath.Clean(full)
	// A path that does not exist yet cannot be resolved through its symlinks,
	// so the deepest existing parent is resolved and the rest re-joined.
	if real, err := filepath.EvalSymlinks(full); err == nil {
		full = real
	} else if dir, err := filepath.EvalSymlinks(filepath.Dir(full)); err == nil {
		full = filepath.Join(dir, filepath.Base(full))
	}
	if full != root && !strings.HasPrefix(full, root+string(filepath.Separator)) {
		return "", fmt.Errorf("path %q is outside the workspace", path)
	}
	return full, nil
}
