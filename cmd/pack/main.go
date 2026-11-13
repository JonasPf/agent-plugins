// Command pack builds one bundle for one platform and writes it as an MCP
// Bundle: a zip holding manifest.json, the server it names, and each tool's
// eval cases under evals/.
//
//	go run ./cmd/pack -bundle clock -os linux -arch arm64 -out dist
//
// With -skill it packs a skill instead: a zip of the files in its folder,
// SKILL.md at the top, named <skill>-<version>.zip.
//
//	go run ./cmd/pack -skill researching -version 0.1.0 -out dist
//
// With -bin DIR the bundle carries programs of its own beside its server: the
// files in DIR/<os>-<arch>/ go under bin/ in the bundle for that platform, as
// voice-over carries its ffmpeg (ffmpeg/build.sh makes them).
//
//	go run ./cmd/pack -bundle voice-over -bin dist/ffmpeg -out dist
//
// The file is <bundle>-<version>-<os>-<arch>.mcpb. Its manifest is the one in
// the repository with compatibility.platforms set to the platform it was built
// for, because the server inside runs on no other.
package main

import (
	"archive/zip"
	"encoding/json"
	"flag"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
)

// Platforms is what a release is built for.
var Platforms = [][2]string{{"linux", "amd64"}, {"linux", "arm64"}, {"darwin", "arm64"}}

func main() {
	bundle := flag.String("bundle", "", "the bundle's directory name under bundles/")
	skill := flag.String("skill", "", "instead of a bundle: the skill's directory name under skills/")
	version := flag.String("version", "", "with -skill: the version the archive is named for")
	goos := flag.String("os", "", "linux or darwin; empty builds every platform the bundle runs on")
	goarch := flag.String("arch", "", "amd64 or arm64")
	out := flag.String("out", "dist", "where the .mcpb files go")
	bin := flag.String("bin", "", "a folder with one folder per platform, <os>-<arch>, of programs the bundle carries under bin/")
	flag.Parse()
	if *skill != "" {
		path, err := PackSkill(".", *skill, *version, *out)
		if err != nil {
			fmt.Fprintln(os.Stderr, "pack:", err)
			os.Exit(1)
		}
		fmt.Println(path)
		return
	}
	if *bundle == "" {
		fmt.Fprintln(os.Stderr, "pack: -bundle is required")
		os.Exit(2)
	}
	targets := Platforms
	if *goos != "" {
		targets = [][2]string{{*goos, *goarch}}
	}
	for _, t := range targets {
		path, err := Pack(".", *bundle, t[0], t[1], *out, *bin)
		if err != nil {
			fmt.Fprintln(os.Stderr, "pack:", err)
			os.Exit(1)
		}
		if path != "" {
			fmt.Println(path)
		}
	}
}

