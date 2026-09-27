package cmd

import (
	"fmt"
	"math"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/ngavilan-dogfy/datadog-cli/datadog"
	"github.com/ngavilan-dogfy/datadog-cli/internal/series"
)

// The analysis turns the facts into an investigation: verdicts on each
// signal (compared with the baseline, with absolute floors so a quiet
// service's noise isn't a finding), the onset, a timeline, leads ranked by
// how close in time and how strong their evidence is, what looked normal,
// and what would have caught it sooner. Pure functions over the facts.

// investigation is the report.
type investigation struct {
	Question    string          `json:"question,omitempty"`
	Scope       []string        `json:"scope"`
	From        time.Time       `json:"from"`
	To          time.Time       `json:"to"`
	BaseFrom    time.Time       `json:"baseline_from"`
	BaseTo      time.Time       `json:"baseline_to"`
	Compare     string          `json:"compare"`
	Status      string          `json:"status"` // degraded, recovered, normal, unknown
	Summary     string          `json:"summary"`
	Onset       *time.Time      `json:"onset,omitempty"`
	Timeline    []invEvent      `json:"timeline"`
	Leads       []invLead       `json:"leads"`
	Findings    []invFinding    `json:"findings"`
	Normal      []invNote       `json:"checked_normal"`
	Gaps        []string        `json:"could_not_check,omitempty"`
	Suggestions []invSuggestion `json:"suggestions,omitempty"`
	Next        []string        `json:"next_steps"`
	References  []ref           `json:"references"`
	Notes       []string        `json:"notes,omitempty"`

	refs  refList
	clock func(int64) string
}

type invEvent struct {
	At   time.Time `json:"at"`
	Kind string    `json:"kind"` // onset, change, deploy, alert, logs, incident, config, event
	What string    `json:"what"`
	Refs []int     `json:"refs,omitempty"`
}

type invFinding struct {
	ID       string     `json:"id"`
	Severity string     `json:"severity"` // high, medium, low, info
	Area     string     `json:"area"`     // errors, latency, traffic, endpoints, dependencies, changes, logs, alerting
	Service  string     `json:"service"`
	Text     string     `json:"text"`
	At       *time.Time `json:"at,omitempty"`
	Refs     []int      `json:"refs,omitempty"`
}

type invLead struct {
	Service    string   `json:"service,omitempty"`
	Title      string   `json:"title"`
	Confidence string   `json:"confidence"` // high, medium, low
	For        []string `json:"evidence_for"`
	Against    []string `json:"evidence_against,omitempty"`
	Verify     string   `json:"verify,omitempty"`
	Refs       []int    `json:"refs,omitempty"`
	score      float64
}

type invNote struct {
	Text string `json:"text"`
	Refs []int  `json:"refs,omitempty"`
}

type invSuggestion struct {
	What    string `json:"what"`
	Why     string `json:"why"`
	Command string `json:"command,omitempty"`
	Refs    []int  `json:"refs,omitempty"`
}

// Thresholds: a change has to be both relative and absolute to count.
const (
	minErrorsToJudge  = 10                     // errors in the window before an error rate is judged
	errRateFactor     = 2.0                    // the error rate at least doubled…
	errRateMinPoints  = 0.005                  // …and rose by at least half a percentage point
	errRateFromZero   = 0.01                   // from no errors: at least 1% of requests
	latencyFactor     = 1.5                    // p95 at least ×1.5…
	latencyMinDelta   = 50 * time.Millisecond  // …and 50 ms slower
	trafficDropFactor = 0.5                    // requests halved…
	trafficSurge      = 2.0                    // …or doubled,
	minRequests       = 100                    // on at least this many requests
	nearOnset         = 15 * time.Minute       // a change this close before the onset is a strong lead
	beforeOnset       = 60 * time.Minute       // and a weaker one up to this far
	minPointHits      = 5                      // requests in a point before its error rate counts
	latencySpikeMin   = 500 * time.Millisecond // a latency spike is at least this far above the usual (and ×5)
	minSpikeRequests  = 100                    // requests around a latency spike (±1 min) for it to count: 5% of them is 5 slow requests
)

// svcState is what the analysis learned about one service.
type svcState struct {
	f        *invServiceFacts
	label    string // "api", "api · POST /v1/orders"
	status   string
	onset    time.Time
	started  bool // degraded since before the window
	symptoms []string
	bad      map[string]bool // errors, latency, traffic, logs
	firstBad map[string]time.Time
	back     map[string]bool // signals that went bad and came back by the end
	alertAt  time.Time       // first alert on the service after the onset
	alerted  string
	changes  []invEvent // changes of the service itself, with their time
	deps     []depState // dependencies that got worse
	peaks    []int64    // when its p95 spiked (ms)
	// everywhere: most of its endpoints spiked together
	everywhere bool
	jobs       []string // job-like endpoints that ran slow at the spikes
	vitals     []string // vital signs that moved with it: "event loop delay 519 ms at 10:56"
}

// analyzeInvestigation builds the report from the facts.
func analyzeInvestigation(f *invFacts) *investigation {
	inv := &investigation{
		Question: f.Question, From: f.From, To: f.To, BaseFrom: f.baseFrom(), BaseTo: f.baseTo(),
		Compare: fmtDuration(f.Compare), Notes: f.Notes, clock: clockFor(f.From.Add(-f.Lookback), f.To),
	}
	var states []*svcState
	for _, s := range f.Services {
		st := &svcState{f: s, label: s.Name, bad: map[string]bool{}, firstBad: map[string]time.Time{}, back: map[string]bool{}}
		if s.Resource != "" {
			st.label += " · " + s.Resource
		}
		scope := st.label
		if s.Env != "" {
			scope += " (env:" + s.Env + ")"
		}
		inv.Scope = append(inv.Scope, scope)
		inv.analyzeGolden(st, f)
		inv.analyzeLogs(st, f)
		inv.analyzeEndpoints(st, f)
		inv.analyzeDependencies(st, f)
		inv.analyzeInstances(st, f)
		inv.analyzeVitals(st, f)
		states = append(states, st)
	}
	for _, st := range states {
		inv.analyzeAlerting(st, f)
	}
	inv.analyzeChanges(states, f)
	inv.analyzeIncidents(f)
	for _, st := range states {
		inv.leadsFor(st, f)
		inv.suggestionsFor(st, f)
		inv.nextFor(st, f)
	}
	inv.gaps(f, states)
	inv.summarize(states)

	sort.SliceStable(inv.Timeline, func(i, j int) bool { return inv.Timeline[i].At.Before(inv.Timeline[j].At) })
	sort.SliceStable(inv.Leads, func(i, j int) bool { return inv.Leads[i].score > inv.Leads[j].score })
	for i := range inv.Findings {
		inv.Findings[i].ID = fmt.Sprintf("F%d", i+1)
	}
	inv.References = inv.refs.list
	if inv.References == nil {
		inv.References = []ref{}
	}
	for _, empty := range []*[]invEvent{&inv.Timeline} {
		if *empty == nil {
			*empty = []invEvent{}
		}
	}
	if inv.Leads == nil {
		inv.Leads = []invLead{}
	}
	if inv.Findings == nil {
		inv.Findings = []invFinding{}
	}
	if inv.Normal == nil {
		inv.Normal = []invNote{}
	}
	if inv.Next == nil {
		inv.Next = []string{}
	}
	return inv
}

func (inv *investigation) finding(sev, area, service, text string, at time.Time, refs ...int) {
	fi := invFinding{Severity: sev, Area: area, Service: service, Text: text, Refs: nonZero(refs)}
	if !at.IsZero() {
		t := at
		fi.At = &t
	}
	inv.Findings = append(inv.Findings, fi)
}

func (inv *investigation) normal(text string, refs ...int) {
	inv.Normal = append(inv.Normal, invNote{Text: text, Refs: nonZero(refs)})
}

func (inv *investigation) event(at time.Time, kind, what string, refs ...int) {
	inv.Timeline = append(inv.Timeline, invEvent{At: at, Kind: kind, What: what, Refs: nonZero(refs)})
}

func nonZero(ns []int) []int {
	var out []int
	for _, n := range ns {
		if n > 0 {
			out = append(out, n)
		}
	}
	return out
}

func (inv *investigation) at(t time.Time) string { return inv.clock(t.UnixMilli()) }

// ─── golden signals ──────────────────────────────────────────────

