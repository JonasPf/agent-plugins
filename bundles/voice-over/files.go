package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// The files of a project other than the sound: what CONTRACT.md's table of
// the project folder lists, read and written in one place.

// scriptLine is a line of script.json: when it starts and what is said.
// script.json holds times only; words to find a start by are an input to
// voiceover_script, and once found they are a time.
type scriptLine struct {
	At   float64 `json:"at"`
	Text string  `json:"text"`
}

// choices is project.json: the voice the lines are spoken in, and a
// translation owed after the operator picked a language on the review page.
type choices struct {
	Voice       string `json:"voice,omitempty"`
	Model       string `json:"model,omitempty"`
	TranslateTo string `json:"translate_to,omitempty"`
}

func (p project) script() ([]scriptLine, error) {
	b, err := os.ReadFile(filepath.Join(p.dir, "script.json"))
	if errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("%s has no script yet: write its lines with voiceover_script first", shown(p.video))
	}
	if err != nil {
		return nil, err
	}
	var lines []scriptLine
	if err := json.Unmarshal(b, &lines); err != nil {
		return nil, fmt.Errorf("%s is not a script: %v. Write the lines again with voiceover_script", shown(filepath.Join(p.dir, "script.json")), err)
	}
	return lines, nil
}

func (p project) writeScript(lines []scriptLine) error {
	return p.writeJSON("script.json", lines)
}

func (p project) choices() (choices, error) {
	var c choices
	b, err := os.ReadFile(filepath.Join(p.dir, "project.json"))
	if errors.Is(err, os.ErrNotExist) {
		return c, nil
	}
	if err != nil {
		return c, err
	}
	if err := json.Unmarshal(b, &c); err != nil {
		return c, fmt.Errorf("%s cannot be read: %v", shown(filepath.Join(p.dir, "project.json")), err)
	}
	return c, nil
}

func (p project) writeChoices(c choices) error {
	return p.writeJSON("project.json", c)
}

func (p project) writeJSON(name string, v any) error {
	if err := os.MkdirAll(p.dir, 0o755); err != nil {
		return fmt.Errorf("could not make %s: %v", shown(p.dir), err)
	}
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(p.dir, name), append(b, '\n'), 0o644)
}

// speechOf is where a line's speech is kept, without its extension: named by
// the model, the voice and the words, so a line whose words did not change
// keeps its speech wherever it moves, and a new voice keeps none.
func (p project) speechOf(v Voice, text string) string {
	h := sha256.Sum256([]byte(v.Model + "\x00" + v.Name + "\x00" + text))
	return filepath.Join(p.dir, "speech", hex.EncodeToString(h[:8]))
}

// spoken is the speech already kept for a line, or "".
func (p project) spoken(v Voice, text string) string {
	base := p.speechOf(v, text)
	for _, ext := range []string{".mp3", ".wav"} {
		if _, err := os.Stat(base + ext); err == nil {
			return base + ext
		}
	}
	return ""
}