// Pack builds bundle for one platform and returns the file it wrote, or ""
// when the bundle's manifest says it does not run there. bin, if not empty,
// holds the programs it carries, one folder per platform.
func Pack(root, bundle, goos, goarch, out, bin string) (string, error) {
	dir := filepath.Join(root, "bundles", bundle)
	raw, err := os.ReadFile(filepath.Join(dir, "manifest.json"))
	if err != nil {
		return "", err
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		return "", fmt.Errorf("%s: manifest.json: %w", bundle, err)
	}
	version, _ := m["version"].(string)
	if version == "" {
		return "", fmt.Errorf("%s: manifest.json names no version", bundle)
	}
	compat, _ := m["compatibility"].(map[string]any)
	if compat == nil {
		compat = map[string]any{}
	}
	if only, ok := compat["platforms"].([]any); ok && !slices.Contains(only, any(goos)) {
		return "", nil
	}
	compat["platforms"] = []string{goos}
	m["compatibility"] = compat
	manifest, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return "", err
	}

	tmp, err := os.MkdirTemp("", "pack")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(tmp)
	server := filepath.Join(tmp, "server")
	abs, err := filepath.Abs(server)
	if err != nil {
		return "", err
	}
	build := exec.Command("go", "build", "-trimpath", "-ldflags", "-s -w", "-o", abs, "./bundles/"+bundle)
	build.Dir = root
	build.Env = append(os.Environ(), "GOOS="+goos, "GOARCH="+goarch, "CGO_ENABLED=0")
	if b, err := build.CombinedOutput(); err != nil {
		return "", fmt.Errorf("%s: build for %s/%s: %v\n%s", bundle, goos, goarch, err, b)
	}
	binary, err := os.ReadFile(server)
	if err != nil {
		return "", err
	}

	if err := os.MkdirAll(out, 0o755); err != nil {
		return "", err
	}
	path := filepath.Join(out, fmt.Sprintf("%s-%s-%s-%s.mcpb", bundle, version, goos, goarch))
	f, err := os.Create(path)
	if err != nil {
		return "", err
	}
	type entry struct {
		name string
		mode os.FileMode
		body []byte
	}
	entries := []entry{{"manifest.json", 0o644, append(manifest, '\n')}, {"server", 0o755, binary}}
	// The eval cases go with the bundle: they test its descriptions against a
	// model, and only a host can put them to one.
	evals, err := filepath.Glob(filepath.Join(dir, "evals", "*.json"))
	if err != nil {
		return "", err
	}
	for _, e := range evals {
		body, err := os.ReadFile(e)
		if err != nil {
			return "", err
		}
		entries = append(entries, entry{"evals/" + filepath.Base(e), 0o644, body})
	}
	if bin != "" {
		// A bundle given programs to carry is never packed without them: it
		// would install, and fail at its first call.
		here := filepath.Join(bin, goos+"-"+goarch)
		files, err := os.ReadDir(here)
		if err != nil || len(files) == 0 {
			return "", fmt.Errorf("%s: no programs for %s-%s in %s", bundle, goos, goarch, bin)
		}
		for _, file := range files {
			if file.IsDir() {
				continue
			}
			info, err := file.Info()
			if err != nil {
				return "", err
			}
			body, err := os.ReadFile(filepath.Join(here, file.Name()))
			if err != nil {
				return "", err
			}
			mode := os.FileMode(0o644)
			if info.Mode()&0o111 != 0 {
				mode = 0o755
			}
			entries = append(entries, entry{"bin/" + file.Name(), mode, body})
		}
	}
	z := zip.NewWriter(f)
	for _, file := range entries {
		h := &zip.FileHeader{Name: file.name, Method: zip.Deflate}
		h.SetMode(file.mode)
		w, err := z.CreateHeader(h)
		if err != nil {
			return "", err
		}
		if _, err := w.Write(file.body); err != nil {
			return "", err
		}
	}
	if err := z.Close(); err != nil {
		return "", err
	}
	return path, f.Close()
}

// PackSkill zips the files of skills/<name> and returns the file it wrote.
// A skill is text and runs anywhere, so there is one archive, not one per
// platform. Its eval cases are left out: they are for whoever changes it.
func PackSkill(root, name, version, out string) (string, error) {
	dir := filepath.Join(root, "skills", name)
	if _, err := os.Stat(filepath.Join(dir, "SKILL.md")); err != nil {
		return "", err
	}
	if version == "" {
		return "", fmt.Errorf("%s: a skill is packed for a version", name)
	}
	if err := os.MkdirAll(out, 0o755); err != nil {
		return "", err
	}
	path := filepath.Join(out, fmt.Sprintf("%s-%s.zip", name, version))
	f, err := os.Create(path)
	if err != nil {
		return "", err
	}
	z := zip.NewWriter(f)
	err = filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(dir, p)
		if d.IsDir() {
			if rel == "evals" {
				return filepath.SkipDir
			}
			return nil
		}
		body, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		h := &zip.FileHeader{Name: filepath.ToSlash(rel), Method: zip.Deflate}
		h.SetMode(0o644)
		w, err := z.CreateHeader(h)
		if err != nil {
			return err
		}
		_, err = w.Write(body)
		return err
	})
	if err != nil {
		return "", err
	}
	if err := z.Close(); err != nil {
		return "", err
	}
	return path, f.Close()
}
