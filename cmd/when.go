package cmd

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// parseWhen reads a moment the way people say it, in local time, besides
// the exact forms (RFC3339, epoch seconds or milliseconds, "2006-01-02
// 15:04", "2006-01-02"; without a zone they're local):
//
//	now · today · yesterday · today 09:40 · yesterday 18:00 · 09:40 · monday 9:00 · 2h ago
//	ahora · hoy · ayer · anteayer · hoy 9:40 · ayer a las 18:00 · lunes 9:00 · hace 2h
//
// A bare time is today, or yesterday when that time hasn't come yet; a
// weekday is the last one before today.
func parseWhen(s string, now time.Time) (time.Time, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return time.Time{}, fmt.Errorf("empty time")
	}
	if v, err := strconv.ParseInt(s, 10, 64); err == nil {
		if len(s) >= 13 {
			return time.UnixMilli(v), nil
		}
		return time.Unix(v, 0), nil
	}
	for _, layout := range []string{time.RFC3339, time.RFC3339Nano} {
		if v, err := time.Parse(layout, s); err == nil {
			return v, nil
		}
	}
	for _, layout := range []string{"2006-01-02T15:04:05", "2006-01-02 15:04:05", "2006-01-02T15:04", "2006-01-02 15:04", "2006-01-02"} {
		if v, err := time.ParseInLocation(layout, s, now.Location()); err == nil {
			return v, nil
		}
	}
	if t, ok := parseSpokenTime(strings.ToLower(s), now); ok {
		return t, nil
	}
	return time.Time{}, fmt.Errorf("unrecognized time %q (try \"today 09:40\", \"yesterday 18:00\", \"2h ago\", RFC3339 or epoch)", s)
}

var (
	reAgo    = regexp.MustCompile(`^(\d+)\s*(m|min|mins|minutes?|h|hr|hrs|hours?|d|days?|w|weeks?)\s+ago$`)
	reHace   = regexp.MustCompile(`^hace\s+(\d+)\s*(m|min|mins|minutos?|h|horas?|d|d[ií]as?|semanas?)$`)
	reClock  = regexp.MustCompile(`^(\d{1,2})(?::(\d{2}))?\s*(am|pm|h)?$`)
	weekdays = map[string]time.Weekday{
		"sunday": time.Sunday, "monday": time.Monday, "tuesday": time.Tuesday, "wednesday": time.Wednesday,
		"thursday": time.Thursday, "friday": time.Friday, "saturday": time.Saturday,
		"sun": time.Sunday, "mon": time.Monday, "tue": time.Tuesday, "wed": time.Wednesday,
		"thu": time.Thursday, "fri": time.Friday, "sat": time.Saturday,
		"domingo": time.Sunday, "lunes": time.Monday, "martes": time.Tuesday, "miércoles": time.Wednesday,
		"miercoles": time.Wednesday, "jueves": time.Thursday, "viernes": time.Friday, "sábado": time.Saturday, "sabado": time.Saturday,
	}
)

func parseSpokenTime(s string, now time.Time) (time.Time, bool) {
	unit := func(u string) time.Duration {
		switch {
		case strings.HasPrefix(u, "w"), strings.HasPrefix(u, "sem"):
			return 7 * 24 * time.Hour
		case strings.HasPrefix(u, "d"):
			return 24 * time.Hour
		case strings.HasPrefix(u, "h"):
			return time.Hour
		}
		return time.Minute
	}
	for _, re := range []*regexp.Regexp{reAgo, reHace} {
		if m := re.FindStringSubmatch(s); m != nil {
			n, _ := strconv.Atoi(m[1])
			return now.Add(-time.Duration(n) * unit(m[2])), true
		}
	}
	if s == "now" || s == "ahora" {
		return now, true
	}

	// "<day> [at|a las] <clock>", or either alone.
	s = strings.NewReplacer(" at ", " ", " a las ", " ", " a la ", " ", ",", " ").Replace(s)
	fields := strings.Fields(s)
	midnight := func(t time.Time) time.Time { return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, t.Location()) }
	var day time.Time
	dayGiven := false
	if len(fields) > 0 {
		switch f := fields[0]; {
		case f == "today" || f == "hoy":
			day, dayGiven = midnight(now), true
		case f == "yesterday" || f == "ayer":
			day, dayGiven = midnight(now).AddDate(0, 0, -1), true
		case f == "anteayer":
			day, dayGiven = midnight(now).AddDate(0, 0, -2), true
		default:
			if wd, ok := weekdays[f]; ok {
				back := (int(now.Weekday()) - int(wd) + 7) % 7
				if back == 0 {
					back = 7
				}
				day, dayGiven = midnight(now).AddDate(0, 0, -back), true
			}
		}
		if dayGiven {
			fields = fields[1:]
		}
	}
	if len(fields) == 0 {
		return day, dayGiven
	}
	if len(fields) > 1 {
		return time.Time{}, false
	}
	m := reClock.FindStringSubmatch(fields[0])
	if m == nil {
		return time.Time{}, false
	}
	h, _ := strconv.Atoi(m[1])
	min := 0
	if m[2] != "" {
		min, _ = strconv.Atoi(m[2])
	}
	switch m[3] {
	case "pm":
		if h < 12 {
			h += 12
		}
	case "am":
		if h == 12 {
			h = 0
		}
	}
	if h > 23 || min > 59 {
		return time.Time{}, false
	}
	at := func(d time.Time) time.Time {
		return time.Date(d.Year(), d.Month(), d.Day(), h, min, 0, 0, d.Location())
	}
	if !dayGiven {
		day = midnight(now)
		if at(day).After(now) {
			day = day.AddDate(0, 0, -1)
		}
	}
	return at(day), true
}
