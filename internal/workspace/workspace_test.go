package workspace

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The workspace boundary is the operating system's; this is the message a tool
// gives instead of letting the kernel say it. It has to agree with the kernel,
// or a tool reports a refusal for a path that would have worked.
func TestResolveInside(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	// On macOS the temporary directory is reached through a symlink, and the
	// resolved path is what the sandbox matches on, so it is what the assertion
	// below has to compare against.
	if real, err := filepath.EvalSymlinks(root); err == nil {
		root = real
	}
	for _, c := range []struct {
		name, path string
		wantErr    bool
	}{
		{"a relative path", "notes.txt", false},
		{"a nested path", "sub/notes.txt", false},
		{"a path that does not exist yet", "sub/deeper/new.txt", false},
		{"the root itself", ".", false},
		{"an absolute path inside", filepath.Join(root, "notes.txt"), false},
		{"an empty path", "", true},
		{"a parent escape", "../outside.txt", true},
		{"a nested parent escape", "sub/../../outside.txt", true},
		{"an absolute path outside", "/etc/passwd", true},
	} {
		t.Run(c.name, func(t *testing.T) {
			got, err := ResolveInside(root, c.path)
			if c.wantErr {
				if err == nil {
					t.Errorf("resolved %q to %q, want a refusal", c.path, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("refused %q: %v", c.path, err)
			}
			if !strings.HasPrefix(got, root) {
				t.Errorf("resolved %q to %q, which is outside %q", c.path, got, root)
			}
		})
	}
}
