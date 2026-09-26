package series

import (
	"math"
	"math/rand"
	"strings"
	"testing"
)

func make1(n int, f func(i int) float64) []Point {
	var out []Point
	for i := 0; i < n; i++ {
		out = append(out, Point{T: int64(i) * 60000, V: f(i)})
	}
	return out
}

func noisy(r *rand.Rand, base, pct float64) float64 { return base * (1 + pct*(r.Float64()*2-1)) }

func TestStepIsOneShift(t *testing.T) {
	r := rand.New(rand.NewSource(1))
	s := Describe(make1(100, func(i int) float64 {
		if i < 60 {
			return noisy(r, 0.23, 0.08)
		}
		return noisy(r, 1.6, 0.08)
	}))
	if len(s.Shifts) != 1 || s.Shifts[0].At != 60*60000 {
		t.Fatalf("want one shift at minute 60, got %+v", s.Shifts)
	}
	if s.Shifts[0].To/s.Shifts[0].From < 6 {
		t.Errorf("shift ratio: %+v", s.Shifts[0])
	}
	txt := s.Text(Formatter{})
	if !strings.Contains(txt, "rose to") || !strings.Contains(txt, "×") {
		t.Errorf("text: %s", txt)
	}
}

func TestNoiseAloneIsNotAShift(t *testing.T) {
	r := rand.New(rand.NewSource(2))
	s := Describe(make1(120, func(int) float64 { return noisy(r, 50, 0.1) }))
	if len(s.Shifts) != 0 || len(s.Spikes) != 0 {
		t.Fatalf("noise read as events: %+v", s)
	}
	if !strings.HasPrefix(s.Text(Formatter{}), "around ") {
		t.Errorf("text: %s", s.Text(Formatter{}))
	}
}

func TestSpikeComesBack(t *testing.T) {
	r := rand.New(rand.NewSource(3))
	s := Describe(make1(80, func(i int) float64 {
		if i == 40 {
			return 100
		}
		return noisy(r, 10, 0.05)
	}))
	if len(s.Spikes) != 1 || s.Spikes[0].At != 40*60000 || len(s.Shifts) != 0 {
		t.Fatalf("want one spike at minute 40: %+v %+v", s.Spikes, s.Shifts)
	}
}

func TestRampIsATrendNotSteps(t *testing.T) {
	r := rand.New(rand.NewSource(4))
	s := Describe(make1(90, func(i int) float64 { return noisy(r, 10+float64(i), 0.02) }))
	if len(s.Shifts) != 0 {
		t.Fatalf("a ramp became steps: %+v", s.Shifts)
	}
	if s.Trend < 1 || !strings.Contains(s.Text(Formatter{}), "trending up") {
		t.Errorf("trend %.2f, text %s", s.Trend, s.Text(Formatter{}))
	}
}

func TestGapsAndFlat(t *testing.T) {
	s := Describe(make1(30, func(i int) float64 {
		if i >= 10 && i < 16 {
			return math.NaN()
		}
		return 5
	}))
	if !s.Flat || s.Missing != 6 {
		t.Fatalf("flat with a gap: %+v", s)
	}
	g := Describe(make1(30, func(i int) float64 {
		if i >= 10 && i < 16 {
			return math.NaN()
		}
		return float64(i % 7)
	}))
	if len(g.Gaps) != 1 || g.Gaps[0].From != 10*60000 {
		t.Fatalf("gap: %+v", g.Gaps)
	}
}