func (inv *investigation) analyzeGolden(st *svcState, f *invFacts) {
	s := st.f
	if s.Hits == nil {
		return
	}
	hitsNow, hitsBase := total(s.Hits.Points), total(s.Hits.Base)
	errsNow, errsBase := 0.0, 0.0
	if s.Errors != nil {
		errsNow, errsBase = total(s.Errors.Points), total(s.Errors.Base)
	}
	win := f.To.Sub(f.From)
	hitsRef := inv.refs.add(st.label+": requests", metricLink(s.Hits.Query, f.From, f.To), s.Hits.Query)
	svcRef := inv.refs.add(st.label+" in APM", serviceLink(s.Name, s.Env, s.Entry, f.From, f.To), "")

	// Traffic: against the baseline, or a step inside the window (a drop
	// halfway through is diluted in the window's total).
	ratio := safeRatio(hitsNow, hitsBase)
	hitsSum := series.Describe(s.Hits.Points)
	dropAt := firstShift(hitsSum, func(sh series.Shift) bool { return sh.From >= 5 && sh.To <= sh.From*trafficDropFactor })
	surgeAt := firstShift(hitsSum, func(sh series.Shift) bool { return sh.To >= 5 && sh.To >= sh.From*trafficSurge })
	switch {
	case (hitsBase >= minRequests && ratio <= trafficDropFactor) || (!dropAt.IsZero() && hitsNow+hitsBase >= minRequests):
		st.bad["traffic"] = true
		st.mark("traffic", dropAt)
		overall := fmt.Sprintf("%s (%s before: %s)", rate(hitsNow, win), inv.Compare, rate(hitsBase, win))
		txt := "Traffic fell to " + overall
		sym := fmt.Sprintf("traffic fell %s", changeText(hitsBase, hitsNow))
		if !dropAt.IsZero() {
			// The step inside the window says more than the totals, which
			// can even go the other way.
			sh := shiftAt(hitsSum, dropAt)
			txt = fmt.Sprintf("Traffic dropped at %s (%s → %s a point)", inv.at(dropAt), fmtCount(int(sh.From)), fmtCount(int(sh.To)))
			sym = fmt.Sprintf("traffic fell %s at %s", changeText(sh.From, sh.To), inv.at(dropAt))
			if recovered(hitsSum, sh) {
				txt += " and came back by the end"
				st.back["traffic"] = true
			}
			txt += "; over the window " + overall
		}
		inv.finding("high", "traffic", s.Name, txt+".", dropAt, hitsRef)
		st.symptoms = append(st.symptoms, sym)
	case (hitsNow >= minRequests && ratio >= trafficSurge) || (!surgeAt.IsZero() && hitsNow >= minRequests):
		st.bad["traffic"] = true
		st.mark("traffic", surgeAt)
		overall := fmt.Sprintf("%s (%s before: %s)", rate(hitsNow, win), inv.Compare, rate(hitsBase, win))
		txt := "Traffic rose to " + overall
		sym := fmt.Sprintf("traffic %s", changeText(hitsBase, hitsNow))
		if !surgeAt.IsZero() {
			sh := shiftAt(hitsSum, surgeAt)
			txt = fmt.Sprintf("Traffic surged at %s (%s → %s a point)", inv.at(surgeAt), fmtCount(int(sh.From)), fmtCount(int(sh.To)))
			sym = fmt.Sprintf("traffic %s at %s", changeText(sh.From, sh.To), inv.at(surgeAt))
			if recovered(hitsSum, sh) {
				txt += " and came back by the end"
				st.back["traffic"] = true
			}
			txt += "; over the window " + overall
		}
		inv.finding("medium", "traffic", s.Name, txt+".", surgeAt, hitsRef)
		st.symptoms = append(st.symptoms, sym)
	case hitsNow == 0 && hitsBase == 0:
		inv.normal(fmt.Sprintf("%s served no requests in the window, nor %s before.", st.label, inv.Compare), hitsRef)
	default:
		inv.normal(fmt.Sprintf("Traffic %s (%s before: %s).", rate(hitsNow, win), inv.Compare, rate(hitsBase, win)), hitsRef)
	}

	// Error rate.
	if s.Errors != nil {
		errRef := inv.refs.add(st.label+": errors", metricLink(s.Errors.Query, f.From, f.To), s.Errors.Query)
		erNow, erBase := safeDiv(errsNow, hitsNow), safeDiv(errsBase, hitsBase)
		rateSeries := errorRateSeries(s.Hits.Points, s.Errors.Points)
		rateSum := series.Describe(rateSeries)
		bad := errsNow >= minErrorsToJudge &&
			((erBase > 0 && erNow >= erBase*errRateFactor && erNow-erBase >= errRateMinPoints) || (erBase == 0 && erNow >= errRateFromZero))
		// A step inside the window counts even when the whole window's rate
		// is diluted by the calm part before it.
		at := firstShift(rateSum, func(sh series.Shift) bool {
			return sh.To >= math.Max(sh.From*errRateFactor, sh.From+errRateMinPoints) && sh.To >= errRateMinPoints
		})
		if !bad && !at.IsZero() && errsNow >= minErrorsToJudge {
			bad = true
		}
		if bad {
			st.bad["errors"] = true
			st.mark("errors", at)
			txt := fmt.Sprintf("Error rate %s (%s errors of %s requests), against %s %s before", pct(erNow), fmtCount(int(errsNow)), fmtCount(int(hitsNow)), pct(erBase), inv.Compare)
			if !at.IsZero() {
				sh := shiftAt(rateSum, at)
				txt += fmt.Sprintf("; it rose at %s (%s → %s)", inv.at(at), pct(sh.From), pct(sh.To))
				if recovered(rateSum, sh) {
					txt += fmt.Sprintf(" and came back to %s by the end", pct(rateSum.Last))
					st.back["errors"] = true
				}
			} else if since := startedBefore(s.Before["errors"], f.From, func(pts []series.Point) []series.Point {
				return errorRateSeries(s.Before["hits"], pts)
			}, func(sh series.Shift) bool {
				return sh.To >= math.Max(sh.From*errRateFactor, sh.From+errRateMinPoints) && sh.To >= errRateMinPoints
			}); !since.IsZero() {
				at = since
				st.mark("errors", at)
				txt += fmt.Sprintf("; it rose at %s, before the window", inv.at(at))
			} else if erBase > 0 {
				txt += " — already high when the window starts"
				st.started = true
			}
			inv.finding("high", "errors", s.Name, txt+".", at, errRef, svcRef)
			sym := "errors " + changeText(math.Max(erBase, 1e-9), erNow)
			if erBase == 0 {
				sym = "errors appeared"
			}
			if !at.IsZero() {
				sym += " at " + inv.at(at)
			}
			sym += fmt.Sprintf(" (%s → %s of requests)", pct(erBase), pct(erNow))
			st.symptoms = append(st.symptoms, sym)
		} else {
			inv.normal(fmt.Sprintf("Error rate %s (%s before: %s).", pct(erNow), inv.Compare, pct(erBase)), errRef)
		}
	}

	// Latency: a lasting rise (the typical level), a step inside the
	// window, or spikes far above the usual — each dates the trouble.
	if s.P95 != nil && len(s.P95.Points) > 0 {
		latRef := inv.refs.add(st.label+": p95 latency", metricLink(s.P95.Query, f.From, f.To), s.P95.Query)
		latSum, baseSum := series.Describe(s.P95.Points), series.Describe(s.P95.Base)
		now, base := seconds(latSum.Median), seconds(baseSum.Median)
		sustained := base > 0 && float64(now) >= float64(base)*latencyFactor && now-base >= latencyMinDelta
		at := firstShift(latSum, func(sh series.Shift) bool {
			return sh.To >= sh.From*latencyFactor && seconds(sh.To-sh.From) >= latencyMinDelta
		})
		// A spike counts on enough requests: at night, one slow request is
		// the whole p95.
		requestsAt := map[int64]float64{}
		if s.Hits != nil {
			for _, p := range s.Hits.Points {
				requestsAt[p.T] = p.V
			}
		}
		enough := func(at int64) bool {
			if s.Hits == nil {
				return true
			}
			n := 0.0
			for t, v := range requestsAt {
				if d := t - at; d >= -60*1000 && d <= 60*1000 {
					n += v
				}
			}
			return n >= minSpikeRequests
		}
		var peaks []series.Spike
		for _, sp := range latSum.Spikes {
			if v := seconds(sp.Value); base > 0 && v >= max(5*base, base+latencySpikeMin) && enough(sp.At) &&
				lasted(s.P95.Points, sp.At, float64(max(3*base, base+latencySpikeMin))/float64(time.Second)) >= time.Minute {
				peaks = append(peaks, sp)
			}
		}
		if sustained || !at.IsZero() || len(peaks) > 0 {
			st.bad["latency"] = true
			txt := fmt.Sprintf("p95 latency typically %s, against %s %s before", fmtDur(now), fmtDur(base), inv.Compare)
			var sym []string
			if sustained {
				sym = append(sym, fmt.Sprintf("p95 latency %s (%s → %s)", changeText(float64(base), float64(now)), fmtDur(base), fmtDur(now)))
			}
			if !at.IsZero() {
				st.mark("latency", at)
				sh := shiftAt(latSum, at)
				txt += fmt.Sprintf("; it rose at %s (%s → %s)", inv.at(at), fmtDur(seconds(sh.From)), fmtDur(seconds(sh.To)))
				if recovered(latSum, sh) {
					txt += fmt.Sprintf(" and came back to %s by the end", fmtDur(seconds(latSum.Last)))
					st.back["latency"] = true
				}
				if !sustained {
					sym = append(sym, fmt.Sprintf("p95 latency %s at %s (%s → %s)", changeText(sh.From, sh.To), inv.at(at), fmtDur(seconds(sh.From)), fmtDur(seconds(sh.To))))
				}
			}
			if len(peaks) > 0 {
				top := peaks[0]
				var times []string
				for _, sp := range peaks {
					if sp.Value > top.Value {
						top = sp
					}
					times = append(times, inv.at(time.UnixMilli(sp.At)))
				}
				st.mark("latency", time.UnixMilli(peaks[0].At))
				for _, sp := range peaks {
					st.peaks = append(st.peaks, sp.At)
				}
				txt += fmt.Sprintf("; spikes up to %s (at %s)", fmtDur(seconds(top.Value)), strings.Join(times, ", "))
				sym = append(sym, fmt.Sprintf("p95 latency spiked to %s (%s)", fmtDur(seconds(top.Value)), strings.Join(times, ", ")))
				if !sustained && at.IsZero() && seconds(latSum.Last) < base*2 {
					st.back["latency"] = true
				}
			}
			if at.IsZero() && len(peaks) == 0 {
				// Up the whole window: when did it start?
				if since := startedBefore(s.Before["p95"], f.From, nil, func(sh series.Shift) bool {
					return sh.To >= sh.From*latencyFactor && seconds(sh.To-sh.From) >= latencyMinDelta
				}); !since.IsZero() {
					at = since
					st.mark("latency", at)
					txt += fmt.Sprintf("; it rose at %s, before the window", inv.at(at))
				} else {
					st.started = true
				}
			}
			inv.finding("high", "latency", s.Name, txt+".", st.firstBad["latency"], latRef, svcRef)
			st.symptoms = append(st.symptoms, sym...)
		} else if base > 0 || now > 0 {
			inv.normal(fmt.Sprintf("p95 latency typically %s (%s before: %s).", fmtDur(now), inv.Compare, fmtDur(base)), latRef)
		}
	}
}

