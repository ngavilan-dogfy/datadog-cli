// Package series turns a time series into a few facts an agent (or a person)
// can act on: where it sits, when it changed level and by how much, its
// spikes and gaps, and where it ends — instead of hundreds of raw points.
package series

import (
	"math"
	"sort"
)

// Point is one value at a time in milliseconds. NaN means no data.
type Point struct {
	T int64   `json:"t"`
	V float64 `json:"v"`
}

// Shift is a lasting change of level.
type Shift struct {
	At   int64   `json:"at"`
	From float64 `json:"from"`
	To   float64 `json:"to"`
}

// Spike is a short excursion that comes back.
type Spike struct {
	At    int64   `json:"at"`
	Value float64 `json:"value"`
}

// Gap is a stretch without data.
type Gap struct {
	From int64 `json:"from"`
	To   int64 `json:"to"`
}

// Summary describes a series.
type Summary struct {
	Points  int     `json:"points"`
	Missing int     `json:"missing,omitempty"`
	First   float64 `json:"first"`
	Last    float64 `json:"last"`
	LastAt  int64   `json:"last_at"`
	Min     float64 `json:"min"`
	MinAt   int64   `json:"min_at"`
	Max     float64 `json:"max"`
	MaxAt   int64   `json:"max_at"`
	Mean    float64 `json:"mean"`
	Median  float64 `json:"median"`
	P95     float64 `json:"p95"`
	// Trend compares the last third with the first third: +0.5 is 50% up.
	Trend  float64 `json:"trend"`
	Shifts []Shift `json:"shifts,omitempty"`
	Spikes []Spike `json:"spikes,omitempty"`
	Gaps   []Gap   `json:"gaps,omitempty"`
	Flat   bool    `json:"flat,omitempty"`
}

// Describe summarizes points (sorted by time or not).
func Describe(pts []Point) Summary {
	var s Summary
	all := append([]Point(nil), pts...)
	sort.Slice(all, func(i, j int) bool { return all[i].T < all[j].T })
	var vals []float64
	var clean []Point
	for _, p := range all {
		if math.IsNaN(p.V) || math.IsInf(p.V, 0) {
			s.Missing++
			continue
		}
		clean = append(clean, p)
		vals = append(vals, p.V)
	}
	s.Points = len(clean)
	if len(clean) == 0 {
		return s
	}
	s.First, s.Last, s.LastAt = clean[0].V, clean[len(clean)-1].V, clean[len(clean)-1].T
	s.Min, s.Max = math.Inf(1), math.Inf(-1)
	sum := 0.0
	for _, p := range clean {
		sum += p.V
		if p.V < s.Min {
			s.Min, s.MinAt = p.V, p.T
		}
		if p.V > s.Max {
			s.Max, s.MaxAt = p.V, p.T
		}
	}
	s.Mean = sum / float64(len(clean))
	sorted := append([]float64(nil), vals...)
	sort.Float64s(sorted)
	s.Median = quantile(sorted, 0.5)
	s.P95 = quantile(sorted, 0.95)

	scale := math.Max(math.Abs(s.Median), math.Abs(s.Mean))
	if s.Max-s.Min <= 1e-9*math.Max(1, scale) {
		s.Flat = true
		return s
	}
	if n := len(clean); n >= 6 {
		third := n / 3
		a, b := mean(vals[:third]), mean(vals[n-third:])
		if math.Abs(a) > 1e-12 {
			s.Trend = (b - a) / math.Abs(a)
		} else if b != 0 {
			s.Trend = math.Copysign(1, b)
		}
	}
	s.Gaps = gaps(all)
	s.Shifts = shifts(clean)
	s.Spikes = spikes(clean, s.Shifts)
	return s
}

func mean(v []float64) float64 {
	if len(v) == 0 {
		return math.NaN()
	}
	t := 0.0
	for _, x := range v {
		t += x
	}
	return t / float64(len(v))
}

func quantile(sorted []float64, q float64) float64 {
	if len(sorted) == 0 {
		return math.NaN()
	}
	pos := q * float64(len(sorted)-1)
	lo := int(math.Floor(pos))
	hi := int(math.Ceil(pos))
	if lo == hi {
		return sorted[lo]
	}
	return sorted[lo] + (sorted[hi]-sorted[lo])*(pos-float64(lo))
}

// noise is a robust estimate of point-to-point noise: the median absolute
// difference between neighbors, which level shifts barely move.
func noise(v []float64) float64 {
	if len(v) < 3 {
		return 0
	}
	d := make([]float64, 0, len(v)-1)
	for i := 1; i < len(v); i++ {
		d = append(d, math.Abs(v[i]-v[i-1]))
	}
	sort.Float64s(d)
	med := quantile(d, 0.5)
	if med == 0 {
		// Mostly still with sudden jumps (sparse counts): the mean jump.
		med = mean(d)
	}
	return med / 0.954 // ≈ σ√2 for normal noise → σ
}

