package viz

import (
	"math"
	"math/rand"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

func series(n int, f func(i int) float64) []Point {
	start := time.Date(2026, 9, 26, 10, 0, 0, 0, time.UTC).UnixMilli()
	out := make([]Point, n)
	for i := range out {
		out[i] = Point{T: start + int64(i)*20_000, V: f(i)}
	}
	return out
}

func checkBox(t *testing.T, name string, lines []string, w, h int) {
	t.Helper()
	if len(lines) != h {
		t.Fatalf("%s: %d lines, want %d", name, len(lines), h)
	}
	for i, l := range lines {
		if got := ansi.StringWidth(l); got != w {
			t.Fatalf("%s: line %d is %d wide, want %d: %q", name, i, got, w, ansi.Strip(l))
		}
	}
}

func TestChartAlwaysFitsItsBox(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	data := map[string][]Series{
		"sine": {{Name: "a", Color: lipgloss.Color("4"), Points: series(180, func(i int) float64 { return math.Sin(float64(i) / 20) })}},
		"multi": {
			{Name: "a", Color: lipgloss.Color("4"), Points: series(180, func(i int) float64 { return rng.Float64() * 100 })},
			{Name: "b", Color: lipgloss.Color("5"), Points: series(180, func(i int) float64 { return rng.Float64() * 50 })},
			{Name: "c", Color: lipgloss.Color("6"), Points: series(180, func(i int) float64 { return rng.Float64() * 10 })},
		},
		"gaps": {{Name: "a", Color: lipgloss.Color("2"), Points: series(100, func(i int) float64 {
			if i%7 < 3 {
				return math.NaN()
			}
			return float64(i)
		})}},
		"one":      {{Name: "a", Color: lipgloss.Color("2"), Points: series(1, func(int) float64 { return 42 })}},
		"flat":     {{Name: "a", Color: lipgloss.Color("2"), Points: series(50, func(int) float64 { return 3 })}},
		"negative": {{Name: "a", Color: lipgloss.Color("1"), Points: series(90, func(i int) float64 { return float64(i - 45) })}},
		"empty":    {},
	}
	for name, ss := range data {
		for _, kind := range []Kind{Line, Area, Bars} {
			for _, style := range []Style{Blocks, Braille} {
				for _, size := range [][2]int{{4, 1}, {10, 3}, {40, 8}, {97, 13}, {200, 40}} {
					c := Chart{Width: size[0], Height: size[1], Series: ss, Kind: kind, Style: style, Cursor: 5,
						Unit: Unit{Family: "time", Scale: 0.001}, CursorBG: lipgloss.Color("0"),
						Markers: []Marker{{Value: 50, Color: lipgloss.Color("1")}}}
					checkBox(t, name, c.Render(), size[0], size[1])
				}
			}
		}
	}
}

func TestBrailleAndBlocksDrawSomething(t *testing.T) {
	s := []Series{{Name: "a", Color: lipgloss.Color("4"), Points: series(120, func(i int) float64 { return float64(i % 30) })}}
	br := ansi.Strip(strings.Join(Chart{Width: 60, Height: 10, Series: s, Style: Braille, Cursor: -1}.Render(), "\n"))
	if !strings.ContainsAny(br, "⠁⠂⠄⡀⠈⠐⠠⢀") && !strings.ContainsRune(br, '⣿') {
		hasBraille := false
		for _, r := range br {
			if r >= 0x2801 && r <= 0x28FF {
				hasBraille = true
			}
		}
		if !hasBraille {
			t.Error("braille chart has no braille dots")
		}
	}
	bl := ansi.Strip(strings.Join(Chart{Width: 60, Height: 10, Series: s, Style: Blocks, Cursor: -1}.Render(), "\n"))
	if !strings.ContainsAny(bl, "▀▄█") {
		t.Error("half-block chart draws nothing")
	}
	ar := ansi.Strip(strings.Join(Chart{Width: 60, Height: 10, Series: s, Kind: Area, Cursor: -1}.Render(), "\n"))
	if !strings.ContainsAny(ar, "▁▂▃▄▅▆▇█") {
		t.Error("area chart draws nothing")
	}
}

func TestAxisLabels(t *testing.T) {
	s := []Series{{Name: "lat", Color: lipgloss.Color("4"), Points: series(180, func(i int) float64 { return 0.1 + float64(i)/1000 })}}
	lines := Chart{Width: 60, Height: 8, Series: s, Unit: Unit{Family: "time", Name: "second", Scale: 1}, Cursor: -1, Loc: time.UTC}.Render()
	text := ansi.Strip(strings.Join(lines, "\n"))
	if !strings.Contains(text, "ms") {
		t.Errorf("y labels should use milliseconds:\n%s", text)
	}
	if !strings.Contains(ansi.Strip(lines[len(lines)-1]), "10:") {
		t.Errorf("x labels should show times:\n%s", text)
	}
	if strings.Contains(text, "e-") {
		t.Errorf("float noise in labels:\n%s", text)
	}
}

func TestFormat(t *testing.T) {
	cases := []struct {
		v    float64
		u    Unit
		want string
	}{
		{0, Unit{}, "0"}, {0.0042, Unit{}, "0.0042"}, {3.14159, Unit{}, "3.14"}, {12.34, Unit{}, "12.3"},
		{999, Unit{}, "999"}, {1234, Unit{}, "1234"}, {12345, Unit{}, "12.3k"}, {2.5e6, Unit{}, "2.5M"},
		{0.35, Unit{Family: "time", Scale: 1}, "350ms"}, {1500, Unit{Family: "time", Scale: 0.001}, "1.5s"},
		{2.5e-5, Unit{Family: "time", Scale: 1}, "25µs"}, {1536, Unit{Family: "bytes", Scale: 1}, "1.5KiB"},
		{73.4, Unit{Family: "percentage", Name: "percent"}, "73.4%"}, {12, Unit{Short: "req"}, "12req"},
		{math.NaN(), Unit{}, "–"},
	}
	for _, c := range cases {
		if got := Format(c.v, c.u); got != c.want {
			t.Errorf("Format(%v, %+v) = %q, want %q", c.v, c.u, got, c.want)
		}
	}
}

func TestSmallPieces(t *testing.T) {
	if w := ansi.StringWidth(Sparkline([]float64{1, 5, 2, math.NaN(), 9}, 12, lipgloss.Color("2"))); w != 12 {
		t.Errorf("sparkline width %d", w)
	}
	for _, v := range []float64{0, 0.3, 0.5, 1, 7} {
		if w := ansi.StringWidth(HBar(v, 1, 17, lipgloss.Color("4"))); w != 17 {
			t.Errorf("hbar(%v) width %d", v, w)
		}
	}
	lines, w := BigText("99.9%", lipgloss.Color("2"))
	checkBox(t, "bigtext", lines, w, 3)
}

func TestSpikesAreNeverClipped(t *testing.T) {
	pts := series(180, func(i int) float64 { return 2 })
	pts[150].V = 11.5 // one-sample spike, averaged away at low resolution
	for _, style := range []Style{Blocks, Braille} {
		c := Chart{Width: 80, Height: 12, Series: []Series{{Name: "a", Color: lipgloss.Color("4"), Points: pts}}, Style: style, Cursor: -1}
		l, vals := c.layout()
		top := 0.0
		for _, v := range vals[0] {
			top = math.Max(top, v)
		}
		if l.hi < top {
			t.Fatalf("style %v: axis tops at %v, below the drawn peak %v", style, l.hi, top)
		}
	}
}

func TestMarkersFarFromDataDontSquashIt(t *testing.T) {
	pts := series(60, func(i int) float64 { return 0.004 + float64(i%5)/1000 }) // ~4 ms
	markers := []Marker{{Value: 0.75, Color: lipgloss.Color("3")}, {Value: 1.5, Color: lipgloss.Color("1")}}
	c := Chart{Width: 60, Height: 10, Series: []Series{{Name: "p50", Color: lipgloss.Color("4"), Points: pts}}, Markers: markers, Cursor: -1}
	l, _ := c.layout()
	if l.hi > 0.1 {
		t.Errorf("axis stretched to %v by far-away markers", l.hi)
	}
	c.FitMarkers = true
	if l, _ := c.layout(); l.hi < 1.5 {
		t.Errorf("FitMarkers: axis tops at %v, want ≥ 1.5", l.hi)
	}
}

func TestSparklineStretchesFewPoints(t *testing.T) {
	s := ansi.Strip(Sparkline([]float64{1, 2, 3}, 12, nil))
	if strings.Contains(s, " ") || len([]rune(s)) != 12 {
		t.Fatalf("3 points over 12 cells should fill them all: %q", s)
	}
	gap := ansi.Strip(Sparkline([]float64{1, math.NaN(), 3}, 9, nil))
	if !strings.Contains(gap, "   ") {
		t.Fatalf("a missing point should stay a gap: %q", gap)
	}
}

// With a little less than one point per column, bars must still touch:
// each point covers its interval.
func TestBarsCoverTheirInterval(t *testing.T) {
	var pts []Point
	for i := 0; i < 50; i++ {
		pts = append(pts, Point{T: int64(i) * 60000, V: 10})
	}
	c := Chart{Width: 60, Height: 4, Kind: Bars, Series: []Series{{Name: "a", Points: pts}}, NoXAxis: true, NoYAxis: true}
	rows := c.Render()
	bottom := strings.TrimRight(ansi.Strip(rows[len(rows)-1]), " ")
	if strings.Contains(strings.TrimLeft(bottom, " "), " ") {
		t.Fatalf("gaps between bars: %q", bottom)
	}
	// A real hole in the data stays a hole.
	pts[25].V = math.NaN()
	pts[26].V = math.NaN()
	pts[27].V = math.NaN()
	c.Series[0].Points = pts
	rows = c.Render()
	if bottom := strings.TrimSpace(ansi.Strip(rows[len(rows)-1])); !strings.Contains(bottom, " ") {
		t.Fatalf("missing points should leave a gap: %q", bottom)
	}
}