// lasted is how long a series stayed above a level around a moment: its
// points above it, next to each other, times the step between points.
func lasted(pts []series.Point, at int64, level float64) time.Duration {
	if len(pts) < 2 {
		return 0
	}
	sorted := append([]series.Point(nil), pts...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].T < sorted[j].T })
	step := time.Duration(sorted[len(sorted)-1].T-sorted[0].T) * time.Millisecond / time.Duration(len(sorted)-1)
	i := sort.Search(len(sorted), func(i int) bool { return sorted[i].T >= at })
	if i == len(sorted) {
		i--
	}
	n := 0
	for j := i; j >= 0 && sorted[j].V >= level; j-- {
		n++
	}
	for j := i + 1; j < len(sorted) && sorted[j].V >= level; j++ {
		n++
	}
	return time.Duration(n) * step
}

// startedBefore dates a trouble that was on when the window started: the
// last matching level change in the hours before it (zero when there's
// none, or the data doesn't say). derive turns raw points into the signal
// (errors into an error rate).
func startedBefore(pts []series.Point, from time.Time, derive func([]series.Point) []series.Point, ok func(series.Shift) bool) time.Time {
	if len(pts) == 0 {
		return time.Time{}
	}
	if derive != nil {
		pts = derive(pts)
	}
	sum := series.Describe(pts)
	var last time.Time
	for _, sh := range sum.Shifts {
		if t := time.UnixMilli(sh.At); t.Before(from) && ok(sh) {
			last = t
		}
	}
	return last
}

// mark records when a signal went bad, and moves the onset earlier.
func (st *svcState) mark(signal string, at time.Time) {
	if at.IsZero() {
		return
	}
	st.firstBad[signal] = at
	if st.onset.IsZero() || at.Before(st.onset) {
		st.onset = at
	}
}

// errorRateSeries is errors/requests point by point, where requests are
// enough to say.
func errorRateSeries(hits, errs []series.Point) []series.Point {
	e := map[int64]float64{}
	for _, p := range errs {
		if p.V == p.V {
			e[p.T] = p.V
		}
	}
	out := make([]series.Point, 0, len(hits))
	for _, p := range hits {
		if p.V < minPointHits || p.V != p.V {
			out = append(out, series.Point{T: p.T, V: math.NaN()})
			continue
		}
		out = append(out, series.Point{T: p.T, V: e[p.T] / p.V})
	}
	return out
}

// firstShift is when the first level change matching ok happened.
func firstShift(s series.Summary, ok func(series.Shift) bool) time.Time {
	for _, sh := range s.Shifts {
		if ok(sh) {
			return time.UnixMilli(sh.At)
		}
	}
	return time.Time{}
}

func shiftAt(s series.Summary, at time.Time) series.Shift {
	for _, sh := range s.Shifts {
		if sh.At == at.UnixMilli() {
			return sh
		}
	}
	return series.Shift{}
}

// recovered: after the shift, the series came back near where it was.
func recovered(s series.Summary, sh series.Shift) bool {
	if sh.To == sh.From {
		return false
	}
	return math.Abs(s.Last-sh.From) < math.Abs(sh.To-sh.From)*0.3
}

func seconds(v float64) time.Duration { return time.Duration(v * float64(time.Second)) }

func safeRatio(now, before float64) float64 {
	if before == 0 {
		if now == 0 {
			return 1
		}
		return math.Inf(1)
	}
	return now / before
}

func safeDiv(a, b float64) float64 {
	if b == 0 {
		return 0
	}
	return a / b
}

// rate is a count over a window as a per-minute (or per-hour) rate.
func rate(n float64, window time.Duration) string {
	perMin := n / math.Max(window.Minutes(), 1)
	switch {
	case perMin >= 1:
		return fmtCount(int(math.Round(perMin))) + "/min"
	case perMin*60 >= 1:
		return fmt.Sprintf("%.0f/h", perMin*60)
	}
	return fmt.Sprintf("%s in %s", fmtCount(int(n)), fmtDuration(window.Round(time.Minute)))
}

func pct(v float64) string {
	switch p := v * 100; {
	case p == 0:
		return "0%"
	case p < 0.1:
		return fmt.Sprintf("%.2f%%", p)
	case p < 10:
		return fmt.Sprintf("%.1f%%", p)
	default:
		return fmt.Sprintf("%.0f%%", p)
	}
}

func fmtDur(d time.Duration) string {
	switch {
	case d <= 0:
		return "0 ms"
	case d < time.Millisecond:
		return fmt.Sprintf("%.2f ms", float64(d)/float64(time.Millisecond))
	case d < 10*time.Second:
		if d < time.Second {
			return fmt.Sprintf("%.0f ms", float64(d)/float64(time.Millisecond))
		}
		return fmt.Sprintf("%.2f s", d.Seconds())
	case d < time.Minute:
		return fmt.Sprintf("%.0f s", d.Seconds())
	}
	return d.Round(time.Second).String()
}

func fmtAgo(d time.Duration) string {
	d = d.Round(time.Minute)
	switch {
	case d < time.Minute:
		return "less than a minute"
	case d < time.Hour:
		return fmt.Sprintf("%d min", int(d.Minutes()))
	case d%time.Hour == 0:
		return fmt.Sprintf("%dh", int(d.Hours()))
	}
	return fmt.Sprintf("%dh%02d", int(d.Hours()), int(d.Minutes())%60)
}

// ─── logs ────────────────────────────────────────────────────────

func (inv *investigation) analyzeLogs(st *svcState, f *invFacts) {
	s := st.f
	r := s.ErrorLogs
	if r == nil {
		return
	}
	logRef := inv.refs.add(st.label+": error logs", logsLink(s.LogQuery, f.From, f.To), s.LogQuery)
	grew := r.Total >= 50 && r.Compare != "" && float64(r.Total) >= 2*float64(max(r.TotalBefore, 1))
	var newOnes []logPattern
	for _, p := range r.Patterns {
		if p.New && p.Estimate >= 5 {
			newOnes = append(newOnes, p)
		}
	}
	vol := series.Describe(r.volume)
	at := firstShift(vol, func(sh series.Shift) bool { return sh.To >= 2*math.Max(sh.From, 1) && sh.To-sh.From >= 5 })
	if grew || len(newOnes) > 0 {
		if grew {
			st.bad["logs"] = true
			st.mark("logs", at)
			change := changeText(float64(max(r.TotalBefore, 1)), float64(r.Total))
			st.symptoms = append(st.symptoms, fmt.Sprintf("error logs %s (%s, %s before: %s)", change, fmtCount(r.Total), inv.Compare, fmtCount(r.TotalBefore)))
			txt := fmt.Sprintf("%s error logs, %s the %s of %s before", fmtCount(r.Total), change, fmtCount(r.TotalBefore), inv.Compare)
			if !at.IsZero() {
				txt += fmt.Sprintf("; they rose at %s", inv.at(at))
			}
			inv.finding("medium", "logs", s.Name, txt+".", at, logRef)
		}
		for i, p := range newOnes {
			if i >= 3 {
				break
			}
			q := s.LogQuery
			pref := inv.refs.add(fmt.Sprintf("%s: new error %q", st.label, oneLine(p.Pattern, 60)), logsLink(q, p.First, f.To), q)
			sev := "medium"
			if p.Share < 5 {
				sev = "low"
			}
			inv.finding(sev, "logs", s.Name, fmt.Sprintf("New error pattern, absent %s before: %q — ~%s logs (%.0f%% of errors), first at %s, last at %s.",
				inv.Compare, oneLine(p.Pattern, 160), fmtCount(p.Estimate), p.Share, inv.at(p.First), inv.at(p.Last)), p.First, pref)
			if !p.First.IsZero() {
				inv.event(p.First, "logs", fmt.Sprintf("%s: new error %q first logged", st.label, oneLine(p.Pattern, 80)), pref)
			}
		}
		return
	}
	switch {
	case r.Total == 0:
		inv.normal(fmt.Sprintf("No error logs from %s.", st.f.Name), logRef)
	case r.Compare != "":
		inv.normal(fmt.Sprintf("%s error logs (%s before: %s), no new patterns.", fmtCount(r.Total), inv.Compare, fmtCount(r.TotalBefore)), logRef)
	default:
		inv.normal(fmt.Sprintf("%s error logs.", fmtCount(r.Total)), logRef)
	}
}

// ─── endpoints ───────────────────────────────────────────────────