// shifts finds lasting level changes by binary segmentation: split where
// the means on each side differ the most, keep the split when that
// difference stands well above the noise and lasts, and look again inside
// each side.
func shifts(pts []Point) []Shift {
	v := make([]float64, len(pts))
	for i, p := range pts {
		v[i] = p.V
	}
	sigma := noise(v)
	span := 0.0
	if len(v) > 0 {
		lo, hi := v[0], v[0]
		for _, x := range v {
			lo, hi = math.Min(lo, x), math.Max(hi, x)
		}
		span = hi - lo
	}
	if sigma == 0 {
		sigma = span / 50
	}
	minLen := max(3, len(v)/15)
	prefix := make([]float64, len(v)+1)
	for i, x := range v {
		prefix[i+1] = prefix[i] + x
	}
	sumOf := func(a, b int) float64 { return prefix[b] - prefix[a] }
	var cuts []int
	var split func(lo, hi, depth int)
	split = func(lo, hi, depth int) {
		if depth > 4 || hi-lo < 2*minLen {
			return
		}
		best, bestK := 0.0, -1
		for k := lo + minLen; k <= hi-minLen; k++ {
			nl, nr := float64(k-lo), float64(hi-k)
			ml, mr := sumOf(lo, k)/nl, sumOf(k, hi)/nr
			score := math.Abs(ml-mr) * math.Sqrt(nl*nr/(nl+nr))
			if score > best {
				best, bestK = score, k
			}
		}
		if bestK < 0 {
			return
		}
		ml, mr := mean(v[lo:bestK]), mean(v[bestK:hi])
		diff := math.Abs(ml - mr)
		ref := math.Max(math.Abs(ml), math.Abs(mr))
		// Significant against the noise, big enough to matter, and a real
		// part of the series' range.
		if best < 6*sigma || (ref > 0 && diff/ref < 0.2) || diff < span*0.15 {
			return
		}
		cuts = append(cuts, bestK)
		split(lo, bestK, depth+1)
		split(bestK, hi, depth+1)
	}
	split(0, len(v), 0)
	sort.Ints(cuts)
	bounds := append(append([]int{0}, cuts...), len(v))
	// A steady ramp splits into "steps" that keep climbing inside: that's a
	// trend, not level changes.
	if len(cuts) > 0 {
		ramp := true
		for i := 1; i < len(bounds); i++ {
			if bounds[i]-bounds[i-1] < 4 || math.Abs(corrIndex(v[bounds[i-1]:bounds[i]])) < 0.6 {
				ramp = false
				break
			}
		}
		if ramp {
			return nil
		}
	}
	var out []Shift
	for i := 1; i < len(bounds)-1; i++ {
		before, after := v[bounds[i-1]:bounds[i]], v[bounds[i]:bounds[i+1]]
		from, to := median(before), median(after)
		if from == to {
			// Mostly zeros on both sides (sparse counts): the rate changed.
			from, to = mean(before), mean(after)
		}
		out = append(out, Shift{At: pts[bounds[i]].T, From: from, To: to})
	}
	return out
}

// corrIndex is the correlation between position and value: near ±1 when
// the values climb or fall steadily.
func corrIndex(v []float64) float64 {
	n := float64(len(v))
	if n < 3 {
		return 0
	}
	mx, my := (n-1)/2, mean(v)
	var sxy, sxx, syy float64
	for i, y := range v {
		dx, dy := float64(i)-mx, y-my
		sxy += dx * dy
		sxx += dx * dx
		syy += dy * dy
	}
	if sxx == 0 || syy == 0 {
		return 0
	}
	return sxy / math.Sqrt(sxx*syy)
}

func median(v []float64) float64 {
	s := append([]float64(nil), v...)
	sort.Float64s(s)
	return quantile(s, 0.5)
}

// spikes are points far from their neighborhood that don't start a shift.
func spikes(pts []Point, sh []Shift) []Spike {
	n := len(pts)
	if n < 7 {
		return nil
	}
	v := make([]float64, n)
	for i, p := range pts {
		v[i] = p.V
	}
	sigma := noise(v)
	if sigma == 0 {
		return nil
	}
	var out []Spike
	for i := range v {
		lo, hi := max(0, i-5), min(n, i+6)
		var around []float64
		for j := lo; j < hi; j++ {
			if j != i {
				around = append(around, v[j])
			}
		}
		m := median(around)
		if math.Abs(v[i]-m) < 8*sigma || math.Abs(v[i]-m) < 0.5*math.Abs(m) {
			continue
		}
		nearShift := false
		for _, s := range sh {
			if abs64(s.At-pts[i].T) <= abs64(pts[min(n-1, i+2)].T-pts[max(0, i-2)].T) {
				nearShift = true
			}
		}
		if !nearShift {
			out = append(out, Spike{At: pts[i].T, Value: v[i]})
		}
	}
	sort.Slice(out, func(i, j int) bool { return math.Abs(out[i].Value) > math.Abs(out[j].Value) })
	if len(out) > 3 {
		out = out[:3]
	}
	sort.Slice(out, func(i, j int) bool { return out[i].At < out[j].At })
	return out
}

func abs64(x int64) int64 {
	if x < 0 {
		return -x
	}
	return x
}

// gaps are stretches where the series stops reporting: a missing point
// (NaN) or a jump in time much longer than the usual step.
func gaps(pts []Point) []Gap {
	if len(pts) < 3 {
		return nil
	}
	var steps []int64
	for i := 1; i < len(pts); i++ {
		steps = append(steps, pts[i].T-pts[i-1].T)
	}
	sorted := append([]int64(nil), steps...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
	step := sorted[len(sorted)/2]
	var out []Gap
	start := int64(-1)
	for i, p := range pts {
		missing := math.IsNaN(p.V)
		if i > 0 && step > 0 && p.T-pts[i-1].T > 3*step && !math.IsNaN(pts[i-1].V) {
			out = append(out, Gap{From: pts[i-1].T + step, To: p.T})
		}
		switch {
		case missing && start < 0:
			start = p.T
		case !missing && start >= 0:
			out = append(out, Gap{From: start, To: p.T})
			start = -1
		}
	}
	if start >= 0 {
		out = append(out, Gap{From: start, To: pts[len(pts)-1].T})
	}
	// Only gaps worth mentioning: longer than two steps.
	var keep []Gap
	for _, g := range out {
		if step == 0 || g.To-g.From >= 2*step {
			keep = append(keep, g)
		}
	}
	return keep
}
