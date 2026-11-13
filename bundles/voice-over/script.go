package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"unicode"

	"github.com/JonasPf/agent-plugins/internal/serve"
)

// voiceover_script finds where each line goes and writes script.json. It is
// place_chunks.py --check from the skill, ported: a line starts where the
// narrator said its first words, so no time is chosen by eye. In the run the
// skill was worked out from, 27 times picked by eye were up to 9.5 s wrong.

const (
	minWords = 3
	// fuzzy is how near a stretch of the transcript must be to the words
	// wanted, as difflib's ratio, to count as them.
	fuzzy = 0.8
	// wordsPerSecond is how fast the voices read: HeyGen 2.3 to 2.9,
	// ElevenLabs about 2.7. The slowest, so TIGHT warns early.
	wordsPerSecond = 2.3
	// maxChars: HeyGen refuses a call over 5,000 characters, after it has
	// been paid for.
	maxChars = 4500
)

// offeredModels are the speaking models a voice is chosen from: the cheap
// one, good enough for most videos, and the one for when quality matters
// more than price. Their voices' names do not overlap, so a voice's name is
// enough to choose it by. The gateway lists dozens more.
var offeredModels = []string{defaultSpeech, "elevenlabs/eleven-flash-v2.5"}

var segmentLine = regexp.MustCompile(`^\[(\d+(?:\.\d+)?)\s*[–-]\s*(\d+(?:\.\d+)?)\]\s*(.*)$`)

// word is one word of the transcript, with when it was said.
type word struct {
	at        float64
	norm, raw string
}

// placed is a line with its start found.
type placed struct {
	n      int
	at     float64
	text   string
	heard  string
	exact  bool
	byTime bool
}

func script(ctx context.Context, raw json.RawMessage) (string, error) {
	var args struct {
		Video string `json:"video"`
		Lines []struct {
			StartsWith string   `json:"starts_with"`
			At         *float64 `json:"at"`
			Text       string   `json:"text"`
		} `json:"lines"`
	}
	if err := serve.Args(raw, &args); err != nil {
		return "", err
	}
	p, err := openProject(args.Video)
	if err != nil {
		return "", err
	}
	if len(args.Lines) == 0 {
		return "", errors.New("no lines: give the script's lines, each with its text and starts_with, or at for a video with no narration")
	}
	ff, err := findFFmpeg()
	if err != nil {
		return "", err
	}
	c, err := ff.probe(ctx, p.video)
	if err != nil {
		return "", err
	}
	backend, err := newOpenRouter()
	if err != nil {
		return "", err
	}

	var words []word
	for _, l := range args.Lines {
		if l.At == nil {
			if words, err = loadWords(filepath.Join(p.dir, "segments.txt")); err != nil {
				return "", err
			}
			break
		}
	}
	var rows []placed
	var problems []string
	cursor, last := 0, 0.0
	for i, l := range args.Lines {
		n := i + 1
		text := strings.TrimSpace(l.Text)
		switch {
		case text == "":
			problems = append(problems, fmt.Sprintf("line %d has no text", n))
			continue
		case len(text) > maxChars:
			problems = append(problems, fmt.Sprintf("line %d has %d characters, over the %d one speech call can take: split it", n, len(text), maxChars))
			continue
		}
		r := placed{n: n, text: text, exact: true}
		if l.At != nil {
			r.at, r.heard, r.byTime = *l.At, "(set by time)", true
		} else {
			want := tokens(l.StartsWith)
			if len(want) < minWords {
				problems = append(problems, fmt.Sprintf("line %d: starts_with needs at least %d words, copied from the transcript", n, minWords))
				continue
			}
			j, exact, ok := find(words, want, cursor)
			if !ok {
				problems = append(problems, fmt.Sprintf("line %d: %q is not in the transcript after %.1f s", n, l.StartsWith, last))
				continue
			}
			end := min(j+6, len(words))
			var heard []string
			for _, w := range words[j:end] {
				heard = append(heard, w.raw)
			}
			r.at, r.heard, r.exact = words[j].at, strings.Join(heard, " "), exact
			cursor = j + len(want)
		}
		if len(rows) > 0 && r.at < last {
			problems = append(problems, fmt.Sprintf("line %d starts at %.1f s, before line %d, the line ahead of it", n, r.at, rows[len(rows)-1].n))
		}
		if r.at >= c.seconds {
			problems = append(problems, fmt.Sprintf("line %d starts at %.1f s, after the video's end at %.1f s: nothing of it would be heard", n, r.at, c.seconds))
		}
		last = r.at
		rows = append(rows, r)
	}
	if len(problems) > 0 {
		return "", fmt.Errorf("nothing was written:\n%s", strings.Join(problems, "\n"))
	}

	lines := make([]scriptLine, len(rows))
	for i, r := range rows {
		lines[i] = scriptLine{r.at, r.text}
	}
	ch, err := p.choices()
	if err != nil {
		return "", err
	}
	if err := p.writeScript(lines); err != nil {
		return "", err
	}
	// The lines given are the translation, if one was owed.
	if ch.TranslateTo != "" {
		ch.TranslateTo = ""
		if err := p.writeChoices(ch); err != nil {
			return "", err
		}
	}

	var b strings.Builder
	b.WriteString(table(rows, c.seconds))
	fmt.Fprintf(&b, "Wrote %s: %s. Read the last column: each line must be the same thought as what the narrator says "+
		"at its start. ~ is a near match, so look at it twice; TIGHT is a line the next will probably cut off.\n",
		shown(filepath.Join(p.dir, "script.json")), count(len(lines), "line"))
	vs, err := offered(ctx, backend)
	if err != nil {
		fmt.Fprintf(&b, "What speaking it costs could not be read: %v\n", err)
		return b.String(), nil
	}
	b.WriteString(speakingCost(p, lines, vs, ch.Voice))
	b.WriteString(voiceList(vs))
	return b.String(), nil
}