func (inv *investigation) analyzeEndpoints(st *svcState, f *invFacts) {
	s := st.f
	if len(s.Resources) == 0 || s.Resource != "" {
		return
	}
	if st.bad["errors"] {
		extra := 0.0
		for _, r := range s.Resources {
			if d := r.Errors - r.BaseErrors; d > 0 {
				extra += d
			}
		}
		rs := append([]invGroup(nil), s.Resources...)
		sort.SliceStable(rs, func(i, j int) bool { return rs[i].Errors-rs[i].BaseErrors > rs[j].Errors-rs[j].BaseErrors })
		var parts []string
		var refs []int
		for i, r := range rs {
			d := r.Errors - r.BaseErrors
			if i >= 3 || d <= 0 || (extra > 0 && d/extra < 0.1) {
				break
			}
			q := spanQueryFor(s, r.Name) + " status:error"
			refs = append(refs, inv.refs.add(fmt.Sprintf("%s · %s: failing requests", s.Name, r.Name), spansLink(q, f.From, f.To), q))
			share := ""
			if extra > 0 {
				share = fmt.Sprintf(", %.0f%% of the extra errors", d/extra*100)
			}
			parts = append(parts, fmt.Sprintf("%s: %s errors of %s requests (%s before: %s%s)", r.Name, fmtCount(int(r.Errors)), fmtCount(int(r.Count)), inv.Compare, fmtCount(int(r.BaseErrors)), share))
		}
		if len(parts) > 0 {
			inv.finding("high", "endpoints", s.Name, "The errors come from "+strings.Join(parts, "; ")+".", time.Time{}, refs...)
		}
	}
	if st.bad["latency"] {
		var parts []string
		var refs []int
		rs := append([]invGroup(nil), s.Resources...)
		sort.SliceStable(rs, func(i, j int) bool {
			return float64(rs[i].P95-rs[i].BaseP95)*rs[i].Count > float64(rs[j].P95-rs[j].BaseP95)*rs[j].Count
		})
		for _, r := range rs {
			if len(parts) >= 3 {
				break
			}
			if r.Count < 20 || r.BaseP95 == 0 || float64(r.P95) < float64(r.BaseP95)*latencyFactor || r.P95-r.BaseP95 < latencyMinDelta {
				continue
			}
			q := spanQueryFor(s, r.Name)
			refs = append(refs, inv.refs.add(fmt.Sprintf("%s · %s: requests", s.Name, r.Name), spansLink(q, f.From, f.To), q))
			parts = append(parts, fmt.Sprintf("%s: p95 %s (%s before: %s), %s requests", r.Name, fmtDur(r.P95), inv.Compare, fmtDur(r.BaseP95), fmtCount(int(r.Count))))
		}
		if len(parts) > 0 {
			inv.finding("high", "endpoints", s.Name, "Slower endpoints: "+strings.Join(parts, "; ")+".", time.Time{}, refs...)
		}
		type spike struct {
			name, when string
			peak       time.Duration
		}
		var spiked []spike
		busy := 0
		for _, r := range s.Resources {
			if r.Count < 20 {
				continue
			}
			busy++
			if w, peak := spikedWith(r.P95Series, st.peaks, inv.clock); w != "" {
				spiked = append(spiked, spike{r.Name, w, peak})
			}
		}
		sort.SliceStable(spiked, func(i, j int) bool { return spiked[i].peak > spiked[j].peak })
		var names []string
		for _, sp := range spiked {
			names = append(names, fmt.Sprintf("%s (%s)", sp.name, sp.when))
		}
		// A heavy job running at those moments is often the cause, not a
		// victim: a rare, job-like endpoint that was very slow then.
		for _, sp := range spiked {
			for _, r := range s.Resources {
				if r.Name == sp.name && reJobLike.MatchString(r.Name) && r.Count <= jobMaxRequests(s) {
					st.jobs = append(st.jobs, fmt.Sprintf("%s (%s, %s runs in the window)", sp.name, sp.when, fmtCount(int(r.Count))))
				}
			}
		}
		switch {
		case len(spiked) >= 5 && 2*len(spiked) >= busy:
			inv.finding("high", "endpoints", s.Name, fmt.Sprintf("%d of its %d busy endpoints spiked with the service — not one endpoint's problem. The highest: %s.",
				len(spiked), busy, strings.Join(firstN(names, 3), "; ")), time.Time{})
			st.everywhere = true
		case len(spiked) > 0:
			inv.finding("high", "endpoints", s.Name, "Endpoints that spiked with the service: "+strings.Join(firstN(names, 5), "; ")+".", time.Time{})
		}
	}
	// Endpoints that went silent or appeared: often a routing or client change.
	var gone, born []string
	for _, r := range s.Resources {
		switch {
		case r.BaseCount >= minRequests && r.Count == 0:
			gone = append(gone, fmt.Sprintf("%s (%s requests %s before)", r.Name, fmtCount(int(r.BaseCount)), inv.Compare))
		case r.Count >= minRequests && r.BaseCount == 0:
			born = append(born, fmt.Sprintf("%s (%s requests)", r.Name, fmtCount(int(r.Count))))
		}
	}
	if len(gone) > 0 {
		inv.finding("medium", "endpoints", s.Name, "Endpoints that stopped receiving requests: "+strings.Join(firstN(gone, 5), "; ")+".", time.Time{})
	}
	if len(born) > 0 {
		inv.finding("info", "endpoints", s.Name, "Endpoints with requests now and none before: "+strings.Join(firstN(born, 5), "; ")+".", time.Time{})
	}
}

// reJobLike matches endpoints that run jobs rather than serve users.
var reJobLike = regexp.MustCompile(`(?i)(cron|job|batch|task|worker|sync|export|import|report|stats|backfill|migrat|cleanup|digest|schedul)`)

// jobMaxRequests: a job runs rarely — at most a hundredth of the service's
// requests, and no more than a few hundred times.
func jobMaxRequests(s *invServiceFacts) float64 {
	return math.Min(300, math.Max(60, total(s.Hits.Points)/100))
}

// spanQueryFor is the spans search of one endpoint of a service.
func spanQueryFor(s *invServiceFacts, resource string) string {
	q := "service:" + s.Name
	if s.Env != "" {
		q += " env:" + s.Env
	}
	return q + fmt.Sprintf(" resource_name:%q", resource)
}

// ─── dependencies ────────────────────────────────────────────────

// depState is a dependency (an operation or a peer) that got worse.
type depState struct {
	name       string
	errs       bool
	slow       bool
	together   string        // "11 s at 10:46": spikes in step with the service's
	peak       time.Duration // the highest of them
	text       string
	ref        int
	p95Delta   time.Duration
	errorDelta float64
}

// spikedWith reports when a p95 series (seconds) spiked at the same
// moments as the service's peaks (within two minutes) — "11 s at 10:46" —
// and the highest of those spikes.
func spikedWith(pts []series.Point, peaks []int64, clock func(int64) string) (string, time.Duration) {
	if len(pts) == 0 || len(peaks) == 0 {
		return "", 0
	}
	usual := series.Describe(pts).Median
	var hits []string
	var top time.Duration
	for _, at := range peaks {
		best := 0.0
		for _, p := range pts {
			if d := p.T - at; d >= -2*60*1000 && d <= 2*60*1000 && p.V == p.V && p.V > best {
				best = p.V
			}
		}
		if usual > 0 && best >= 3*usual && seconds(best-usual) >= 100*time.Millisecond {
			hits = append(hits, fmt.Sprintf("%s at %s", fmtDur(seconds(best)), clock(at)))
			top = max(top, seconds(best))
		}
	}
	return strings.Join(hits, ", "), top
}

func (inv *investigation) analyzeDependencies(st *svcState, f *invFacts) {
	s := st.f
	var worse []depState
	var fine []string
	check := func(_ string, g invGroup, query string) {
		if g.Name == s.Entry || g.Count+g.BaseCount < 20 {
			return
		}
		d := depState{name: g.Name}
		d.together, d.peak = spikedWith(g.P95Series, st.peaks, inv.clock)
		if d.together != "" {
			d.slow = true
		}
		if g.Errors >= minErrorsToJudge && g.Errors >= 2*math.Max(g.BaseErrors, 1) {
			d.errs, d.errorDelta = true, g.Errors-g.BaseErrors
		}
		if g.Count >= 20 && g.BaseP95 > 0 && float64(g.P95) >= float64(g.BaseP95)*latencyFactor && g.P95-g.BaseP95 >= 20*time.Millisecond {
			d.slow, d.p95Delta = true, g.P95-g.BaseP95
		}
		if g.Count == 0 && g.BaseCount >= minRequests {
			d.text = fmt.Sprintf("%s: no calls now (%s %s before)", g.Name, fmtCount(int(g.BaseCount)), inv.Compare)
			d.ref = inv.refs.add(fmt.Sprintf("%s → %s", s.Name, g.Name), spansLink(query, f.baseFrom(), f.baseTo()), query)
			worse = append(worse, d)
			return
		}
		if !d.errs && !d.slow {
			fine = append(fine, fmt.Sprintf("%s (%s calls, p95 %s, %s errors)", g.Name, fmtCount(int(g.Count)), fmtDur(g.P95), fmtCount(int(g.Errors))))
			return
		}
		var bits []string
		if d.errs {
			bits = append(bits, fmt.Sprintf("%s errors (%s before: %s)", fmtCount(int(g.Errors)), inv.Compare, fmtCount(int(g.BaseErrors))))
		}
		if d.slow {
			bits = append(bits, fmt.Sprintf("p95 typically %s (%s before: %s)", fmtDur(g.P95), inv.Compare, fmtDur(g.BaseP95)))
		}
		if d.together != "" {
			bits = append(bits, "spiked with the service: "+d.together)
		}
		d.text = fmt.Sprintf("%s: %s, %s calls", g.Name, strings.Join(bits, ", "), fmtCount(int(g.Count)))
		q := query
		if d.errs && !d.slow {
			q += " status:error"
		}
		d.ref = inv.refs.add(fmt.Sprintf("%s → %s", s.Name, g.Name), spansLink(q, f.From, f.To), q)
		worse = append(worse, d)
	}
	scope := "service:" + s.Name
	if s.Env != "" {
		scope += " env:" + s.Env
	}
	for _, g := range s.Operations {
		q := scope
		op, peer, hasPeer := strings.Cut(g.Name, " → ")
		q += " operation_name:" + op
		if hasPeer {
			q += " @peer.service:" + peer
		}
		check("", g, q)
	}
	for _, d := range worse {
		sev := "medium"
		if (d.slow && st.bad["latency"]) || (d.errs && st.bad["errors"]) {
			sev = "high"
		}
		inv.finding(sev, "dependencies", s.Name, d.text+".", time.Time{}, d.ref)
	}
	if len(fine) > 0 {
		inv.normal(fmt.Sprintf("What %s calls, like %s before: %s.", s.Name, inv.Compare, strings.Join(firstN(fine, 6), "; ")))
	}
	st.deps = worse
}

func capitalize(s string) string {
	if s == "" {
		return s
	}
	r := []rune(s)
	return strings.ToUpper(string(r[0])) + string(r[1:])
}

// ─── vital signs ─────────────────────────────────────────────────

// fmtVital renders a vital sign's value in its unit.
func fmtVital(v float64, unit string) string {
	switch unit {
	case "nanosecond":
		return fmtDur(time.Duration(v))
	case "percent":
		return fmt.Sprintf("%.0f%%", v)
	case "fraction usable":
		return fmt.Sprintf("%.0f%%", (1-v)*100)
	}
	return fmt.Sprintf("%.3g", v)
}

// worse is how far a vital sign moved the wrong way: memory usable going
// down is memory used going up.
func worse(unit string, now, before float64) float64 {
	if unit == "fraction usable" {
		return (1 - now) - (1 - before)
	}
	return now - before
}

