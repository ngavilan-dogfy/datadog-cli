package viz

import (
	"math"
	"sort"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

// Style is how lines are drawn. Areas and bars always use eighth blocks,
// which every programming font has.
type Style int

const (
	Blocks  Style = iota // half blocks: 1×2 pixels per cell, full color
	Braille              // braille dots: 2×4 per cell, needs a font with braille
)

// Kind of time-series chart, as Datadog's display_type.
type Kind int

const (
	Line Kind = iota
	Area      // stacked
	Bars      // stacked
)

// Point is one value at a time (ms). NaN is a gap.
type Point struct {
	T int64
	V float64
}

// Series is one line (or area/bar layer).
type Series struct {
	Name   string
	Color  lipgloss.TerminalColor
	Points []Point
}

// Marker is a horizontal reference line, like a monitor threshold.
type Marker struct {
	Value float64
	Color lipgloss.TerminalColor
	Label string
}

// Chart is a time-series chart of exactly Width × Height cells, y labels
// and x labels included.
type Chart struct {
	Width, Height int
	Series        []Series
	Kind          Kind
	Style         Style
	From, To      int64 // ms; zero = the data's range
	Unit          Unit
	Markers       []Marker
	FitMarkers    bool     // always widen the axis to show every marker
	Min, Max      *float64 // fixed axis bounds
	LooseZero     bool     // don't force the axis to include zero
	NoXAxis       bool
	NoYAxis       bool
	Cursor        int // highlighted plot column, -1 for none
	Axis          lipgloss.TerminalColor
	CursorBG      lipgloss.TerminalColor
	Loc           *time.Location
}

type cell struct {
	ch     string
	fg, bg lipgloss.TerminalColor
}

// layout is what Render computes once and helpers reuse.
type layout struct {
	from, to   int64
	yw, pw, ph int // y label width, plot width/height (cells)
	lo, hi     float64
	step       float64
}

func (c Chart) domain() (int64, int64) {
	from, to := c.From, c.To
	if from == 0 || to == 0 {
		var lo, hi int64 = math.MaxInt64, math.MinInt64
		for _, s := range c.Series {
			for _, p := range s.Points {
				lo, hi = min(lo, p.T), max(hi, p.T)
			}
		}
		if lo > hi {
			now := time.Now().UnixMilli()
			lo, hi = now-3600_000, now
		}
		if from == 0 {
			from = lo
		}
		if to == 0 {
			to = hi
		}
	}
	if to <= from {
		to = from + 1
	}
	return from, to
}

// buckets averages each series into n columns across [from, to).
func buckets(s Series, from, to int64, n int) []float64 {
	sum := make([]float64, n)
	cnt := make([]int, n)
	span := float64(to - from)
	for _, p := range s.Points {
		if math.IsNaN(p.V) || p.T < from || p.T > to {
			continue
		}
		i := int(float64(p.T-from) / span * float64(n))
		if i >= n {
			i = n - 1
		}
		sum[i] += p.V
		cnt[i]++
	}
	out := make([]float64, n)
	for i := range out {
		if cnt[i] == 0 {
			out[i] = math.NaN()
		} else {
			out[i] = sum[i] / float64(cnt[i])
		}
	}
	return out
}

// gapLimit is how many empty columns a line may bridge: sparse metrics
// report less often than the chart has columns, and those must connect.
func gapLimit(s Series, from, to int64, n int) int {
	var d []int64
	for i := 1; i < len(s.Points); i++ {
		if !math.IsNaN(s.Points[i].V) && !math.IsNaN(s.Points[i-1].V) {
			d = append(d, s.Points[i].T-s.Points[i-1].T)
		}
	}
	if len(d) == 0 {
		return n
	}
	sort.Slice(d, func(i, j int) bool { return d[i] < d[j] })
	med := float64(d[len(d)/2])
	cols := med / float64(to-from) * float64(n)
	return max(1, int(math.Ceil(cols*2.5)))
}

func (c Chart) layout() (layout, [][]float64) {
	from, to := c.domain()
	l := layout{from: from, to: to, ph: c.Height}
	if c.showX() {
		l.ph--
	}
	l.ph = max(1, l.ph)
	sub := 1
	if c.Kind == Line && c.Style == Braille {
		sub = 2
	}
	// The range comes from the values as drawn at the final width — which
	// depends on the y labels' width, hence the passes.
	var vals [][]float64
	width := c.Width
	for pass := 0; pass < 3; pass++ {
		vals = make([][]float64, len(c.Series))
		for i, s := range c.Series {
			vals[i] = buckets(s, from, to, max(1, width*sub))
		}
		lo, hi := c.valueRange(vals, from, to)
		c.fitTicks(&l, lo, hi)
		pw := max(1, c.Width-l.yw)
		if pw == width {
			break
		}
		width = pw
	}
	l.pw = max(1, width)
	return l, vals
}

func (c Chart) valueRange(vals [][]float64, from, to int64) (float64, float64) {
	lo, hi := math.Inf(1), math.Inf(-1)
	if c.Kind != Line {
		n := 0
		if len(vals) > 0 {
			n = len(vals[0])
		}
		for x := 0; x < n; x++ {
			sum := 0.0
			for _, vs := range vals {
				if v := vs[x]; !math.IsNaN(v) && v > 0 {
					sum += v
				}
			}
			hi = math.Max(hi, sum)
		}
		lo = 0
	} else {
		// What gets drawn, at the final resolution: tight, and a spike that
		// survives the bucketing is never clipped.
		for _, vs := range vals {
			for _, v := range vs {
				if !math.IsNaN(v) {
					lo, hi = math.Min(lo, v), math.Max(hi, v)
				}
			}
		}
	}
	if math.IsInf(lo, 1) {
		lo, hi = 0, 1
	}
	// Markers (thresholds) widen the axis only when they're near the data,
	// like Datadog: a 1.5 s alert line mustn't squash a 4 ms series flat.
	// FitMarkers (monitor charts) always shows them.
	dlo, dhi := lo, hi
	span := dhi - dlo
	for _, m := range c.Markers {
		near := span > 0 && m.Value >= dlo-span*0.25 && m.Value <= dhi+span*0.25
		if c.FitMarkers || near {
			lo, hi = math.Min(lo, m.Value), math.Max(hi, m.Value)
		}
	}
	if !c.LooseZero {
		if lo > 0 {
			lo = 0
		}
		if hi < 0 {
			hi = 0
		}
	}
	if c.Min != nil {
		lo = *c.Min
	}
	if c.Max != nil {
		hi = *c.Max
	}
	return lo, hi
}

// fitTicks picks round bounds and the y labels' width.
func (c Chart) fitTicks(l *layout, lo, hi float64) {
	l.lo, l.hi, l.step = niceTicks(lo, hi, max(1, l.ph/3))
	if c.Min != nil {
		l.lo = *c.Min
	}
	if c.Max != nil {
		l.hi = *c.Max
	}
	l.yw = 0
	if !c.NoYAxis {
		for _, v := range l.ticks() {
			l.yw = max(l.yw, ansi.StringWidth(Format(v, c.Unit)))
		}
		l.yw++                // gap between labels and plot
		if l.yw*3 > c.Width { // tiny chart: the data matters more than labels
			l.yw = 0
		}
	}
}

// ticks are the round values between lo and hi, snapped to the step so
// float noise never shows up as "-1e-17".
func (l layout) ticks() []float64 {
	if l.step <= 0 {
		return []float64{l.lo, l.hi}
	}
	var out []float64
	n := int(math.Round((l.hi - l.lo) / l.step))
	for i := 0; i <= n && i <= 100; i++ {
		v := l.lo + float64(i)*l.step
		v = math.Round(v/l.step) * l.step
		if math.Abs(v) < l.step*1e-9 {
			v = 0
		}
		out = append(out, v)
	}
	return out
}

// showX: the time axis needs room — charts under 3 rows go without it.
func (c Chart) showX() bool { return !c.NoXAxis && c.Height >= 3 }

// PlotWidth is the number of columns the data uses (for cursors).
func (c Chart) PlotWidth() int {
	l, _ := c.layout()
	return l.pw
}

// At returns the time and each series' value at a plot column.
func (c Chart) At(col int) (int64, []float64) {
	l, _ := c.layout()
	col = max(0, min(l.pw-1, col))
	t := l.from + int64(float64(l.to-l.from)*(float64(col)+0.5)/float64(l.pw))
	out := make([]float64, len(c.Series))
	for i, s := range c.Series {
		out[i] = buckets(s, l.from, l.to, l.pw)[col]
	}
	return t, out
}

// Render draws the chart: exactly Height lines of exactly Width cells.
func (c Chart) Render() []string {
	if c.Width < 4 || c.Height < 1 {
		return blankLines(c.Width, c.Height)
	}
	l, vals := c.layout()
	grid := make([][]cell, l.ph)
	for y := range grid {
		grid[y] = make([]cell, l.pw)
		for x := range grid[y] {
			grid[y][x] = cell{ch: " "}
		}
	}
	project := func(v float64, levels int) int { // 0 = top
		f := (v - l.lo) / (l.hi - l.lo)
		return levels - 1 - int(math.Round(f*float64(levels-1)))
	}
	switch {
	case c.Kind != Line:
		c.drawColumns(grid, l, vals)
	case c.Style == Braille:
		c.drawBraille(grid, l, vals, project)
	default:
		c.drawHalfBlocks(grid, l, vals, project)
	}
	for _, m := range c.Markers {
		if m.Value < l.lo || m.Value > l.hi {
			continue
		}
		row := project(m.Value, l.ph)
		for x := 0; x < l.pw; x++ {
			if grid[row][x].ch == " " {
				grid[row][x] = cell{ch: "╌", fg: m.Color}
			}
		}
	}
	if c.Cursor >= 0 && c.Cursor < l.pw {
		for y := range grid {
			grid[y][c.Cursor].bg = c.CursorBG
		}
	}
	// Y labels at round values, top and bottom always.
	labels := make([]string, l.ph)
	if !c.NoYAxis {
		used := map[int]bool{}
		ticks := l.ticks()
		for i := len(ticks) - 1; i >= 0; i-- {
			v := ticks[i]
			row := project(v, l.ph)
			if row < 0 || row >= l.ph || used[row] || used[row-1] || used[row+1] {
				continue
			}
			used[row] = true
			labels[row] = Format(v, c.Unit)
		}
	}
	axis := lipgloss.NewStyle().Foreground(c.Axis)
	out := make([]string, 0, c.Height)
	for y := 0; y < l.ph; y++ {
		var b strings.Builder
		if l.yw > 0 {
			lab := labels[y]
			b.WriteString(axis.Render(strings.Repeat(" ", max(0, l.yw-1-ansi.StringWidth(lab))) + lab))
			b.WriteString(" ")
		}
		b.WriteString(renderRow(grid[y]))
		out = append(out, b.String())
	}
	if c.showX() {
		out = append(out, strings.Repeat(" ", l.yw)+axis.Render(c.xLabels(l)))
	}
	for len(out) < c.Height {
		out = append(out, strings.Repeat(" ", c.Width))
	}
	return out
}

// renderRow styles runs of cells that share colors.
func renderRow(cells []cell) string {
	var b strings.Builder
	i := 0
	for i < len(cells) {
		j := i
		var run strings.Builder
		for j < len(cells) && cells[j].fg == cells[i].fg && cells[j].bg == cells[i].bg {
			run.WriteString(cells[j].ch)
			j++
		}
		st := lipgloss.NewStyle()
		if cells[i].fg != nil {
			st = st.Foreground(cells[i].fg)
		}
		if cells[i].bg != nil {
			st = st.Background(cells[i].bg)
		}
		if cells[i].fg == nil && cells[i].bg == nil {
			b.WriteString(run.String())
		} else {
			b.WriteString(st.Render(run.String()))
		}
		i = j
	}
	return b.String()
}

func (c Chart) drawBraille(grid [][]cell, l layout, vals [][]float64, project func(float64, int) int) {
	w, h := l.pw*2, l.ph*4
	masks := make([][]uint8, l.ph)
	owner := make([][]int, l.ph)
	for y := range masks {
		masks[y] = make([]uint8, l.pw)
		owner[y] = make([]int, l.pw)
	}
	bits := [2][4]uint8{{0x01, 0x02, 0x04, 0x40}, {0x08, 0x10, 0x20, 0x80}}
	set := func(x, y, s int) {
		if x < 0 || x >= w || y < 0 || y >= h {
			return
		}
		masks[y/4][x/2] |= bits[x%2][y%4]
		owner[y/4][x/2] = s + 1
	}
	for si, s := range c.Series {
		c.traceLine(vals[si], gapLimit(s, l.from, l.to, w), func(v float64) int { return project(v, h) },
			func(x, y int) { set(x, y, si) })
	}
	for y := range grid {
		for x := range grid[y] {
			if masks[y][x] != 0 {
				grid[y][x] = cell{ch: string(rune(0x2800 + int(masks[y][x]))), fg: c.Series[owner[y][x]-1].Color}
			}
		}
	}
}

func (c Chart) drawHalfBlocks(grid [][]cell, l layout, vals [][]float64, project func(float64, int) int) {
	w, h := l.pw, l.ph*2
	px := make([][]int, h)
	for y := range px {
		px[y] = make([]int, w)
	}
	for si, s := range c.Series {
		c.traceLine(vals[si], gapLimit(s, l.from, l.to, w), func(v float64) int { return project(v, h) },
			func(x, y int) {
				if x >= 0 && x < w && y >= 0 && y < h {
					px[y][x] = si + 1
				}
			})
	}
	col := func(i int) lipgloss.TerminalColor { return c.Series[i-1].Color }
	for y := range grid {
		for x := range grid[y] {
			t, b := px[2*y][x], px[2*y+1][x]
			switch {
			case t == 0 && b == 0:
			case b == 0:
				grid[y][x] = cell{ch: "▀", fg: col(t)}
			case t == 0:
				grid[y][x] = cell{ch: "▄", fg: col(b)}
			case t == b:
				grid[y][x] = cell{ch: "█", fg: col(t)}
			default:
				grid[y][x] = cell{ch: "▀", fg: col(t), bg: col(b)}
			}
		}
	}
}

// traceLine plots a series column by column, joining consecutive points
// (across short gaps) with vertical runs so steep changes stay connected.
func (c Chart) traceLine(vs []float64, gap int, project func(float64) int, plot func(x, y int)) {
	px, py := -1, 0
	for x, v := range vs {
		if math.IsNaN(v) {
			continue
		}
		y := project(v)
		if px >= 0 && x-px <= gap {
			// Straight segment from (px,py) to (x,y), then fill vertical gaps.
			dx := x - px
			for i := 1; i <= dx; i++ {
				yy := py + int(math.Round(float64((y-py)*i)/float64(dx)))
				prev := py + int(math.Round(float64((y-py)*(i-1))/float64(dx)))
				lo, hi := min(prev, yy), max(prev, yy)
				for k := lo; k <= hi; k++ {
					plot(px+i, k)
				}
			}
		} else {
			plot(x, y)
		}
		px, py = x, y
	}
}

var eighths = []string{"", "▁", "▂", "▃", "▄", "▅", "▆", "▇", "█"}

// drawColumns stacks areas/bars in eighth blocks. A cell holding the top of
// one series and the start of the next shows both: the lower one in the
// glyph's color, the upper one as the background.
func (c Chart) drawColumns(grid [][]cell, l layout, vals [][]float64) {
	n := len(vals)
	gapEvery := 0 // bars: leave a gap between wide bars
	if c.Kind == Bars {
		points := 0
		for _, s := range c.Series {
			points = max(points, len(s.Points))
		}
		if points > 0 && l.pw/points >= 3 {
			gapEvery = l.pw / points
		}
	}
	span := l.hi - l.lo
	for x := 0; x < l.pw; x++ {
		if gapEvery > 0 && x%gapEvery == gapEvery-1 {
			continue
		}
		// cumulative tops in eighths of a row
		tops := make([]int, n)
		acc := 0.0
		for s := 0; s < n; s++ {
			v := vals[s][x]
			if !math.IsNaN(v) && v > 0 {
				acc += v
			}
			tops[s] = int(math.Round((acc - l.lo) / span * float64(l.ph*8)))
		}
		for r := 0; r < l.ph; r++ { // r = 0 is the bottom row
			base, ceil := r*8, r*8+8
			seg := -1 // series covering the cell's bottom
			for s := 0; s < n; s++ {
				bottom := 0
				if s > 0 {
					bottom = tops[s-1]
				}
				if tops[s] > base && bottom <= base {
					seg = s
					break
				}
			}
			if seg < 0 {
				continue
			}
			y := l.ph - 1 - r
			end := tops[seg]
			if end >= ceil {
				grid[y][x] = cell{ch: "█", fg: c.Series[seg].Color}
				continue
			}
			cl := cell{ch: eighths[end-base], fg: c.Series[seg].Color}
			for s := seg + 1; s < n; s++ { // next layer continuing above
				if tops[s] > end {
					cl.bg = c.Series[s].Color
					break
				}
			}
			grid[y][x] = cl
		}
	}
}

func (c Chart) xLabels(l layout) string {
	row := []rune(strings.Repeat(" ", l.pw))
	span := time.Duration(l.to-l.from) * time.Millisecond
	loc := c.Loc
	if loc == nil {
		loc = time.Local
	}
	sample := timeLabel(time.UnixMilli(l.from).In(loc), span)
	need := float64(len([]rune(sample)) + 3)
	var step time.Duration
	for _, s := range timeSteps {
		if float64(s)/float64(span)*float64(l.pw) >= need {
			step = s
			break
		}
	}
	if step == 0 {
		return string(row)
	}
	start := time.UnixMilli(l.from).In(loc)
	t := start.Truncate(step)
	if step >= 24*time.Hour { // align days to local midnight
		t = time.Date(start.Year(), start.Month(), start.Day(), 0, 0, 0, 0, loc)
	}
	last := -10
	for ; t.UnixMilli() <= l.to; t = t.Add(step) {
		if t.UnixMilli() < l.from {
			continue
		}
		x := int(float64(t.UnixMilli()-l.from) / float64(l.to-l.from) * float64(l.pw))
		lab := []rune(timeLabel(t, span))
		x -= len(lab) / 2
		if x < 0 || x+len(lab) > l.pw || x < last+2 {
			continue
		}
		copy(row[x:], lab)
		last = x + len(lab)
	}
	return string(row)
}

func blankLines(w, h int) []string {
	out := make([]string, max(0, h))
	for i := range out {
		out[i] = strings.Repeat(" ", max(0, w))
	}
	return out
}
