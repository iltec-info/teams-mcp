package tools

import (
	"testing"
	"time"
)

func TestParseSince(t *testing.T) {
	loc := time.FixedZone("EEST", 3*3600)
	// Wednesday 2026-10-07 14:30 local
	now := time.Date(2026, 10, 7, 14, 30, 0, 0, loc)

	cases := []struct {
		in   string
		want time.Time
	}{
		{"", time.Time{}},
		{"today", time.Date(2026, 10, 7, 0, 0, 0, 0, loc)},
		{"TODAY", time.Date(2026, 10, 7, 0, 0, 0, 0, loc)},
		{"week", time.Date(2026, 10, 5, 0, 0, 0, 0, loc)},
		{"month", time.Date(2026, 10, 1, 0, 0, 0, 0, loc)},
		{"2026-09-30", time.Date(2026, 9, 30, 0, 0, 0, 0, loc)},
		{"2026-10-01T08:00:00Z", time.Date(2026, 10, 1, 8, 0, 0, 0, time.UTC)},
	}
	for _, c := range cases {
		got, err := parseSince(c.in, now)
		if err != nil {
			t.Errorf("parseSince(%q) error: %v", c.in, err)
			continue
		}
		if !got.Equal(c.want) {
			t.Errorf("parseSince(%q) = %v, want %v", c.in, got, c.want)
		}
	}

	if _, err := parseSince("yesterday-ish", now); err == nil {
		t.Error("parseSince(garbage) expected error")
	}
}

func TestParseSinceWeekOnMondayAndSunday(t *testing.T) {
	loc := time.UTC
	monday := time.Date(2026, 10, 5, 9, 0, 0, 0, loc)
	sunday := time.Date(2026, 10, 11, 9, 0, 0, 0, loc)
	wantMonday := time.Date(2026, 10, 5, 0, 0, 0, 0, loc)

	for name, now := range map[string]time.Time{"monday": monday, "sunday": sunday} {
		got, err := parseSince("week", now)
		if err != nil || !got.Equal(wantMonday) {
			t.Errorf("%s: parseSince(week) = %v, %v; want %v", name, got, err, wantMonday)
		}
	}
}