func (inv *investigation) analyzeVitals(st *svcState, f *invFacts) {
	s := st.f
	var calm []string
	for _, v := range s.Vitals {
		sum, base := series.Describe(v.Points), series.Describe(v.Base)
		ref := inv.refs.add(st.label+": "+v.Name, metricLink(v.Query, f.From, f.To), v.Query)
		// In step with the service's latency spikes?
		if len(st.peaks) > 0 {
			var hits []string
			for _, at := range st.peaks {
				best := math.NaN()
				for _, p := range v.Points {
					if d := p.T - at; d >= -2*60*1000 && d <= 2*60*1000 && p.V == p.V && (best != best || worse(v.Unit, p.V, best) > 0) {
						best = p.V
					}
				}
				if best == best && vitalJump(v.Unit, sum.Median, best) {
					hits = append(hits, fmt.Sprintf("%s at %s", fmtVital(best, v.Unit), inv.at(time.UnixMilli(at))))
				}
			}
			if len(hits) > 0 {
				txt := fmt.Sprintf("%s: %s, against a usual %s", v.Name, strings.Join(hits, ", "), fmtVital(sum.Median, v.Unit))
				st.vitals = append(st.vitals, txt)
				inv.finding("medium", "dependencies", s.Name, "Moved with the latency spikes — "+txt+".", time.Time{}, ref)
				continue
			}
		}
		// Or higher the whole window than before?
		if len(v.Base) > 0 && vitalJump(v.Unit, base.Median, sum.Median) {
			txt := fmt.Sprintf("%s typically %s, against %s %s before", v.Name, fmtVital(sum.Median, v.Unit), fmtVital(base.Median, v.Unit), inv.Compare)
			st.vitals = append(st.vitals, txt)
			inv.finding("medium", "dependencies", s.Name, txt+".", time.Time{}, ref)
			continue
		}
		calm = append(calm, fmt.Sprintf("%s %s", v.Name, fmtVital(sum.Median, v.Unit)))
	}
	if len(calm) > 0 {
		inv.normal(fmt.Sprintf("Vital signs as usual: %s.", strings.Join(firstN(calm, 6), ", ")))
	}
}

// vitalJump: a vital sign went from usual to worrying.
func vitalJump(unit string, usual, now float64) bool {
	switch unit {
	case "percent":
		return now >= 80 && now-usual >= 25
	case "fraction usable":
		return now <= 0.1 && usual-now >= 0.1
	case "nanosecond":
		return now >= 3*usual && time.Duration(now-usual) >= 100*time.Millisecond
	}
	return usual > 0 && now >= 3*usual
}

// ─── instances and versions ──────────────────────────────────────

func (inv *investigation) analyzeInstances(st *svcState, f *invFacts) {
	s := st.f
	for _, kind := range []struct {
		groups     []invGroup
		what, many string
		deploy     string
	}{
		{s.Hosts, "instance", "instances", "deploy, restart or scale-out"},
		{s.Versions, "version", "versions", "deploy"},
	} {
		if len(kind.groups) == 0 {
			continue
		}
		var born, gone []invGroup
		for _, g := range kind.groups {
			switch {
			case g.BaseCount == 0 && g.Count > 0:
				born = append(born, g)
			case g.Count == 0 && g.BaseCount > 0:
				gone = append(gone, g)
			}
		}
		if len(born) == 0 && len(gone) == 0 {
			inv.normal(fmt.Sprintf("Same %s as %s before (%d): no %s.", kind.many, inv.Compare, len(kind.groups), kind.deploy))
			continue
		}
		// Those that appeared inside the window are changes in it; those
		// already there from its start came before it.
		var inside []invGroup
		var before []string
		for _, g := range born {
			if !g.FirstSeen.IsZero() && g.FirstSeen.Sub(f.From) > 2*time.Minute {
				inside = append(inside, g)
			} else {
				before = append(before, g.Name)
			}
		}
		sort.Slice(inside, func(i, j int) bool { return inside[i].FirstSeen.Before(inside[j].FirstSeen) })
		for _, g := range inside {
			q := fmt.Sprintf("service:%s %s:%q", s.Name, map[string]string{"instance": "host", "version": "version"}[kind.what], g.Name)
			r := inv.refs.add(fmt.Sprintf("%s: first requests of %s %s", s.Name, kind.what, g.Name), spansLink(q, g.FirstSeen.Add(-5*time.Minute), g.FirstSeen.Add(15*time.Minute)), q)
			ev := invEvent{At: g.FirstSeen, Kind: "deploy", What: fmt.Sprintf("%s: new %s %s (%s)", s.Name, kind.what, g.Name, kind.deploy), Refs: nonZero([]int{r})}
			inv.Timeline = append(inv.Timeline, ev)
			st.changes = append(st.changes, ev)
		}
		if len(inside) > 0 {
			var names []string
			for _, g := range inside {
				names = append(names, fmt.Sprintf("%s (first request %s)", g.Name, inv.at(g.FirstSeen)))
			}
			txt := fmt.Sprintf("%s in the window — a %s: %s", capitalize(pluralOf(len(inside), "new "+kind.what, "new "+kind.many)), kind.deploy, strings.Join(names, ", "))
			if len(gone) > 0 {
				txt += fmt.Sprintf("; %s that served %s before %s", pluralOf(len(gone), kind.what, kind.many), inv.Compare, map[bool]string{true: "doesn't now", false: "don't now"}[len(gone) == 1])
			}
			inv.finding("medium", "changes", s.Name, txt+".", inside[0].FirstSeen)
		}
		if len(before) > 0 {
			inv.finding("info", "changes", s.Name, fmt.Sprintf("Serving from other %s than %s before (%s): changed before this window.", kind.many, inv.Compare, strings.Join(firstN(before, 4), ", ")), time.Time{})
		}
	}
}

func pluralOf(n int, one, many string) string {
	if n == 1 {
		return "1 " + one
	}
	return fmt.Sprintf("%d %s", n, many)
}

// ─── alerting ────────────────────────────────────────────────────

// alertAbout reports whether a monitor transition concerns the service.
func alertAbout(e datadog.EventV2, service string) bool {
	if hasTag(e.Tags, "service:"+service) {
		return true
	}
	if m := e.Monitor; m != nil {
		if hasTag(m.Tags, "service:"+service) || mentionsService(m.Query, service) {
			return true
		}
	}
	return false
}

// mentionsService: service:<name> in a query or a scope.
func mentionsService(text, service string) bool {
	for _, m := range reServiceTag.FindAllStringSubmatch(text, -1) {
		if m[1] == service {
			return true
		}
	}
	return false
}

func (inv *investigation) analyzeAlerting(st *svcState, f *invFacts) {
	s := st.f
	var fired []datadog.EventV2
	for _, e := range f.Alerts {
		if e.Monitor == nil || !alertAbout(e, s.Name) {
			continue
		}
		if e.Timestamp.Before(f.From.Add(-f.Lookback)) || e.Timestamp.After(f.To) {
			continue
		}
		fired = append(fired, e)
	}
	sort.Slice(fired, func(i, j int) bool { return fired[i].Timestamp.Before(fired[j].Timestamp) })
	for _, e := range fired {
		m := e.Monitor
		r := inv.refs.add(fmt.Sprintf("monitor %q", m.Name), monitorLink(m.ID, f.From.Add(-f.Lookback), f.To), m.Query)
		inv.event(e.Timestamp, "alert", fmt.Sprintf("monitor %q %s → %s", m.Name, orDash(m.FromState), m.ToState), r)
		if (m.ToState == "Alert" || m.ToState == "Warn") && !st.onset.IsZero() && !e.Timestamp.Before(st.onset.Add(-5*time.Minute)) && st.alertAt.IsZero() {
			st.alertAt, st.alerted = e.Timestamp, m.Name
		}
	}
	var now []string
	var refs []int
	for _, m := range s.Monitors {
		if m.Status == "Alert" || m.Status == "Warn" || m.Status == "No Data" {
			now = append(now, fmt.Sprintf("%q (%s)", m.Name, m.Status))
			refs = append(refs, inv.refs.add(fmt.Sprintf("monitor %q", m.Name), monitorLink(m.ID, f.From, f.To), ""))
		}
	}
	recent := f.Now.IsZero() || f.Now.Sub(f.To) <= 30*time.Minute
	if len(now) > 0 && recent {
		inv.finding("medium", "alerting", s.Name, "Monitors not OK now: "+strings.Join(now, ", ")+".", time.Time{}, refs...)
	}
	if len(fired) == 0 {
		switch n := max(len(s.WatchedBy), len(s.Monitors)); {
		case n == 0:
			inv.normal(fmt.Sprintf("No monitor watches %s.", s.Name))
		case len(now) == 0 || !recent:
			inv.normal(fmt.Sprintf("None of the %s watching %s changed state in the window.", pluralOf(n, "monitor", "monitors"), s.Name))
		}
	}
	for _, slo := range s.SLOs {
		r := inv.refs.add(fmt.Sprintf("SLO %q", slo.Name), sloLink(slo.ID), "")
		for _, o := range slo.OverallStatus {
			switch o.Status {
			case "BREACHED", "WARNING":
				inv.finding("medium", "alerting", s.Name, fmt.Sprintf("SLO %q is %s over %s: %.3g%% against a %.3g%% target, %.0f%% of the error budget left.",
					slo.Name, strings.ToLower(o.Status), o.Timeframe, o.SLI, o.Target, o.ErrorBudgetRemaining), time.Time{}, r)
			}
		}
	}
}

// ─── changes ─────────────────────────────────────────────────────

// reWordish splits names into words for "is this change about X?".
var reWordish = regexp.MustCompile(`[a-z0-9]+`)

// changeAbout reports whether a change event concerns the service: tagged
// with it, or naming it as a word ("shop-api-00321" is about api).
func changeAbout(e datadog.EventV2, service string) bool {
	if hasTag(e.Tags, "service:"+service) {
		return true
	}
	want := strings.ToLower(service)
	for _, text := range []string{e.Changed, e.Title} {
		low := strings.ToLower(text)
		if strings.Contains(low, want) && len(want) >= 5 {
			return true
		}
		for _, w := range reWordish.FindAllString(low, -1) {
			if w == want {
				return true
			}
		}
	}
	return false
}