// table is the placement as the model reads it, as place_chunks.py printed
// it: each line's start, the time until the next starts (or the video ends),
// how long it will probably take to say, and what the narrator says there.
func table(rows []placed, seconds float64) string {
	var b strings.Builder
	b.WriteString(" #    start    gap   est  line                            narrator at that moment\n")
	for k, r := range rows {
		next := seconds
		if k+1 < len(rows) {
			next = rows[k+1].at
		}
		gap := next - r.at
		est := float64(len(strings.Fields(r.text))) / wordsPerSecond
		mark := ""
		if est > gap {
			mark = " TIGHT"
		}
		near := ""
		if !r.exact {
			near = "~ "
		}
		fmt.Fprintf(&b, "%02d %8.2f %6.1f %5.1f  %-30s  %s%s%s\n", r.n, r.at, gap, est, cut(r.text, 30), near, cut(r.heard, 44), mark)
	}
	return b.String()
}

func cut(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n])
}

// offered is every voice of the offered models, in their order.
func offered(ctx context.Context, s Speaker) ([]Voice, error) {
	all, err := s.Voices(ctx)
	if err != nil {
		return nil, err
	}
	var out []Voice
	for _, m := range offeredModels {
		for _, v := range all {
			if v.Model == m {
				out = append(out, v)
			}
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("the gateway lists none of the voices of %s", strings.Join(offeredModels, " or "))
	}
	return out, nil
}

// pick is the voice a name chooses, or the default when there is none.
func pick(vs []Voice, name string) (Voice, error) {
	if name == "" {
		name = defaultVoice
	}
	for _, v := range vs {
		if v.Name == name {
			return v, nil
		}
	}
	return Voice{}, fmt.Errorf("there is no voice %q. The voices there are:\n%s", name, voiceList(vs))
}

// speakingCost is what speaking the lines not spoken yet will cost.
func speakingCost(p project, lines []scriptLine, vs []Voice, name string) string {
	v, err := pick(vs, name)
	if err != nil {
		return fmt.Sprintf("What speaking it costs is not known: the voice chosen before, %q, is not one there is now.\n", name)
	}
	todo, chars := 0, 0
	for _, l := range lines {
		if p.spoken(v, l.Text) == "" {
			todo++
			chars += len(l.Text)
		}
	}
	return fmt.Sprintf("Speaking it: %s to speak, %d already spoken, %d characters, about %s in %s (%s, $%g per million characters).\n",
		count(todo, "line"), len(lines)-todo, chars, dollars(float64(chars)*v.PerMillion/1e6), v.Name, v.ModelName, v.PerMillion)
}

// voiceList is the voices, one model a line.
func voiceList(vs []Voice) string {
	var b strings.Builder
	b.WriteString("Voices, one for the whole video:\n")
	for i := 0; i < len(vs); {
		j, names := i, []string{}
		for ; j < len(vs) && vs[j].Model == vs[i].Model; j++ {
			names = append(names, vs[j].Name)
		}
		fmt.Fprintf(&b, "  %s, $%g per million characters: %s\n", vs[i].ModelName, vs[i].PerMillion, strings.Join(names, ", "))
		i = j
	}
	return b.String()
}

// norm is a word as it is compared: lower case, letters and digits only.
func norm(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(s) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
		}
	}
	return b.String()
}

