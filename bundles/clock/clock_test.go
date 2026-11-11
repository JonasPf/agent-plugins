package main

import (
	"strings"
	"testing"
	"time"
)

// A job is far more often days away than nanoseconds, and Go's own duration
// parser stops at hours, so days and weeks have to be understood here.
func TestParseOffset(t *testing.T) {
	for _, c := range []struct {
		in   string
		want time.Duration
	}{
		{"", 0},
		{"90m", 90 * time.Minute},
		{"2h30m", 2*time.Hour + 30*time.Minute},
		{"-1h", -time.Hour},
		{"+15s", 15 * time.Second},
		{"3d", 72 * time.Hour},
		{"2w", 14 * 24 * time.Hour},
		{"1d12h", 36 * time.Hour},
		{"1.5h", 90 * time.Minute},
	} {
		got, err := ParseOffset(c.in)
		if err != nil {
			t.Errorf("ParseOffset(%q) errored: %v", c.in, err)
			continue
		}
		if got != c.want {
			t.Errorf("ParseOffset(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}

// A malformed offset has to be refused rather than silently read as zero, which
// would answer a question about a different time than the one asked.
func TestParseOffsetRefusesNonsense(t *testing.T) {
	for _, in := range []string{"soon", "2", "h", "2x", "2h30", "--1h", "-", "2 h"} {
		if got, err := ParseOffset(in); err == nil {
			t.Errorf("ParseOffset(%q) = %v, want a refusal", in, got)
		}
	}
}

// Four forms, because four different questions are being asked: an unambiguous
// instant, the operator's own wall clock, the day of the week, and something to
// do arithmetic on.
func TestFormatAnswersEveryForm(t *testing.T) {
	got := Format(time.Date(2026, 9, 4, 21, 30, 0, 0, time.UTC))
	for _, want := range []string{"utc:", "local:", "weekday:", "unix:",
		"2026-09-04T21:30:00Z", "1788557400"} {
		if !strings.Contains(got, want) {
			t.Errorf("Format missing %q:\n%s", want, got)
		}
	}
	if !strings.Contains(got, "Friday") {
		t.Errorf("Format does not name the weekday:\n%s", got)
	}
}