func (inv *investigation) analyzeChanges(states []*svcState, f *invFacts) {
	lookFrom := f.From.Add(-f.Lookback)
	var elsewhere []datadog.EventV2
	ignored := 0
	for _, e := range f.Changes {
		if e.Timestamp.Before(lookFrom) || e.Timestamp.After(f.To) {
			continue
		}
		mine := false
		for _, st := range states {
			if changeAbout(e, st.f.Name) {
				mine = true
				q := "@evt.category:change"
				r := inv.refs.add(fmt.Sprintf("change: %s", oneLine(e.Title, 60)), eventsLink(q, e.Timestamp.Add(-5*time.Minute), e.Timestamp.Add(5*time.Minute)), q)
				ev := invEvent{At: e.Timestamp, Kind: "change", What: fmt.Sprintf("%s: %s", st.f.Name, oneLine(e.Title, 120)), Refs: nonZero([]int{r})}
				inv.Timeline = append(inv.Timeline, ev)
				st.changes = append(st.changes, ev)
			}
		}
		if !mine {
			if relevantElsewhere(e, envOf(states)) {
				elsewhere = append(elsewhere, e)
			} else {
				ignored++
			}
		}
	}
	if len(elsewhere) > 0 {
		sort.Slice(elsewhere, func(i, j int) bool { return elsewhere[i].Timestamp.Before(elsewhere[j].Timestamp) })
		q := "@evt.category:change"
		r := inv.refs.add("changes elsewhere", eventsLink(q, lookFrom, f.To), q)
		var names []string
		for _, e := range elsewhere {
			names = append(names, fmt.Sprintf("%s %s", inv.at(e.Timestamp), oneLine(firstNonEmpty(e.Changed, e.Title), 60)))
		}
		txt := fmt.Sprintf("%s to other services around it: %s", pluralOf(len(elsewhere), "deploy or change", "deploys and changes"), strings.Join(firstN(names, 6), "; "))
		if ignored > 0 {
			txt += fmt.Sprintf(" (%d changes to data, storage or other environments left out)", ignored)
		}
		inv.finding("info", "changes", "", txt+".", time.Time{}, r)
	}
	listed := map[string]bool{}
	for _, e := range f.Changes {
		listed[e.ID] = true
	}
	for _, e := range f.Events {
		if e.Timestamp.Before(lookFrom) || e.Timestamp.After(f.To) || listed[e.ID] {
			continue
		}
		inv.event(e.Timestamp, "event", oneLine(e.Title, 120))
	}

	// Datadog's own configuration: monitors, pipelines, indexes, keys…
	var related, other []datadog.AuditEvent
	for _, a := range f.Audit {
		text := strings.ToLower(a.Attributes.ResourceName() + " " + a.Attributes.Message)
		hit := false
		for _, st := range states {
			if strings.Contains(text, strings.ToLower(st.f.Name)) {
				hit = true
			}
		}
		if hit {
			related = append(related, a)
		} else {
			other = append(other, a)
		}
	}
	for _, a := range related {
		t := datadog.ParseTime(a.Attributes.Timestamp)
		if t.IsZero() {
			continue
		}
		q := "-@asset.type:datadog_agent_configuration"
		r := inv.refs.add("audit trail", auditLink(q, lookFrom, f.To), q)
		ev := invEvent{At: t, Kind: "config", What: fmt.Sprintf("Datadog %s %s %s by %s", strings.ToLower(a.Attributes.Product()), orDash(a.Attributes.ResourceName()), a.Attributes.Action(), orDash(a.Attributes.Actor())), Refs: nonZero([]int{r})}
		inv.Timeline = append(inv.Timeline, ev)
		for _, st := range states {
			st.changes = append(st.changes, ev)
		}
	}
	if len(other) > 0 {
		q := "-@asset.type:datadog_agent_configuration"
		r := inv.refs.add("audit trail", auditLink(q, lookFrom, f.To), q)
		products := map[string]int{}
		for _, a := range other {
			products[strings.ToLower(orDash(a.Attributes.Product()))]++
		}
		var parts []string
		for p, n := range products {
			parts = append(parts, fmt.Sprintf("%d %s", n, p))
		}
		sort.Strings(parts)
		inv.finding("info", "changes", "", fmt.Sprintf("%s in Datadog's configuration around it, none naming the service (%s).",
			pluralOf(len(other), "change", "changes"), strings.Join(parts, ", ")), time.Time{}, r)
	}
	if len(f.Changes) == 0 && len(related) == 0 && len(other) == 0 {
		inv.normal("No change events or Datadog configuration changes around the window.")
	}
}

// computeResources are resource types whose changes can break a service:
// what runs code or routes traffic, not data or storage.
var computeResources = []string{"run_revision", "run_service", "app_engine", "appengine", "kubernetes", "k8s", "deployment",
	"statefulset", "daemonset", "replicaset", "pod", "function", "lambda", "instance_group", "instance_template",
	"compute_instance", "ecs", "container", "load_balancer", "url_map", "backend_service", "ingress", "gateway"}

// relevantElsewhere: a change to another service that could matter — a
// compute or routing resource, not in another environment's project.
func relevantElsewhere(e datadog.EventV2, env string) bool {
	if e.ResourceType != "" {
		rt := strings.ToLower(e.ResourceType)
		ok := false
		for _, c := range computeResources {
			if strings.Contains(rt, c) {
				ok = true
				break
			}
		}
		if !ok {
			return false
		}
	}
	key := strings.ToLower(e.ResourceKey + " " + strings.Join(e.Tags, " "))
	if productionEnv(map[string]int{env: 1}) != "" {
		for _, other := range []string{"-stg", "staging", "-dev", "sandbox", "-test"} {
			if strings.Contains(key, other) {
				return false
			}
		}
	}
	return true
}

func envOf(states []*svcState) string {
	for _, st := range states {
		if st.f.Env != "" {
			return st.f.Env
		}
	}
	return ""
}

func firstNonEmpty(ss ...string) string {
	for _, s := range ss {
		if strings.TrimSpace(s) != "" {
			return s
		}
	}
	return ""
}

func (inv *investigation) analyzeIncidents(f *invFacts) {
	for _, inc := range f.Incidents {
		r := inv.refs.add(fmt.Sprintf("incident %q", inc.Attributes.Title), incidentLink(inc.ID), "")
		created := datadog.ParseTime(inc.Attributes.Created)
		inv.finding("medium", "alerting", "", fmt.Sprintf("Incident %q (%s, %s) open during the window.", inc.Attributes.Title, orDash(inc.Attributes.Severity), inc.Attributes.Status), created, r)
		if !created.IsZero() {
			inv.event(created, "incident", fmt.Sprintf("incident %q declared", inc.Attributes.Title), r)
		}
	}
}

// ─── leads ───────────────────────────────────────────────────────

func (st *svcState) degraded() bool {
	return st.bad["errors"] || st.bad["latency"] || st.bad["traffic"] || st.bad["logs"]
}

