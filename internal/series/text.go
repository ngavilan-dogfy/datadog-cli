package series

import (
	"fmt"
	"math"
	"strings"
)

// Formatter renders values and times for Text.
type Formatter struct {
	Value func(float64) string
	Time  func(ms int64) string
}

func (f Formatter) v(x float64) string {
	if f.Value == nil {
		return fmt.Sprintf("%.3g", x)
	}
	return f.Value(x)
}

func (f Formatter) t(ms int64) string {
	if f.Time == nil {
		return fmt.Sprint(ms)
	}
	return f.Time(ms)
}

// change describes going from a to b: "×7", "÷3" or "+40%".
func change(a, b float64) string {
	switch {
	case a == 0 && b == 0:
		return "no change"
	case a == 0 || math.Signbit(a) != math.Signbit(b):
		return "from " + fmt.Sprintf("%.3g", a)
	}
	r := b / a
	switch {
	case r >= 2:
		return fmt.Sprintf("×%.3g", r)
	case r <= 0.5:
		return fmt.Sprintf("÷%.3g", 1/r)
	}
	return fmt.Sprintf("%+.0f%%", (r-1)*100)
}

// Text is the summary as one line an agent can quote: levels and when they
// changed, peaks, gaps and where the series ends.
func (s Summary) Text(f Formatter) string {
	switch {
	case s.Points == 0:
		return "no data"
	case s.Flat:
		return "flat at " + f.v(s.Last)
	}
	var parts []string
	if len(s.Shifts) == 0 {
		parts = append(parts, fmt.Sprintf("around %s (%s–%s)", f.v(s.Median), f.v(s.Min), f.v(s.Max)))
		if math.Abs(s.Trend) >= 0.2 {
			dir := "up"
			if s.Trend < 0 {
				dir = "down"
			}
			parts = append(parts, fmt.Sprintf("trending %s %.0f%%", dir, math.Abs(s.Trend)*100))
		}
	} else {
		parts = append(parts, "~"+f.v(s.Shifts[0].From))
		for _, sh := range s.Shifts {
			verb := "rose"
			if sh.To < sh.From {
				verb = "fell"
			}
			parts = append(parts, fmt.Sprintf("%s to ~%s at %s (%s)", verb, f.v(sh.To), f.t(sh.At), change(sh.From, sh.To)))
		}
	}
	if s.Max > s.P95*1.15 || len(s.Shifts) > 0 {
		parts = append(parts, fmt.Sprintf("peak %s at %s", f.v(s.Max), f.t(s.MaxAt)))
	}
	for _, sp := range s.Spikes {
		if sp.At != s.MaxAt {
			parts = append(parts, fmt.Sprintf("spike %s at %s", f.v(sp.Value), f.t(sp.At)))
		}
	}
	if len(s.Gaps) > 2 {
		longest := s.Gaps[0]
		for _, g := range s.Gaps {
			if g.To-g.From > longest.To-longest.From {
				longest = g
			}
		}
		parts = append(parts, fmt.Sprintf("patchy: %d gaps, longest %s–%s", len(s.Gaps), f.t(longest.From), f.t(longest.To)))
	} else {
		for _, g := range s.Gaps {
			parts = append(parts, fmt.Sprintf("no data %s–%s", f.t(g.From), f.t(g.To)))
		}
	}
	parts = append(parts, "last "+f.v(s.Last))
	return strings.Join(parts, "; ")
}
