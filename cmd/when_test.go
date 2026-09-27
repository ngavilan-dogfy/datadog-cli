package cmd

import (
	"testing"
	"time"
)

func TestParseWhen(t *testing.T) {
	loc := time.FixedZone("CEST", 2*3600)
	now := time.Date(2026, 9, 24, 10, 30, 0, 0, loc) // a Thursday morning
	day := func(d, h, m int) time.Time { return time.Date(2026, 9, d, h, m, 0, 0, loc) }
	cases := map[string]time.Time{
		"now":                  now,
		"ahora":                now,
		"today":                day(24, 0, 0),
		"hoy 9:40":             day(24, 9, 40),
		"today 09:40":          day(24, 9, 40),
		"yesterday 18:00":      day(23, 18, 0),
		"ayer a las 18:00":     day(23, 18, 0),
		"ayer 18h":             day(23, 18, 0),
		"anteayer":             day(22, 0, 0),
		"09:40":                day(24, 9, 40),
		"18:00":                day(23, 18, 0), // hasn't come yet today
		"6pm":                  day(23, 18, 0),
		"monday 9:00":          day(21, 9, 0),
		"lunes 9:00":           day(21, 9, 0),
		"thursday":             day(17, 0, 0), // the last one before today
		"2h ago":               now.Add(-2 * time.Hour),
		"30 min ago":           now.Add(-30 * time.Minute),
		"hace 2h":              now.Add(-2 * time.Hour),
		"hace 3 días":          now.Add(-72 * time.Hour),
		"2026-09-23 18:00":     day(23, 18, 0), // no zone: local
		"2026-09-23T16:00:00Z": time.Date(2026, 9, 23, 16, 0, 0, 0, time.UTC),
		"1790000000":           time.Unix(1790000000, 0),
		"1790000000000":        time.UnixMilli(1790000000000),
	}
	for in, want := range cases {
		got, err := parseWhen(in, now)
		if err != nil {
			t.Errorf("%q: %v", in, err)
			continue
		}
		if !got.Equal(want) {
			t.Errorf("%q = %v, want %v", in, got, want)
		}
	}
	for _, bad := range []string{"", "soon", "25:00", "yesterday tomorrow", "hace un rato"} {
		if _, err := parseWhen(bad, now); err == nil {
			t.Errorf("%q should not parse", bad)
		}
	}
}

func TestWindowFlags(t *testing.T) {
	loc := time.FixedZone("CEST", 2*3600)
	now := time.Date(2026, 9, 24, 10, 30, 0, 0, loc)
	cases := []struct {
		name     string
		w        windowFlags
		from, to time.Time
	}{
		{"default", windowFlags{def: 4 * time.Hour}, now.Add(-4 * time.Hour), now},
		{"since a duration", windowFlags{def: time.Hour, since: "2d"}, now.Add(-48 * time.Hour), now},
		{"since a moment", windowFlags{def: time.Hour, since: "yesterday 18:00"}, time.Date(2026, 9, 23, 18, 0, 0, 0, loc), now},
		{"around, default hour", windowFlags{def: 4 * time.Hour, around: "today 09:40"}, time.Date(2026, 9, 24, 9, 10, 0, 0, loc), time.Date(2026, 9, 24, 10, 10, 0, 0, loc)},
		{"around with a window", windowFlags{def: 4 * time.Hour, around: "ayer 18:00", window: "2h"}, time.Date(2026, 9, 23, 17, 0, 0, 0, loc), time.Date(2026, 9, 23, 19, 0, 0, 0, loc)},
		{"around now stops at now", windowFlags{def: time.Hour, around: "10:20", window: "1h"}, time.Date(2026, 9, 24, 9, 50, 0, 0, loc), now},
		{"from and to", windowFlags{def: time.Hour, from: "yesterday 18:00", to: "yesterday 20:00"}, time.Date(2026, 9, 23, 18, 0, 0, 0, loc), time.Date(2026, 9, 23, 20, 0, 0, 0, loc)},
	}
	for _, c := range cases {
		from, to, err := c.w.resolveAt(now)
		if err != nil {
			t.Errorf("%s: %v", c.name, err)
			continue
		}
		if !from.Equal(c.from) || !to.Equal(c.to) {
			t.Errorf("%s: got %v → %v, want %v → %v", c.name, from, to, c.from, c.to)
		}
	}
	if _, _, err := (&windowFlags{def: time.Hour, since: "someday"}).resolveAt(now); err == nil {
		t.Error("--since someday should fail")
	}
}
