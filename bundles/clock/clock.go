package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/JonasPf/agent-plugins/internal/serve"
)

type args struct {
	Offset string `json:"offset"`
}

var units = map[byte]time.Duration{
	's': time.Second, 'm': time.Minute, 'h': time.Hour,
	'd': 24 * time.Hour, 'w': 7 * 24 * time.Hour,
}

// ParseOffset reads a Go-style duration such as 90m, 2h30m, -1h, 3d. Go's own
// time.ParseDuration stops at hours, and a job is far more often days away than
// nanoseconds, so days and weeks are added rather than doing without.
func ParseOffset(text string) (time.Duration, error) {
	text = strings.TrimSpace(text)
	if text == "" {
		return 0, nil
	}
	sign := time.Duration(1)
	body := text
	switch body[0] {
	case '-':
		sign, body = -1, body[1:]
	case '+':
		body = body[1:]
	}
	if body == "" {
		return 0, fmt.Errorf("offset %q is not a duration like 2m, 3h, 1h30m, 7d", text)
	}
	var total time.Duration
	for len(body) > 0 {
		i := 0
		for i < len(body) && (body[i] >= '0' && body[i] <= '9' || body[i] == '.') {
			i++
		}
		if i == 0 || i == len(body) {
			return 0, fmt.Errorf("offset %q is not a duration like 2m, 3h, 1h30m, 7d", text)
		}
		n, err := strconv.ParseFloat(body[:i], 64)
		if err != nil {
			return 0, fmt.Errorf("offset %q is not a duration like 2m, 3h, 1h30m, 7d", text)
		}
		unit, ok := units[body[i]]
		if !ok {
			return 0, fmt.Errorf("offset %q is not a duration like 2m, 3h, 1h30m, 7d", text)
		}
		total += time.Duration(n * float64(unit))
		body = body[i+1:]
	}
	return sign * total, nil
}

// Format is the answer, in the four forms a different question needs: an
// unambiguous instant, the operator's own wall clock, the day of the week, and
// something to do arithmetic on.
func Format(t time.Time) string {
	local := t.Local()
	zone, _ := local.Zone()
	if zone == "" {
		zone = "local"
	}
	return strings.Join([]string{
		"utc:      " + t.UTC().Format("2006-01-02T15:04:05Z"),
		"local:    " + local.Format("2006-01-02T15:04:05-0700") + " (" + zone + ")",
		"weekday:  " + local.Format("Monday 02 January 2006"),
		"unix:     " + strconv.FormatInt(t.Unix(), 10),
	}, "\n")
}

// clock answers what the time is. An agent cannot read a clock from a prompt
// written once when its conversation began, so it asks.
func clock(_ context.Context, raw json.RawMessage) (string, error) {
	var a args
	if err := serve.Args(raw, &a); err != nil {
		return "", err
	}
	d, err := ParseOffset(a.Offset)
	if err != nil {
		return "", err
	}
	return Format(time.Now().Add(d)), nil
}