func (inv *investigation) leadsFor(st *svcState, f *invFacts) {
	s := st.f
	if !st.degraded() {
		return
	}
	first := len(inv.Leads)
	defer func() {
		for i := first; i < len(inv.Leads); i++ {
			inv.Leads[i].Service = s.Name
		}
	}()
	onset := st.onset
	if !onset.IsZero() {
		inv.event(onset, "onset", fmt.Sprintf("%s: %s", st.label, strings.Join(st.symptoms, "; ")))
	}

	// 1. A change shortly before the onset.
	if !onset.IsZero() {
		var best *invEvent
		for i := range st.changes {
			c := &st.changes[i]
			gap := onset.Sub(c.At)
			if gap < -2*time.Minute || gap > beforeOnset {
				continue
			}
			if best == nil || c.At.After(best.At) {
				best = c
			}
		}
		if best != nil {
			gap := onset.Sub(best.At)
			conf, score := "medium", 2.0
			if gap <= nearOnset {
				conf, score = "high", 3.0
			}
			l := invLead{Title: fmt.Sprintf("A change %s before it started: %s", fmtAgo(max(gap, 0)), best.What), Confidence: conf, score: score, Refs: best.Refs,
				For: []string{fmt.Sprintf("%s at %s, and the trouble started at %s", best.What, inv.at(best.At), inv.at(onset))}}
			if best.Kind == "deploy" {
				l.Verify = fmt.Sprintf("datadog logs patterns %q --around %q --window 30m --compare %s", s.LogQuery, onset.Format(time.RFC3339), inv.Compare)
			}
			if len(st.changes) > 1 {
				l.Against = append(l.Against, fmt.Sprintf("%d other changes happened around it too (see the timeline)", len(st.changes)-1))
			}
			inv.Leads = append(inv.Leads, l)
		} else {
			inv.normal(fmt.Sprintf("No deploy, restart or change of %s in the hour before %s.", s.Name, inv.at(onset)))
		}
	}

	// 2. Everything slowed at once: the process or its host, rather than
	// one dependency.
	var together []string
	var worst depState
	for _, d := range st.deps {
		if d.together != "" {
			together = append(together, d.name)
			if d.peak > worst.peak || worst.name == "" {
				worst = d
			}
		}
	}
	shared := len(together) >= 3
	if shared || len(st.vitals) > 0 && len(st.peaks) > 0 {
		l := invLead{Title: "Everything slowed down at once: look at the process or its host, not one dependency", Confidence: "high", score: 3.4,
			For:    []string{fmt.Sprintf("%s spiked at the same moments as the service", strings.Join(firstN(together, 5), ", "))},
			Verify: fmt.Sprintf("datadog metrics describe \"max:runtime.node.event_loop.delay.max{service:%s} by {host}\" --since %s", s.Name, fmtDuration(f.To.Sub(f.From).Round(time.Minute)))}
		if !shared {
			l.Confidence, l.score = "medium", 2.4
			l.For = nil
		}
		if st.everywhere {
			l.For = append(l.For, "most of its endpoints spiked together too")
		}
		l.For = append(l.For, st.vitals...)
		if worst.name != "" {
			l.For = append(l.For, fmt.Sprintf("%s spiked the highest (%s)", worst.name, fmtDur(worst.peak)))
		}
		l.Against = append(l.Against, "one slow dependency (a database holding connections, say) can stall everything that waits on the same pool or event loop")
		inv.Leads = append(inv.Leads, l)
	}

	// 3. A heavy job that ran when it spiked.
	if len(st.jobs) > 0 {
		job := strings.Split(st.jobs[0], " (")[0]
		q := fmt.Sprintf("%s resource_name:*%s*", strings.Split(spanQueryFor(s, job), " resource_name:")[0], jobSegment(job))
		r := inv.refs.add(fmt.Sprintf("%s: runs of %s", s.Name, job), spansLink(q, f.From, f.To), q)
		inv.Leads = append(inv.Leads, invLead{Title: "A heavy job ran at those moments: " + job, Confidence: "medium", score: 2.9, Refs: nonZero([]int{r}),
			For: []string{"job-like endpoints that ran slow when the service spiked: " + strings.Join(firstN(st.jobs, 3), "; "),
				"a job that holds the event loop, the CPU or database connections slows every request it shares them with"},
			Verify: fmt.Sprintf("datadog read %q", inv.refURL(r))})
	}

	// 4. A dependency that got worse the same way.
	for _, d := range st.deps {
		if !(d.slow && st.bad["latency"]) && !(d.errs && st.bad["errors"]) && !strings.Contains(d.text, "no calls now") {
			continue
		}
		title := fmt.Sprintf("It comes from %s: %s", d.name, d.text)
		conf, score := "medium", 2.2
		switch {
		case d.together != "" && shared:
			if d.name != worst.name {
				continue // already in the lead above
			}
			title = fmt.Sprintf("The slowness comes from %s: it spiked the most, at the same moments", d.name)
			conf, score = "medium", 2.6
		case d.together != "":
			title = fmt.Sprintf("The slowness comes from %s: it spiked at the same moments", d.name)
			conf, score = "high", 3.2
		case d.slow && st.bad["latency"]:
			title = fmt.Sprintf("The slowness comes from %s", d.name)
			conf, score = "high", 2.8
		case d.errs && st.bad["errors"]:
			title = fmt.Sprintf("The errors come from %s", d.name)
		}
		inv.Leads = append(inv.Leads, invLead{Title: title, Confidence: conf, score: score, Refs: nonZero([]int{d.ref}),
			For:    []string{d.text},
			Verify: fmt.Sprintf("datadog read %q", inv.refURL(d.ref))})
	}

	// 5. Errors concentrated in one endpoint.
	if st.bad["errors"] && s.Resource == "" {
		extra, top, topName := 0.0, 0.0, ""
		for _, r := range s.Resources {
			if d := r.Errors - r.BaseErrors; d > 0 {
				extra += d
				if d > top {
					top, topName = d, r.Name
				}
			}
		}
		if extra > 0 && top/extra >= 0.6 {
			verify := fmt.Sprintf("datadog investigate %s --resource %q --since %s", s.Name, topName, fmtDuration(f.To.Sub(f.From).Round(time.Minute)))
			if s.ExampleTrace != "" && s.ExampleTraceResource == topName {
				verify = "datadog trace " + s.ExampleTrace
			}
			inv.Leads = append(inv.Leads, invLead{Title: fmt.Sprintf("It's one endpoint: %s", topName), Confidence: "medium", score: 2.0,
				For:    []string{fmt.Sprintf("%.0f%% of the extra errors are %s", top/extra*100, topName)},
				Verify: verify})
		}
	}

	// 6. A new error in the logs when it started.
	if r := s.ErrorLogs; r != nil && (st.bad["errors"] || st.bad["logs"]) {
		for _, p := range r.Patterns {
			if !p.New || p.Estimate < 5 {
				continue
			}
			near := !onset.IsZero() && !p.First.IsZero() && p.First.After(onset.Add(-5*time.Minute)) && p.First.Before(onset.Add(nearOnset))
			if !near && p.Share < 30 {
				continue
			}
			conf, score := "medium", 1.8
			if near && p.Share >= 20 {
				conf, score = "high", 2.5
			}
			l := invLead{Title: fmt.Sprintf("The failure says: %q", oneLine(p.Pattern, 100)), Confidence: conf, score: score,
				For:    []string{fmt.Sprintf("a new error pattern (absent %s before), ~%s logs, %.0f%% of the errors, first at %s", inv.Compare, fmtCount(p.Estimate), p.Share, inv.at(p.First))},
				Verify: fmt.Sprintf("datadog logs patterns %q --around %q --window 30m", s.LogQuery, p.First.Format(time.RFC3339))}
			if p.Example != "" {
				l.For = append(l.For, "example: "+oneLine(p.Example, 200))
			}
			inv.Leads = append(inv.Leads, l)
			break
		}
	}

	// 6b. No new error, but one that was already there got much more
	// frequent: it's what the extra error logs say.
	if r := s.ErrorLogs; r != nil && st.bad["logs"] && !inv.hasLead(first, "The failure says") {
		var top *logPattern
		for i := range r.Patterns {
			p := &r.Patterns[i]
			if p.New || p.Before == nil || p.Estimate < 20 || float64(p.Estimate) < 2*float64(max(*p.Before, 1)) {
				continue
			}
			if top == nil || p.Estimate-*p.Before > top.Estimate-*top.Before {
				top = p
			}
		}
		if top != nil {
			conf, score := "low", 1.2
			if top.Share >= 30 {
				conf, score = "medium", 1.6
			}
			l := invLead{Title: fmt.Sprintf("An error that was already there got more frequent: %q", oneLine(top.Pattern, 100)), Confidence: conf, score: score,
				For:    []string{fmt.Sprintf("~%s logs, %s the ~%s of %s before, %.0f%% of the error logs", fmtCount(top.Estimate), changeText(float64(max(*top.Before, 1)), float64(top.Estimate)), fmtCount(*top.Before), inv.Compare, top.Share)},
				Verify: fmt.Sprintf("datadog logs patterns %q --since %s --compare %s", s.LogQuery, fmtDuration(f.To.Sub(f.From).Round(time.Minute)), inv.Compare)}
			if top.Example != "" {
				l.For = append(l.For, "example: "+oneLine(top.Example, 200))
			}
			inv.Leads = append(inv.Leads, l)
		}
	}

	// 7. Load: a surge before the slowdown or the errors.
	if st.bad["traffic"] && (st.bad["latency"] || st.bad["errors"]) {
		t := st.firstBad["traffic"]
		l := invLead{Title: "It's load: traffic changed first", Confidence: "medium", score: 1.9,
			For: []string{fmt.Sprintf("traffic %s", strings.Join(filterPrefix(st.symptoms, "traffic"), ", "))}}
		if !t.IsZero() && (st.firstBad["latency"].IsZero() || !t.After(st.firstBad["latency"])) {
			l.For = append(l.For, fmt.Sprintf("the traffic change (%s) came before the rest", inv.at(t)))
		} else {
			l.Confidence, l.score = "low", 1.0
			l.Against = append(l.Against, "the traffic change didn't come first")
		}
		inv.Leads = append(inv.Leads, l)
	}

	if len(inv.Leads) == first {
		inv.Leads = append(inv.Leads, invLead{Title: "Nothing in Datadog explains it yet", Confidence: "low", score: 0.5,
			For:    []string{"no deploy, restart or configuration change before it, no dependency that got worse, no single endpoint or new error behind it"},
			Verify: "look outside Datadog: upstream clients, feature flags, data changes, third parties; or widen the window: --since " + fmtDuration(2*f.To.Sub(f.From).Round(time.Minute))})
	}
}

// hasLead says whether a lead added since index first starts with prefix.
func (inv *investigation) hasLead(first int, prefix string) bool {
	for _, l := range inv.Leads[first:] {
		if strings.HasPrefix(l.Title, prefix) {
			return true
		}
	}
	return false
}

// jobSegment is an endpoint's most specific path segment.
func jobSegment(resource string) string {
	if h := resourceHint(resource); h != "" {
		return strings.Trim(strings.TrimPrefix(h, " resource_name:"), "*")
	}
	return resource
}

func filterPrefix(ss []string, prefix string) []string {
	var out []string
	for _, s := range ss {
		if strings.HasPrefix(s, prefix) {
			out = append(out, strings.TrimSpace(strings.TrimPrefix(s, prefix)))
		}
	}
	return out
}

func (inv *investigation) refURL(n int) string {
	if n <= 0 || n > len(inv.refs.list) {
		return ""
	}
	return inv.refs.list[n-1].URL
}

// ─── improvements ────────────────────────────────────────────────

