package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// A project is one video and the folder beside it that holds everything made
// for its voice-over: clip.mov has clip.voiceover/. Every call names its video,
// and that is all a call needs to find its project, so nothing is kept in
// memory between calls: some hosts start the server anew for each one.
type project struct {
	video string // absolute
	dir   string // absolute
}

func openProject(video string) (project, error) {
	if strings.TrimSpace(video) == "" {
		return project{}, fmt.Errorf("video is required: the path of the video this is for")
	}
	full, err := filepath.Abs(video)
	if err != nil {
		return project{}, err
	}
	info, err := os.Stat(full)
	if err != nil || info.IsDir() {
		return project{}, fmt.Errorf("%s is not a file in %s: name the video by its path", video, here())
	}
	stem := strings.TrimSuffix(full, filepath.Ext(full))
	return project{video: full, dir: stem + ".voiceover"}, nil
}

// folder makes one of the project's folders and returns its path.
func (p project) folder(name string) (string, error) {
	dir := filepath.Join(p.dir, name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("could not make %s: %v", shown(dir), err)
	}
	return dir, nil
}

func here() string {
	d, err := os.Getwd()
	if err != nil {
		return "."
	}
	return d
}

// shown is a path as a result names it: relative to the working directory
// when it is inside it, which is how the model's other tools name files.
func shown(path string) string {
	if rel, err := filepath.Rel(here(), path); err == nil && !strings.HasPrefix(rel, "..") {
		return rel
	}
	return path
}
