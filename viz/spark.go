package viz

import (
	"math"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

var sparkLevels = []rune("▁▂▃▄▅▆▇█")

// Sparkline squeezes values into one row of width cells (eighth blocks).
// Gaps (NaN) stay blank.
func Sparkline(values []float64, width int, color lipgloss.TerminalColor) string {
	if width <= 0 {
		return ""
	}
	cols := make([]float64, width)
	cnt := make([]int, width)
	for i, v := range values {
		if math.IsNaN(v) {
			continue
		}
		x := i * width / max(1, len(values))
		cols[x] += v
		cnt[x]++
	}
	lo, hi := math.Inf(1), math.Inf(-1)
	for i := range cols {
		if cnt[i] > 0 {
			cols[i] /= float64(cnt[i])
			lo, hi = math.Min(lo, cols[i]), math.Max(hi, cols[i])
		}
	}
	if lo > 0 {
		lo = 0
	}
	var b strings.Builder
	for i := range cols {
		if cnt[i] == 0 {
			b.WriteRune(' ')
			continue
		}
		lv := 0
		if hi > lo {
			lv = int(math.Round((cols[i] - lo) / (hi - lo) * 7))
		}
		b.WriteRune(sparkLevels[max(0, min(7, lv))])
	}
	return lipgloss.NewStyle().Foreground(color).Render(b.String())
}

var hEighths = []rune(" ▏▎▍▌▋▊▉█")

// HBar is a horizontal bar of v/maxV of width cells, in eighths.
func HBar(v, maxV float64, width int, color lipgloss.TerminalColor) string {
	if width <= 0 {
		return ""
	}
	f := 0.0
	if maxV > 0 && !math.IsNaN(v) {
		f = math.Max(0, math.Min(1, v/maxV))
	}
	units := int(math.Round(f * float64(width*8)))
	full, part := units/8, units%8
	s := strings.Repeat("█", full)
	used := full
	if part > 0 {
		s += string(hEighths[part])
		used++
	}
	return lipgloss.NewStyle().Foreground(color).Render(s) + strings.Repeat(" ", width-used)
}