func tokens(s string) []string {
	var out []string
	for _, w := range strings.Fields(s) {
		if t := norm(w); t != "" {
			out = append(out, t)
		}
	}
	return out
}

// loadWords is every word of the transcript with a time, spread evenly
// across its phrase.
func loadWords(path string) ([]word, error) {
	f, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("there is no transcript to find the lines' words in: call voiceover_transcribe first, " +
			"or give each line at, in seconds, for a video with no narration")
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var words []word
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64<<10), 1<<20)
	for sc.Scan() {
		m := segmentLine.FindStringSubmatch(strings.TrimSpace(sc.Text()))
		if m == nil {
			continue
		}
		start, _ := strconv.ParseFloat(m[1], 64)
		end, _ := strconv.ParseFloat(m[2], 64)
		var raw []string
		for _, w := range strings.Fields(m[3]) {
			if norm(w) != "" {
				raw = append(raw, w)
			}
		}
		for i, w := range raw {
			words = append(words, word{start + (end-start)*float64(i)/float64(len(raw)), norm(w), w})
		}
	}
	return words, sc.Err()
}

// find is the first place at or after cursor that says want exactly, or
// else the nearest, if it is near enough.
func find(words []word, want []string, cursor int) (at int, exact, ok bool) {
	n := len(want)
	for j := cursor; j+n <= len(words); j++ {
		same := true
		for k := range want {
			if words[j+k].norm != want[k] {
				same = false
				break
			}
		}
		if same {
			return j, true, true
		}
	}
	target := []rune(strings.Join(want, ""))
	best, at := 0.0, -1
	for j := cursor; j+n <= len(words); j++ {
		var s strings.Builder
		for _, w := range words[j : j+n] {
			s.WriteString(w.norm)
		}
		if r := ratio(target, []rune(s.String())); r > best {
			best, at = r, j
		}
	}
	if best >= fuzzy {
		return at, false, true
	}
	return 0, false, false
}

// ratio is how alike two strings are, as Python's difflib.SequenceMatcher
// says: twice the characters they share in matching blocks, over both
// lengths. The skill placed lines with difflib, and a port that measured
// nearness otherwise would place them elsewhere.
func ratio(a, b []rune) float64 {
	if len(a)+len(b) == 0 {
		return 1
	}
	return 2 * float64(matching(a, b)) / float64(len(a)+len(b))
}

// matching is the characters in difflib's matching blocks: the longest
// common run, then the same on each side of it.
func matching(a, b []rune) int {
	i, j, k := longestRun(a, b)
	if k == 0 {
		return 0
	}
	return k + matching(a[:i], b[:j]) + matching(a[i+k:], b[j+k:])
}

// longestRun is the longest run a and b share, the one that ends first in a
// and then in b where several are as long: difflib's choice.
func longestRun(a, b []rune) (int, int, int) {
	bi, bj, bk := 0, 0, 0
	prev := make([]int, len(b)+1)
	for i := range a {
		cur := make([]int, len(b)+1)
		for j := range b {
			if a[i] == b[j] {
				k := prev[j] + 1
				cur[j+1] = k
				if k > bk {
					bi, bj, bk = i-k+1, j-k+1, k
				}
			}
		}
		prev = cur
	}
	return bi, bj, bk
}
