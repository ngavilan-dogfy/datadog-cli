// Package viz draws charts for the terminal: time series (lines in braille or
// half blocks, areas and bars in eighth blocks), sparklines, horizontal bars
// and big numbers. Everything renders to plain lines of exactly the asked
// width, styled with lipgloss, so the same charts serve the TUI and the
// command line.
package viz

import (
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
)

// Unit describes what the values measure, as Datadog reports it.
type Unit struct {
	Family string  // time, bytes, percentage, network, …
	Name   string  // millisecond, byte, percent, request, …
	Short  string  // ms, B, %, req
	Scale  float64 // factor to the family's base unit (millisecond → 0.001 s)
}

// Format renders a value compactly (at most ~6 characters): 1.2k, 350ms,
// 1.5GiB, 12%.
func Format(v float64, u Unit) string {
	if math.IsNaN(v) {
		return "–"
	}
	if math.IsInf(v, 0) {
		return "∞"
	}
	switch u.Family {
	case "time":
		return formatDuration(v * scaleOr1(u.Scale))
	case "bytes":
		return formatBytes(v*scaleOr1(u.Scale), 1024, []string{"B", "KiB", "MiB", "GiB", "TiB", "PiB"})
	case "bits":
		return formatBytes(v*scaleOr1(u.Scale), 1000, []string{"b", "kb", "Mb", "Gb", "Tb", "Pb"})
	case "percentage":
		if u.Name == "fraction" {
			v *= 100
		}
		return compact(v) + "%"
	}
	s := compact(v)
	if u.Short != "" && len(u.Short) <= 4 && !strings.ContainsAny(u.Short, " ") {
		s += u.Short
	}
	return s
}

// FormatText is Format for sentences, with room to breathe: "11k req",
// not the chart's "11kreq".
func FormatText(v float64, u Unit) string {
	switch u.Family {
	case "time", "bytes", "bits", "percentage":
		return Format(v, u)
	}
	s := Format(v, Unit{})
	switch {
	case s == "–":
	case u.Short != "":
		s += " " + u.Short
	case u.Name != "":
		s += " " + u.Name
	}
	return s
}

func scaleOr1(s float64) float64 {
	if s == 0 {
		return 1
	}
	return s
}

// compact: 0.0123, 1.23, 12.3, 123, 1.2k, 12k, 123k, 1.2M…
func compact(v float64) string {
	a := math.Abs(v)
	switch {
	case a == 0:
		return "0"
	case a >= 1e12:
		return trim(v/1e12) + "T"
	case a >= 1e9:
		return trim(v/1e9) + "G"
	case a >= 1e6:
		return trim(v/1e6) + "M"
	case a >= 1e4:
		return trim(v/1e3) + "k"
	case a >= 1000:
		return strconv.FormatFloat(math.Round(v), 'f', 0, 64)
	case a >= 100:
		return strconv.FormatFloat(v, 'f', 0, 64)
	case a >= 10:
		return trimZeros(strconv.FormatFloat(v, 'f', 1, 64))
	case a >= 0.01:
		return trimZeros(strconv.FormatFloat(v, 'f', 2, 64))
	}
	return strconv.FormatFloat(v, 'g', 2, 64)
}

// trim keeps 3 significant digits: 1.23, 12.3, 123.
func trim(v float64) string {
	a := math.Abs(v)
	switch {
	case a >= 100:
		return strconv.FormatFloat(v, 'f', 0, 64)
	case a >= 10:
		return trimZeros(strconv.FormatFloat(v, 'f', 1, 64))
	}
	return trimZeros(strconv.FormatFloat(v, 'f', 2, 64))
}

func trimZeros(s string) string {
	if strings.Contains(s, ".") {
		s = strings.TrimRight(strings.TrimRight(s, "0"), ".")
	}
	if s == "-0" {
		return "0"
	}
	return s
}

func formatDuration(sec float64) string {
	a := math.Abs(sec)
	switch {
	case a == 0:
		return "0"
	case a < 1e-6:
		return trim(sec*1e9) + "ns"
	case a < 1e-3:
		return trim(sec*1e6) + "µs"
	case a < 1:
		return trim(sec*1e3) + "ms"
	case a < 60:
		return trim(sec) + "s"
	case a < 3600:
		return trim(sec/60) + "min"
	case a < 86400:
		return trim(sec/3600) + "h"
	}
	return trim(sec/86400) + "d"
}

func formatBytes(v, base float64, names []string) string {
	a := math.Abs(v)
	i := 0
	for a >= base && i < len(names)-1 {
		a /= base
		v /= base
		i++
	}
	return trim(v) + names[i]
}

// niceTicks returns about n "round" values covering [lo, hi] (1/2/5 steps).
func niceTicks(lo, hi float64, n int) (float64, float64, float64) {
	if n < 1 {
		n = 1
	}
	if hi == lo {
		if hi == 0 {
			return 0, 1, 1
		}
		d := math.Abs(hi) * 0.1
		lo, hi = lo-d, hi+d
	}
	raw := (hi - lo) / float64(n)
	mag := math.Pow(10, math.Floor(math.Log10(raw)))
	var step float64
	switch r := raw / mag; {
	case r <= 1:
		step = mag
	case r <= 2:
		step = 2 * mag
	case r <= 2.5:
		step = 2.5 * mag
	case r <= 5:
		step = 5 * mag
	default:
		step = 10 * mag
	}
	return math.Floor(lo/step) * step, math.Ceil(hi/step) * step, step
}

// timeSteps are the label intervals charts pick from.
var timeSteps = []time.Duration{
	time.Minute, 2 * time.Minute, 5 * time.Minute, 10 * time.Minute, 15 * time.Minute, 30 * time.Minute,
	time.Hour, 2 * time.Hour, 3 * time.Hour, 6 * time.Hour, 12 * time.Hour,
	24 * time.Hour, 2 * 24 * time.Hour, 7 * 24 * time.Hour, 14 * 24 * time.Hour, 30 * 24 * time.Hour,
}

func timeLabel(t time.Time, span time.Duration) string {
	switch {
	case span <= 36*time.Hour:
		return t.Format("15:04")
	case span <= 8*24*time.Hour:
		if t.Hour() == 0 && t.Minute() == 0 {
			return t.Format("Mon 2")
		}
		return t.Format("Mon 15h")
	}
	return t.Format("Jan 2")
}

// FormatSpan renders a time range length: 15m, 4h, 2d, 1w.
func FormatSpan(d time.Duration) string {
	switch {
	case d%(7*24*time.Hour) == 0 && d >= 7*24*time.Hour:
		return fmt.Sprintf("%dw", int(d/(7*24*time.Hour)))
	case d%(24*time.Hour) == 0 && d >= 24*time.Hour:
		return fmt.Sprintf("%dd", int(d/(24*time.Hour)))
	case d%time.Hour == 0 && d >= time.Hour:
		return fmt.Sprintf("%dh", int(d/time.Hour))
	}
	return fmt.Sprintf("%dm", int(d/time.Minute))
}

// Palette is the series color order, from the terminal's own ANSI colors so
// charts follow its theme: blue, magenta, cyan, green, yellow, red, then
// the bright variants.
func Palette() []lipgloss.TerminalColor {
	return []lipgloss.TerminalColor{
		lipgloss.Color("4"), lipgloss.Color("5"), lipgloss.Color("6"), lipgloss.Color("2"),
		lipgloss.Color("3"), lipgloss.Color("1"), lipgloss.Color("12"), lipgloss.Color("13"),
		lipgloss.Color("14"), lipgloss.Color("10"), lipgloss.Color("11"), lipgloss.Color("9"),
	}
}
