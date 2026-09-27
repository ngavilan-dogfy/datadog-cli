package cmd

import (
	"fmt"
	"time"

	"github.com/spf13/cobra"
)

// windowFlags are the window flags the analysis commands share: a lookback
// (--since 4h, or --since "yesterday 18:00"), an exact window (--from/--to),
// or a moment and its surroundings (--around "today 09:40" --window 1h).
// Times can be spoken, in English or Spanish; see parseWhen.
type windowFlags struct {
	since, from, to, around, window string
	def                             time.Duration
}

func (w *windowFlags) register(c *cobra.Command, def time.Duration) {
	w.def = def
	c.Flags().StringVar(&w.since, "since", "", "Lookback (30m, 4h, 2d) or a moment (yesterday, \"today 09:00\"); default "+fmtDuration(def))
	c.Flags().StringVar(&w.from, "from", "", "Start: RFC3339, epoch, or spoken (\"yesterday 18:00\", \"hace 2h\")")
	c.Flags().StringVar(&w.to, "to", "", "End (default now)")
	c.Flags().StringVar(&w.around, "around", "", "A moment to center the window on (\"today 09:40\", RFC3339…)")
	c.Flags().StringVar(&w.window, "window", "", "Length of the window around --around (default 1h)")
}

func (w *windowFlags) resolve() (time.Time, time.Time, error) {
	return w.resolveAt(time.Now())
}

func (w *windowFlags) resolveAt(now time.Time) (time.Time, time.Time, error) {
	if w.around != "" {
		center, err := parseWhen(w.around, now)
		if err != nil {
			return time.Time{}, time.Time{}, fmt.Errorf("--around: %w", err)
		}
		span := time.Hour
		if w.window != "" {
			if span, err = parseDuration(w.window); err != nil {
				return time.Time{}, time.Time{}, fmt.Errorf("--window: %w", err)
			}
		}
		from, to := center.Add(-span/2), center.Add(span/2)
		if to.After(now) {
			to = now
		}
		if !from.Before(to) {
			return time.Time{}, time.Time{}, fmt.Errorf("--around is in the future")
		}
		return from, to, nil
	}
	to := now
	if w.to != "" {
		t, err := parseWhen(w.to, now)
		if err != nil {
			return time.Time{}, time.Time{}, fmt.Errorf("--to: %w", err)
		}
		to = t
	}
	from := to.Add(-w.def)
	switch {
	case w.from != "":
		f, err := parseWhen(w.from, now)
		if err != nil {
			return time.Time{}, time.Time{}, fmt.Errorf("--from: %w", err)
		}
		from = f
	case w.since != "":
		if d, err := parseDuration(w.since); err == nil {
			from = to.Add(-d)
		} else if t, werr := parseWhen(w.since, now); werr == nil {
			from = t
		} else {
			return time.Time{}, time.Time{}, fmt.Errorf("--since: %q is neither a duration (30m, 4h, 2d) nor a moment (yesterday, \"today 09:00\")", w.since)
		}
	}
	if !from.Before(to) {
		return time.Time{}, time.Time{}, fmt.Errorf("the window is empty: it must start before it ends")
	}
	return from, to, nil
}

func fmtDuration(d time.Duration) string {
	switch {
	case d%(24*time.Hour) == 0:
		return fmt.Sprintf("%dd", d/(24*time.Hour))
	case d%time.Hour == 0:
		return fmt.Sprintf("%dh", d/time.Hour)
	}
	return fmt.Sprintf("%dm", d/time.Minute)
}

// clockFor formats instants for a window: 15:04 within a day, with the date
// for longer windows, always in local time (say which zone in headers).
func clockFor(from, to time.Time) func(ms int64) string {
	layout := "15:04"
	if to.Sub(from) > 20*time.Hour {
		layout = "Jan 2 15:04"
	}
	return func(ms int64) string { return time.UnixMilli(ms).Local().Format(layout) }
}

// zoneName is the local time zone's abbreviation (CEST, UTC…).
func zoneName() string {
	name, _ := time.Now().Zone()
	return name
}

// fmtWindow is a window for headers: "Sep 26 13:48 → 14:48 CEST", with the
// end's date too when it isn't the same day.
func fmtWindow(from, to time.Time) string {
	from, to = from.Local(), to.Local()
	end := to.Format("15:04")
	if from.YearDay() != to.YearDay() || from.Year() != to.Year() {
		end = to.Format("Jan 2 15:04")
	}
	return from.Format("Jan 2 15:04") + " → " + end + " " + zoneName()
}