func (inv *investigation) suggestionsFor(st *svcState, f *invFacts) {
	s := st.f
	tagScope := "service:" + s.Name
	if s.Env != "" {
		tagScope = "env:" + s.Env + "," + tagScope
	}
	op := s.Entry
	create := func(name, typ, query string) string {
		return fmt.Sprintf("datadog monitors create %q --type %q --query %q --message \"… @<who-to-notify>\"   # pick the threshold from the data: datadog metrics describe … --since 7d", name, typ, query)
	}
	// Which kinds of monitor watch the service, and which alerted.
	kinds := map[string][]datadog.Monitor{}
	for _, m := range s.WatchedBy {
		kinds[monitorKind(m)] = append(kinds[monitorKind(m)], m)
	}
	signals := []struct {
		bad, kind, what string
		cmd             func() string
	}{
		{"errors", "errors", "error rate", func() string {
			return create(s.Name+" error rate", "query alert", fmt.Sprintf("sum(last_10m):sum:trace.%s.errors{%s}.as_count() / sum:trace.%s.hits{%s}.as_count() > 0.05", op, tagScope, op, tagScope))
		}},
		{"latency", "latency", "p95 latency", func() string {
			return create(s.Name+" p95 latency", "query alert", fmt.Sprintf("percentile(last_10m):p95:trace.%s{%s} > 1", op, tagScope))
		}},
		{"traffic", "traffic", "traffic", func() string {
			return create(s.Name+" traffic drop", "query alert", fmt.Sprintf("sum(last_30m):sum:trace.%s.hits{%s}.as_count() < 10", op, tagScope))
		}},
		{"logs", "logs", "error logs", func() string {
			return create(s.Name+" error logs", "log alert", fmt.Sprintf("logs(%q).index(\"*\").rollup(\"count\").last(\"10m\") > 50", s.LogQuery))
		}},
	}
	for _, sig := range signals {
		if !st.bad[sig.bad] || (op == "" && sig.bad != "logs") {
			continue
		}
		watchers := kinds[sig.kind]
		switch {
		case len(watchers) == 0:
			inv.Suggestions = append(inv.Suggestions, invSuggestion{
				What:    fmt.Sprintf("A monitor on %s's %s", s.Name, sig.what),
				Why:     fmt.Sprintf("none watches it, so this went unnoticed unless someone was looking"),
				Command: sig.cmd()})
		case st.alertAt.IsZero():
			var names []string
			for _, m := range watchers {
				names = append(names, fmt.Sprintf("%q (datadog monitors explain %d)", m.Name, m.ID))
			}
			quiet := "the whole window"
			if !st.onset.IsZero() {
				quiet = fmtAgo(f.To.Sub(st.onset))
			}
			inv.Suggestions = append(inv.Suggestions, invSuggestion{
				What:    fmt.Sprintf("Review the %s monitors of %s: none alerted", sig.what, s.Name),
				Why:     fmt.Sprintf("%s watch it and stayed quiet for %s: the threshold or the window may be too loose — %s", pluralOf(len(watchers), "monitor", "monitors"), quiet, strings.Join(firstN(names, 3), ", ")),
				Command: "datadog monitors review --service " + s.Name})
		}
	}
	if !st.alertAt.IsZero() && !st.onset.IsZero() && st.alertAt.Sub(st.onset) > 15*time.Minute {
		inv.Suggestions = append(inv.Suggestions, invSuggestion{
			What:    fmt.Sprintf("Catch it sooner: %q alerted %s after it started", st.alerted, fmtAgo(st.alertAt.Sub(st.onset))),
			Why:     "a shorter evaluation window, or a warning threshold, would have given that time back",
			Command: "datadog monitors review --service " + s.Name})
	}
	if len(s.Versions) == 0 && len(s.Hosts) > 0 && op != "" {
		inv.Suggestions = append(inv.Suggestions, invSuggestion{
			What: fmt.Sprintf("Tag %s's deploys with a version", s.Name),
			Why:  "without DD_VERSION on the service, deploys only show as new instances; with it, Datadog compares versions (deployment tracking)",
		})
	}
	if s.Catalog == nil && (s.Hits != nil || s.ErrorLogs != nil) {
		inv.Suggestions = append(inv.Suggestions, invSuggestion{
			What: fmt.Sprintf("Register %s in the Service Catalog with its team", s.Name),
			Why:  "reports and alerts could then say who owns it and link its runbook",
		})
	}
}

// ─── next steps ──────────────────────────────────────────────────

func (inv *investigation) nextFor(st *svcState, f *invFacts) {
	s := st.f
	win := fmtDuration(f.To.Sub(f.From).Round(time.Minute))
	around := ""
	if !st.onset.IsZero() {
		around = fmt.Sprintf(" --around %q --window 30m", st.onset.Format(time.RFC3339))
	}
	if s.SlowTrace != "" && st.bad["latency"] {
		inv.Next = append(inv.Next, fmt.Sprintf("datadog trace %s   # a slow %s request (%s): where its time went", s.SlowTrace, s.SlowTraceResource, fmtDur(s.SlowTraceDuration)))
	}
	if s.ExampleTrace != "" && (st.bad["errors"] || !st.degraded()) {
		inv.Next = append(inv.Next, fmt.Sprintf("datadog trace %s   # a failing %s request: its span tree and logs", s.ExampleTrace, s.ExampleTraceResource))
	}
	if s.LogQuery != "" && (st.bad["errors"] || st.bad["logs"]) {
		if around != "" {
			inv.Next = append(inv.Next, fmt.Sprintf("datadog logs patterns %q%s --compare %s", s.LogQuery, around, inv.Compare))
		} else {
			inv.Next = append(inv.Next, fmt.Sprintf("datadog logs patterns %q --since %s --compare %s", s.LogQuery, win, inv.Compare))
		}
	}
	if st.bad["latency"] && s.P95 != nil {
		inv.Next = append(inv.Next, fmt.Sprintf("datadog metrics describe %q --since %s --compare %s", strings.Replace(s.P95.Query, "}", "} by {resource_name}", 1), win, inv.Compare))
	}
	if f.Now.IsZero() || f.Now.Sub(f.To) <= 30*time.Minute {
		for _, m := range s.Monitors {
			if m.Status == "Alert" || m.Status == "Warn" {
				inv.Next = append(inv.Next, fmt.Sprintf("datadog monitors explain %d   # what %q watches and what its data did", m.ID, m.Name))
				break
			}
		}
	}
	if st.degraded() && st.started {
		inv.Next = append(inv.Next, fmt.Sprintf("datadog investigate %s --since %s   # it was already bad when the window starts: look further back", s.Name, fmtDuration(4*f.To.Sub(f.From).Round(time.Minute))))
	}
	if !st.degraded() {
		inv.Next = append(inv.Next, fmt.Sprintf("datadog investigate %s --since %s   # a longer window, if the trouble was earlier", s.Name, fmtDuration(6*f.To.Sub(f.From).Round(time.Minute))))
	}
}

// ─── gaps and summary ────────────────────────────────────────────

func (inv *investigation) gaps(f *invFacts, states []*svcState) {
	names := make([]string, 0, len(f.Failed))
	for k := range f.Failed {
		names = append(names, k)
	}
	sort.Strings(names)
	for _, k := range names {
		inv.Gaps = append(inv.Gaps, fmt.Sprintf("%s: %s", k, oneLine(f.Failed[k], 200)))
	}
	for _, st := range states {
		if st.f.Entry == "" {
			inv.Gaps = append(inv.Gaps, fmt.Sprintf("%s serves no APM requests (no entry spans): judged from its logs only", st.f.Name))
		}
	}
}

func (inv *investigation) summarize(states []*svcState) {
	var parts []string
	status := "normal"
	var onset time.Time
	for _, st := range states {
		if !st.degraded() {
			parts = append(parts, inv.normalSummary(st))
			continue
		}
		st.status = "degraded"
		if inv.recoveredBy(st) {
			st.status = "recovered"
		}
		if status != "degraded" {
			status = st.status
		}
		if !st.onset.IsZero() && (onset.IsZero() || st.onset.Before(onset)) {
			onset = st.onset
		}
		line := fmt.Sprintf("%s: %s", st.label, strings.Join(st.symptoms, "; "))
		if st.status == "recovered" {
			line += " — back to normal by the end of the window"
		}
		if st.started {
			line += " — already so when the window starts"
		}
		line += "."
		if top := inv.topLead(st.f.Name); top != nil {
			line += fmt.Sprintf(" Most likely (%s confidence): %s.", top.Confidence, lowerFirst(top.Title))
		}
		switch {
		case st.onset.IsZero():
		case st.alertAt.IsZero():
			line += " No monitor alerted on it."
		default:
			line += fmt.Sprintf(" %q alerted %s after it started.", st.alerted, fmtAgo(max(st.alertAt.Sub(st.onset), 0)))
		}
		parts = append(parts, line)
	}
	if len(states) == 0 {
		status = "unknown"
		parts = append(parts, "Nothing to investigate: no service matched.")
	}
	inv.Status = status
	if !onset.IsZero() {
		inv.Onset = &onset
	}
	inv.Summary = strings.Join(parts, " ")
}

// topLead is a service's strongest lead.
func (inv *investigation) topLead(service string) *invLead {
	var best *invLead
	for i := range inv.Leads {
		l := &inv.Leads[i]
		if l.Service == service && (best == nil || l.score > best.score) {
			best = l
		}
	}
	return best
}

// recoveredBy: every golden signal that went bad came back by the
// window's end.
func (inv *investigation) recoveredBy(st *svcState) bool {
	any := false
	for _, sig := range []string{"errors", "latency", "traffic"} {
		if st.bad[sig] {
			any = true
			if !st.back[sig] {
				return false
			}
		}
	}
	return any && !st.bad["logs"]
}

func (inv *investigation) normalSummary(st *svcState) string {
	s := st.f
	var bits []string
	for _, n := range inv.Normal {
		switch {
		case strings.HasPrefix(n.Text, "Traffic "), strings.HasPrefix(n.Text, "Error rate "), strings.HasPrefix(n.Text, "p95 latency"):
			bits = append(bits, lowerFirst(strings.TrimSuffix(n.Text, ".")))
		}
	}
	line := fmt.Sprintf("%s looks normal", st.label)
	if len(bits) > 0 {
		line += ": " + strings.Join(bits, ", ")
	}
	line += "."
	changes := 0
	for _, e := range inv.Timeline {
		if e.Kind == "deploy" || e.Kind == "change" || e.Kind == "config" {
			changes++
		}
	}
	if changes == 0 {
		line += " No deploys or changes around it."
	}
	if s.ErrorLogs != nil {
		for _, p := range s.ErrorLogs.Patterns {
			if p.New && p.Estimate >= 5 {
				line += fmt.Sprintf(" One new error in the logs: %q (~%s).", oneLine(p.Pattern, 80), fmtCount(p.Estimate))
				break
			}
		}
	}
	return line
}

func lowerFirst(s string) string {
	if s == "" {
		return s
	}
	r := []rune(s)
	if len(r) > 1 && r[1] >= 'A' && r[1] <= 'Z' {
		return s // an acronym: keep it
	}
	return strings.ToLower(string(r[0])) + string(r[1:])
}
