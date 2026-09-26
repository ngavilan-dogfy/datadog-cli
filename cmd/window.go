package cmd

import (
	"fmt"
	"time"

	"github.com/spf13/cobra"
)

// windowFlags are the --since / --from / --to trio the analysis commands
// share: a lookback duration, or an exact window (RFC3339 or epoch).
type windowFlags struct {
	since, from, to string
	def             time.Duration
}

func (w *windowFlags) register(c *cobra.Command, def time.Duration) {
	w.def = def
	c.Flags().StringVar(&w.since, "since", "", "Lookback window (30m, 4h, 2d; default "+fmtDuration(def)+")")
	c.Flags().StringVar(&w.from, "from", "", "Start (RFC3339 or epoch)")
	c.Flags().StringVar(&w.to, "to", "", "End (RFC3339 or epoch; default now)")
}

func (w *windowFlags) resolve() (time.Time, time.Time, error) {
	to := time.Now()
	if w.to != "" {
		t, err := parseDate(w.to)
		if err != nil {
			return time.Time{}, time.Time{}, fmt.Errorf("--to: %w", err)
		}
		to = t
	}
	from := to.Add(-w.def)
	switch {
	case w.from != "":
		f, err := parseDate(w.from)
		if err != nil {
			return time.Time{}, time.Time{}, fmt.Errorf("--from: %w", err)
		}
		from = f
	case w.since != "":
		d, err := parseDuration(w.since)
		if err != nil {
			return time.Time{}, time.Time{}, fmt.Errorf("--since: %w", err)
		}
		from = to.Add(-d)
	}
	if !from.Before(to) {
		return time.Time{}, time.Time{}, fmt.Errorf("the window is empty: --from must be before --to")
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
