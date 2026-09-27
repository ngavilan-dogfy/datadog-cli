package cmd

import (
	"fmt"
	"math"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/ngavilan-dogfy/datadog-cli/datadog"
	"github.com/ngavilan-dogfy/datadog-cli/ui"

	"github.com/spf13/cobra"
)

// monitors review reads every monitor in scope next to what it did — its
// transitions over the window and, for metric monitors, its data evaluated
// the way the monitor evaluates it — and proposes improvements, each with
// the command that applies it. Gathering (reviewMonitors) is separate from
// the rules (reviewFrom), which are pure functions of the facts.

var (
	reviewWin     windowFlags
	reviewService string
	reviewTag     string
	reviewJSON    bool
	reviewMD      bool
)

var monitorsReviewCmd = &cobra.Command{
	Use:   "review [monitor-id…]",
	Short: "Review monitors and propose improvements: noise, blind spots, loose thresholds, missing context",
	Long: `Review monitors against what they did over the window (30 days by default)
and propose improvements, each with the command that applies it:

  to fix        notifies no one · stuck in Alert or No Data for days · a
                floor that goes quiet when traffic (or what it counts) stops
                completely · a query left with a template placeholder ·
                shows OK but its query has no data
  worth a look  flaps (alerts over within minutes) · alerting most of the
                time, or over and over · a P1 or P2 that never reminds ·
                cloud metrics judged before they arrive · a threshold its
                data never came near · No Data noise · muted with no end
  to tidy       no runbook or dashboard link · the notification doesn't say
                the value · no service or team tag · an old draft · the same
                query as another monitor

Metric monitors are read the way they evaluate: their data rolled up over
their window, over the last days, next to their thresholds.

Nothing is changed. Each fix is a command ('datadog monitors edit 123
--option renotify_interval=60', …) to run after a yes; 'check' commands
only read.

Scope: every monitor, one service's (--service), a tag's (--tag team:web),
or the ids given (links work too).

Output:
  Terminal: the monitors to fix and to look at, tidying summed up
  --md: a report with every monitor · --json: everything, for agents

Examples:
  datadog monitors review
  datadog monitors review --service checkout
  datadog monitors review --tag team:payments --md
  datadog monitors review 12345 67890 --json`,
	RunE: func(cmd *cobra.Command, args []string) error {
		from, to, err := reviewWin.resolve()
		if err != nil {
			return err
		}
		var ids []int64
		for _, a := range args {
			if l, ok := parseDDLink(a); ok && l.Kind == "monitor" {
				a = l.ID
			}
			id, err := strconv.ParseInt(strings.TrimPrefix(strings.TrimSpace(a), "#"), 10, 64)
			if err != nil {
				return fmt.Errorf("%q isn't a monitor id", a)
			}
			ids = append(ids, id)
		}
		var rep *monitorsReview
		work := func() (err error) {
			rep, err = reviewMonitors(ids, reviewService, reviewTag, from, to)
			return err
		}
		if isTTY() && !reviewJSON && !reviewMD {
			err = withSpinner("Reading monitors, their alerts and their data", work)
		} else {
			err = work()
		}
		if err != nil {
			return err
		}
		switch {
		case reviewJSON:
			return printJSON(rep)
		case reviewMD:
			fmt.Print(rep.markdown())
			return nil
		}
		fmt.Print(rep.text(isTTY()))
		return nil
	},
}

func init() {
	reviewWin.register(monitorsReviewCmd, 30*24*time.Hour)
	monitorsReviewCmd.Flags().StringVar(&reviewService, "service", "", "Only the monitors that watch this service")
	monitorsReviewCmd.Flags().StringVar(&reviewTag, "tag", "", "Only the monitors with this tag (team:web)")
	monitorsReviewCmd.Flags().BoolVar(&reviewJSON, "json", false, "Output as JSON")
	monitorsReviewCmd.Flags().BoolVar(&reviewMD, "md", false, "Output as markdown")
	monitorsCmd.AddCommand(monitorsReviewCmd)
}

// ─── facts ───────────────────────────────────────────────────────

// reviewFacts is everything the review reads; reviewFrom judges it.
type reviewFacts struct {
	Now       time.Time // when it was read: a monitor's state is now, not at To
	From, To  time.Time
	Scope     string
	Service   string
	All       []datadog.Monitor // every monitor: composites and twins are found among them
	InScope   []datadog.Monitor
	Events    []datadog.EventV2 // monitor transitions in the window
	EventsCut bool              // the history hit the search limit
	SLOs      []datadog.SLO
	Downtimes []datadog.DowntimeData
	Data      map[int64]*reviewData
	Failed    map[string]string
}

// reviewData is what a metric monitor's data did, evaluated the way the
// monitor evaluates it (rolled up over its window) when Evaluated. No
// points means its query matched nothing over the span.
type reviewData struct {
	Query     string  `json:"query"`
	Span      string  `json:"span"`
	Evaluated bool    `json:"evaluated_like_the_monitor"`
	Points    int     `json:"points"`
	Min       float64 `json:"min"`
	P01       float64 `json:"p01"`
	P99       float64 `json:"p99"`
	Max       float64 `json:"max"`
}

const (
	reviewEventLimit = 5000
	flapWindow       = 15 * time.Minute
	stuckAfter       = 72 * time.Hour
)

func reviewMonitors(ids []int64, service, tag string, from, to time.Time) (*monitorsReview, error) {
	all, err := client.ListMonitors("", 0)
	if err != nil {
		return nil, err
	}
	f := reviewFacts{Now: time.Now(), From: from, To: to, Service: service, All: all, Failed: map[string]string{}}
	want := map[int64]bool{}
	for _, id := range ids {
		want[id] = true
	}
	switch {
	case len(ids) > 0:
		f.Scope = pluralOf(len(ids), "monitor", "monitors")
	case service != "":
		f.Scope = "the monitors of " + service
	case tag != "":
		f.Scope = "monitors tagged " + tag
	default:
		f.Scope = "every monitor"
	}
	for _, m := range all {
		switch {
		case len(ids) > 0:
			if !want[m.ID] {
				continue
			}
		case service != "":
			names := monitorServices(m)
			if !names[service] && !(len(names) == 0 && rePerService.MatchString(m.Query)) {
				continue
			}
		case tag != "":
			if !hasTag(m.Tags, tag) {
				continue
			}
		}
		f.InScope = append(f.InScope, m)
	}
	if len(f.InScope) == 0 {
		return reviewFrom(f), nil
	}

	var (
		wg sync.WaitGroup
		mu sync.Mutex
	)
	fail := func(what string, err error) {
		mu.Lock()
		f.Failed[what] = err.Error()
		mu.Unlock()
	}
	wg.Add(4)
	go func() {
		defer wg.Done()
		evs, err := client.SearchEvents("source:alert", rfc(from), rfc(to), reviewEventLimit)
		if err != nil {
			fail("alert history", err)
			return
		}
		f.Events, f.EventsCut = evs, len(evs) >= reviewEventLimit
	}()
	go func() {
		defer wg.Done()
		slos, err := client.ListSLOs("")
		if err != nil {
			fail("SLOs", err)
			return
		}
		f.SLOs = slos
	}()
	go func() {
		defer wg.Done()
		dts, err := client.ListDowntimes()
		if err != nil {
			fail("downtimes", err)
			return
		}
		f.Downtimes = dts
	}()
	go func() {
		defer wg.Done()
		data, err := monitorData(f.InScope, to)
		if err != nil {
			fail("monitor data", err)
		}
		f.Data = data
	}()
	wg.Wait()
	return reviewFrom(f), nil
}

// dataSpan is how much of a metric monitor's data is read: up to a week,
// but no more than about 2000 of its windows, so the points stay few (a
// week of 5-minute windows).
func dataSpan(m datadog.Monitor) time.Duration {
	w := 5
	if p := reMetricMonitor.FindStringSubmatch(m.Query); p != nil {
		if mm := reWindow.FindStringSubmatch(p[2]); mm != nil {
			w = windowMinutes(mm[1], mm[2])
		}
	}
	span := time.Duration(w) * 2016 * time.Minute
	return max(24*time.Hour, min(7*24*time.Hour, span.Truncate(24*time.Hour)))
}

// monitorData reads metric monitors' data the way they evaluate it, in
// batches of queries that share a span.
func monitorData(monitors []datadog.Monitor, to time.Time) (map[int64]*reviewData, error) {
	type job struct {
		id        int64
		query     string
		evaluated bool
	}
	bySpan := map[time.Duration][]job{}
	for _, m := range monitors {
		if m.Type != "query alert" && m.Type != "metric alert" {
			continue
		}
		p := reMetricMonitor.FindStringSubmatch(m.Query)
		if p == nil {
			continue
		}
		j := job{id: m.ID, query: strings.TrimSpace(p[3])}
		if q, ok := monitorWindowQuery(p[1], p[2], j.query); ok {
			j.query, j.evaluated = q, true
		}
		bySpan[dataSpan(m)] = append(bySpan[dataSpan(m)], j)
	}
	out := map[int64]*reviewData{}
	var lastErr error
	for span, jobs := range bySpan {
		for start := 0; start < len(jobs); start += 10 {
			batch := jobs[start:min(start+10, len(jobs))]
			qs := make([]string, len(batch))
			for i, j := range batch {
				qs[i] = j.query
			}
			resp, err := client.QueryMetrics(strings.Join(qs, ","), to.Add(-span).Unix(), to.Unix())
			if err != nil {
				lastErr = err
				continue
			}
			vals := make([][]float64, len(batch))
			for _, sr := range resp.Series {
				if sr.QueryIndex < 0 || sr.QueryIndex >= len(batch) {
					continue
				}
				for _, p := range seriesPoints(sr, false) {
					if p.V == p.V {
						vals[sr.QueryIndex] = append(vals[sr.QueryIndex], p.V)
					}
				}
			}
			for i, j := range batch {
				d := &reviewData{Query: j.query, Span: fmtDuration(span), Evaluated: j.evaluated, Points: len(vals[i])}
				if v := vals[i]; len(v) > 0 {
					sort.Float64s(v)
					q := func(p float64) float64 { return v[int(math.Round(p*float64(len(v)-1)))] }
					d.Min, d.P01, d.P99, d.Max = v[0], q(0.01), q(0.99), v[len(v)-1]
				}
				out[j.id] = d
			}
		}
	}
	return out, lastErr
}

// ─── judging ─────────────────────────────────────────────────────

type reviewProblem struct {
	Rule     string `json:"rule"`
	Severity string `json:"severity"` // fix, look, tidy
	What     string `json:"what"`
	Why      string `json:"why"`
	Fix      string `json:"fix,omitempty"`   // the command that applies it, after a yes
	Check    string `json:"check,omitempty"` // a read-only command to look first
	Hint     string `json:"hint,omitempty"`  // what to do when no single command does it
}

type reviewedMonitor struct {
	ID       int64    `json:"id"`
	Name     string   `json:"name"`
	Type     string   `json:"type"`
	Kind     string   `json:"kind"` // errors, latency, traffic, logs, other
	State    string   `json:"state"`
	Priority int      `json:"priority,omitempty"`
	Query    string   `json:"query"`
	URL      string   `json:"url"`
	Notifies []string `json:"notifies,omitempty"`
	Feeds    []string `json:"feeds,omitempty"` // composites and SLOs built on it
	// What it did in the window: episodes that reached Alert, that only
	// warned, went to No Data, alerts over within 15 minutes, and minutes
	// spent alerting (summed over its groups).
	Alerts          int             `json:"alerts"`
	Warnings        int             `json:"warnings"`
	NoData          int             `json:"no_data"`
	Flaps           int             `json:"flaps"`
	MinutesAlerting int             `json:"minutes_alerting"`
	NotOKSince      *time.Time      `json:"not_ok_since,omitempty"`
	NotOKBefore     bool            `json:"not_ok_since_before_window,omitempty"` // it was already when the window began
	Data            *reviewData     `json:"data,omitempty"`
	Problems        []reviewProblem `json:"problems"`
	Verdict         string          `json:"verdict"` // fix, look, tidy, fine
}

type monitorsReview struct {
	From     time.Time         `json:"from"`
	To       time.Time         `json:"to"`
	Scope    string            `json:"scope"`
	Service  string            `json:"service,omitempty"`
	Alerts   int               `json:"alerts"` // alerts of the monitors in scope, in the window
	Summary  map[string]int    `json:"summary"`
	Notes    []string          `json:"notes,omitempty"`
	Monitors []reviewedMonitor `json:"monitors"`
	Failed   map[string]string `json:"failed,omitempty"`
}

func reviewFrom(f reviewFacts) *monitorsReview {
	rep := &monitorsReview{From: f.From, To: f.To, Scope: f.Scope, Service: f.Service, Summary: map[string]int{}}
	if len(f.Failed) > 0 {
		rep.Failed = f.Failed
	}
	history := f.Failed["alert history"] == ""
	if f.EventsCut && len(f.Events) > 0 {
		oldest := f.Events[0].Timestamp
		for _, e := range f.Events {
			if e.Timestamp.Before(oldest) {
				oldest = e.Timestamp
			}
		}
		rep.Notes = append(rep.Notes, fmt.Sprintf("the alert history stops at the newest %d transitions (since %s): older alerts aren't counted", len(f.Events), oldest.Local().Format("Jan 2 15:04")))
	}

	byMonitor := map[int64][]datadog.EventV2{}
	for _, e := range f.Events {
		if e.Monitor != nil {
			byMonitor[e.Monitor.ID] = append(byMonitor[e.Monitor.ID], e)
		}
	}
	feeds := map[int64][]string{}
	for _, m := range f.All {
		if m.Type != "composite" {
			continue
		}
		for _, x := range reNumber.FindAllString(m.Query, -1) {
			id, _ := strconv.ParseInt(x, 10, 64)
			feeds[id] = append(feeds[id], fmt.Sprintf("composite #%d", m.ID))
		}
	}
	for _, s := range f.SLOs {
		for _, id := range s.MonitorIDs {
			feeds[id] = append(feeds[id], "SLO "+strconv.Quote(s.Name))
		}
	}
	sameQuery := map[string][]int64{}
	for _, m := range f.All {
		if m.Type != "composite" {
			sameQuery[normalizeQuery(m.Query)] = append(sameQuery[normalizeQuery(m.Query)], m.ID)
		}
	}
	downtimeOf := map[int64]string{}
	for _, d := range f.Downtimes {
		a := d.Attributes
		if a.MonitorIdentifier != nil && a.MonitorIdentifier.MonitorID != nil && a.Status == "active" &&
			(a.Schedule == nil || a.Schedule.End == nil) {
			downtimeOf[*a.MonitorIdentifier.MonitorID] = d.ID
		}
	}

	for _, m := range f.InScope {
		trs := byMonitor[m.ID]
		sort.SliceStable(trs, func(i, j int) bool { return trs[i].Timestamp.Before(trs[j].Timestamp) })
		rm := reviewedMonitor{ID: m.ID, Name: m.Name, Type: m.Type, Kind: monitorKind(m), State: m.OverallState,
			Query: m.Query, URL: monitorLink(m.ID, time.Time{}, time.Time{}), Notifies: notifiesOf(m),
			Feeds: feeds[m.ID], Data: f.Data[m.ID]}
		if m.Priority != nil {
			rm.Priority = *m.Priority
		}
		if history {
			tally(&rm, trs, m, f.From, f.To, f.Now.Sub(f.To) < time.Hour)
		}
		var twins []int64
		for _, id := range sameQuery[normalizeQuery(m.Query)] {
			if id != m.ID {
				twins = append(twins, id)
			}
		}
		rm.Problems = reviewRules(m, &rm, ruleContext{
			now: f.Now, window: fmtDuration(f.To.Sub(f.From).Round(time.Hour)), span: f.To.Sub(f.From), history: history,
			twins: twins, downtime: downtimeOf[m.ID], dataFailed: f.Failed["monitor data"] != "",
		})
		rm.Verdict = verdictOf(rm.Problems)
		rep.Summary[rm.Verdict]++
		rep.Alerts += rm.Alerts
		rep.Monitors = append(rep.Monitors, rm)
	}
	order := map[string]int{"fix": 0, "look": 1, "tidy": 2, "fine": 3}
	sort.SliceStable(rep.Monitors, func(i, j int) bool {
		a, b := rep.Monitors[i], rep.Monitors[j]
		if order[a.Verdict] != order[b.Verdict] {
			return order[a.Verdict] < order[b.Verdict]
		}
		if a.Alerts != b.Alerts {
			return a.Alerts > b.Alerts
		}
		return a.Name < b.Name
	})
	return rep
}

var reNumber = regexp.MustCompile(`\d+`)

// tally counts what a monitor did from its transitions, per group (a
// multi-alert monitor's hosts or services alert on their own): episodes
// that reached Alert, alerts over within flapWindow, warnings, No Data,
// time alerting and, when the window reaches now (live), since when it
// hasn't been OK.
func tally(rm *reviewedMonitor, trs []datadog.EventV2, m datadog.Monitor, from, to time.Time, live bool) {
	type group struct {
		state   string
		since   time.Time // in this state since
		notOK   time.Time // not OK since (zero while OK)
		before  bool      // not OK since before the window
		alertAt time.Time // this episode's first Alert
	}
	byGroup := map[string][]datadog.EventV2{}
	var keys []string
	for _, e := range trs {
		k := strings.Join(e.Monitor.Groups, ",")
		if _, ok := byGroup[k]; !ok {
			keys = append(keys, k)
		}
		byGroup[k] = append(byGroup[k], e)
	}
	var alerting time.Duration
	var notOK *group
	for _, k := range keys {
		var g *group
		for _, t := range netTransitions(byGroup[k]) {
			if g == nil {
				g = &group{state: t.from, since: from}
				if t.from != "OK" {
					g.notOK, g.before = from, true
				}
			}
			if g.state == "Alert" && t.to != "Alert" {
				alerting += t.at.Sub(g.since)
			}
			switch t.to {
			case "Alert":
				if g.alertAt.IsZero() {
					rm.Alerts++
					g.alertAt = t.at
				}
			case "Warn":
				if g.state != "Alert" && g.state != "Warn" {
					rm.Warnings++
				}
			case "No Data":
				rm.NoData++
			case "OK":
				if !g.alertAt.IsZero() && t.at.Sub(g.alertAt) <= flapWindow {
					rm.Flaps++
				}
				g.alertAt = time.Time{}
			}
			switch {
			case t.to == "OK":
				g.notOK, g.before = time.Time{}, false
			case g.notOK.IsZero():
				g.notOK = t.at
			}
			g.state, g.since = t.to, t.at
		}
		if g == nil {
			continue
		}
		if g.state == "Alert" {
			alerting += to.Sub(g.since)
		}
		if !g.notOK.IsZero() && (notOK == nil || g.notOK.Before(notOK.notOK)) {
			notOK = g
		}
	}
	rm.MinutesAlerting = int(alerting.Minutes())
	switch rm.State {
	case "Alert", "Warn", "No Data":
	default:
		return
	}
	if !live {
		return
	}
	since, before := from, true
	switch {
	case notOK != nil:
		since, before = notOK.notOK, notOK.before
	case len(trs) > 0:
		// Its last transitions left it OK, yet it isn't: it changed after.
		return
	default:
		// Nothing in the window: it was already like this when it began.
		if c := datadog.ParseTime(m.Created); !c.IsZero() && c.After(from) {
			since, before = c, false
		}
	}
	rm.NotOKSince, rm.NotOKBefore = &since, before
}

type transition struct {
	at       time.Time
	from, to string
}

// netTransitions collapses the transitions that share an instant (a
// monitor re-evaluating as it's created or edited flips through states in
// the same second) into the one they add up to — none when they come back
// to where they started.
func netTransitions(evs []datadog.EventV2) []transition {
	var out []transition
	for i := 0; i < len(evs); {
		j := i + 1
		for j < len(evs) && evs[j].Timestamp.Equal(evs[i].Timestamp) {
			j++
		}
		if j == i+1 {
			out = append(out, transition{evs[i].Timestamp, evs[i].Monitor.FromState, evs[i].Monitor.ToState})
			i = j
			continue
		}
		balance := map[string]int{}
		for _, e := range evs[i:j] {
			balance[e.Monitor.FromState]++
			balance[e.Monitor.ToState]--
		}
		var from, to string
		for _, st := range []string{"OK", "Warn", "Alert", "No Data", "Ignored", "Skipped", "Unknown"} {
			switch {
			case balance[st] > 0 && from == "":
				from = st
			case balance[st] < 0 && to == "":
				to = st
			}
		}
		if from != "" && to != "" {
			out = append(out, transition{evs[i].Timestamp, from, to})
		}
		i = j
	}
	return out
}

type ruleContext struct {
	now        time.Time
	window     string // "30d"
	history    bool   // the alert history was read
	span       time.Duration
	dataFailed bool // some monitors' data couldn't be read
	twins      []int64
	downtime   string // the id of the active downtime with no end on it
}

var (
	reDraft         = regexp.MustCompile(`(?i)\b(draft|test|testing|wip|tmp|temp)\b`)
	reQuietByDesign = regexp.MustCompile(`(?i)\b(draft|diag|debug|test|testing|wip|tmp|temp)\b`)
	rePlaceholder   = regexp.MustCompile(`\w*placeholder\w*|\$[A-Za-z_]\w*`)
	reCloudQuery    = regexp.MustCompile(`\b(gcp|aws|azure)\.[\w.]+\{`)
	reZeroFilled    = regexp.MustCompile(`default_zero\(|\.fill\(\s*(zero|0)`)
	reCountish      = regexp.MustCompile(`as_count\(\)|\.hits\{|\.errors\{|\.count\{`)
	reTrafficish    = regexp.MustCompile(`(?i)hits|requests?\b|throughput`)
)

// reviewRules are a monitor's problems, most important first.
func reviewRules(m datadog.Monitor, rm *reviewedMonitor, c ruleContext) []reviewProblem {
	id := strconv.FormatInt(m.ID, 10)
	var out []reviewProblem
	add := func(p reviewProblem) { out = append(out, p) }
	metric := m.Type == "query alert" || m.Type == "metric alert"
	p := reMetricMonitor.FindStringSubmatch(m.Query)
	comparator, window := "", 0
	critical, hasCritical := thresholdOf(m, "critical")
	if p != nil {
		comparator = p[4]
		if mm := reWindow.FindStringSubmatch(p[2]); mm != nil {
			window = windowMinutes(mm[1], mm[2])
		}
		if !hasCritical {
			critical, hasCritical = parseFloat(p[5])
		}
	}
	above := comparator == ">" || comparator == ">="
	below := comparator == "<" || comparator == "<="

	// To fix.
	placeholder := ""
	if metric {
		placeholder = rePlaceholder.FindString(m.Query)
	}
	if placeholder != "" {
		add(reviewProblem{Rule: "unfinished-query", Severity: "fix", What: "Its query was never filled in (" + placeholder + ")",
			Why:  "it was made from a template and left with a placeholder: it matches no data, so it can never alert",
			Hint: fmt.Sprintf("put the real operation, service and env in its query (datadog monitors edit %s --query …), or delete it (datadog monitors delete %s)", id, id)})
	}
	if len(rm.Notifies) == 0 && len(rm.Feeds) == 0 {
		pr := reviewProblem{Rule: "silent", Severity: "fix", What: "Notifies no one",
			Why:  "its message has no @handle: it changes state and nobody hears",
			Hint: fmt.Sprintf("add the team's @handle or @slack-<channel> to its message (datadog monitors edit %s --message …)", id)}
		if reQuietByDesign.MatchString(m.Name) {
			pr.Severity, pr.What, pr.Why = "tidy", "Notifies no one (a draft or a diagnostic)", "fine while nobody relies on it"
		}
		add(pr)
	}
	if c.history && placeholder == "" && stuckNow(rm, c) {
		how := "for " + fmtLong(c.now.Sub(*rm.NotOKSince))
		if rm.NotOKBefore {
			how = "since before the window (" + c.window + ")"
		}
		pr := reviewProblem{Rule: "stuck", Severity: "fix", What: rm.State + " " + how,
			Why:   "an alert nobody acts on teaches everyone to ignore alerts",
			Check: fmt.Sprintf("datadog monitors explain %s --since 7d", id),
			Hint:  fmt.Sprintf("fix what it warns about or retune it; to silence it meanwhile: datadog downtimes schedule --monitor %s --scope '*' -d 7d -m '<why>'", id)}
		if rm.State == "No Data" {
			pr.Why = "its data stopped arriving: what it watched may be gone, or its query no longer matches anything"
			pr.Hint = fmt.Sprintf("fix its query, or delete it if what it watched is gone (datadog monitors delete %s)", id)
		}
		add(pr)
	}
	if d := rm.Data; d != nil && d.Points == 0 && placeholder == "" && rm.State == "OK" && !reCountish.MatchString(m.Query) {
		add(reviewProblem{Rule: "ok-without-data", Severity: "fix", What: "Shows OK, but its query had no data in " + d.Span,
			Why:   "it's showing the last state it knew: it watches nothing, and would stay OK through anything",
			Check: fmt.Sprintf("datadog monitors explain %s --since 7d", id),
			Hint:  fmt.Sprintf("fix its query (the metric or its tags may have changed), or delete it (datadog monitors delete %s)", id)})
	}
	count := reCountish.MatchString(m.Query)
	if metric && below && p != nil && !strings.Contains(p[3], "/") && (count || rm.Kind == "traffic" || reTrafficish.MatchString(m.Query)) && !reZeroFilled.MatchString(m.Query) {
		o := m.Options
		var fix, hint string
		switch {
		case o.OnMissingData == "show_and_notify_no_data", o.OnMissingData == "default" && count:
			// Missing data alerts, or counts as zero: it sees traffic stop.
		case o.OnMissingData != "":
			fix = fmt.Sprintf("datadog monitors edit %s --option on_missing_data=show_and_notify_no_data", id)
			if count {
				hint = "or on_missing_data=default: a count with no data then counts as zero, and the floor itself alerts"
			}
		case !o.NotifyNoData:
			fix = fmt.Sprintf("datadog monitors edit %s --option notify_no_data=true --option no_data_timeframe=%d", id, 2*max(window, 5))
		}
		if fix != "" {
			add(reviewProblem{Rule: "floor-goes-quiet", Severity: "fix", What: "Watches a floor, but goes quiet when there's nothing at all",
				Why: "when requests (or whatever it counts) stop completely the metric disappears instead of reaching zero: the monitor sees no data and tells no one — the worst outage looks like silence",
				Fix: fix, Hint: hint})
		}
	}

	// Worth a look.
	switch {
	case rm.Alerts >= 4 && rm.Flaps*2 >= rm.Alerts:
		pr := reviewProblem{Rule: "flapping", Severity: "look",
			What:  fmt.Sprintf("Flaps: %d alerts in %s, %d over within 15 minutes", rm.Alerts, c.window, rm.Flaps),
			Why:   "an alert that's over before anyone looks is noise; a recovery threshold short of the alert one, or a longer window, waits until it's real",
			Check: fmt.Sprintf("datadog monitors explain %s --since 7d", id)}
		_, hasRecovery := m.Options.Thresholds["critical_recovery"]
		if hasCritical && critical != 0 && !hasRecovery && (above || below) {
			recovery := critical - 0.2*math.Abs(critical)
			if below {
				recovery = critical + 0.2*math.Abs(critical)
			}
			pr.Fix = fmt.Sprintf("datadog monitors edit %s --threshold critical_recovery=%s", id, fmtNum(roundTo(recovery, 2)))
		}
		if longer := longerWindow(m.Query); longer != "" {
			cmd := fmt.Sprintf("datadog monitors edit %s --query %s", id, shellQuote(longer))
			if pr.Fix == "" {
				pr.Fix = cmd
			} else {
				pr.Hint = "or evaluate over a longer window: " + cmd
			}
		}
		add(pr)
	case c.history && c.span > 0 && time.Duration(rm.MinutesAlerting)*time.Minute >= c.span/4 && !stuckNow(rm, c):
		add(reviewProblem{Rule: "mostly-alerting", Severity: "look",
			What:  fmt.Sprintf("Alerting %s of the last %s", fmtLong(time.Duration(rm.MinutesAlerting)*time.Minute), c.window),
			Why:   "an alert that's on most of the time stops meaning anything: fix the cause or move the threshold to what's actually abnormal",
			Check: fmt.Sprintf("datadog monitors explain %s --since 7d", id)})
	case rm.Alerts >= 10:
		add(reviewProblem{Rule: "noisy", Severity: "look", What: fmt.Sprintf("Alerted %d times in %s", rm.Alerts, c.window),
			Why:   "either a problem that keeps coming back and nobody fixes, or a threshold too tight: every alert should need a person",
			Check: fmt.Sprintf("datadog monitors explain %s --since 7d", id)})
	}
	if rm.Priority == 1 || rm.Priority == 2 {
		if r := m.Options.RenotifyInterval; r == nil || *r == 0 {
			every := 60
			if rm.Priority == 1 {
				every = 30
			}
			add(reviewProblem{Rule: "never-reminds", Severity: "look", What: fmt.Sprintf("A P%d that never reminds", rm.Priority),
				Why: "if the first notification gets missed, nothing follows it while it keeps alerting",
				Fix: fmt.Sprintf("datadog monitors edit %s --option renotify_interval=%d", id, every)})
		}
	}
	if metric {
		if cloud := reCloudQuery.FindStringSubmatch(m.Query); cloud != nil {
			if d := m.Options.EvaluationDelay; d == nil || *d == 0 {
				delay := 300
				if cloud[1] == "aws" {
					delay = 900
				}
				add(reviewProblem{Rule: "no-evaluation-delay", Severity: "look", What: "Judges cloud metrics before they arrive",
					Why: cloud[1] + " metrics reach Datadog minutes late: without an evaluation delay it evaluates windows that are still filling in",
					Fix: fmt.Sprintf("datadog monitors edit %s --option evaluation_delay=%d", id, delay)})
			}
		}
	}
	if d := rm.Data; d != nil && d.Evaluated && d.Points > 0 && c.history && hasCritical && rm.Alerts == 0 && rm.Warnings == 0 {
		switch {
		case above && (rm.Kind == "errors" || rm.Kind == "latency") && rm.Priority != 1 && critical > 0 && d.Max > 0 && d.Max < critical*0.3:
			pr := reviewProblem{Rule: "never-close", Severity: "look",
				What:  fmt.Sprintf("Never came close to firing: over %s its data peaked at %s, against a threshold of %s", d.Span, fmtVal(d.Max), fmtVal(critical)),
				Why:   fmt.Sprintf("it only catches a disaster; to catch trouble sooner the threshold has to sit nearer what normal looks like (its worst 1%% was %s)", fmtVal(d.P99)),
				Check: fmt.Sprintf("datadog monitors explain %s --since 7d", id)}
			suggest := roundTo(math.Max(d.P99*2, d.Max*1.2), 2)
			if w, ok := thresholdOf(m, "warning"); suggest < critical && (!ok || w < suggest) {
				pr.Fix = fmt.Sprintf("datadog monitors edit %s --threshold critical=%s", id, fmtNum(suggest))
				pr.Hint = "about twice its worst 1%, above its peak: check it against a bad day before applying"
			}
			add(pr)
		case below && critical > 1 && d.Min > critical*3:
			add(reviewProblem{Rule: "never-close", Severity: "look", Hint: "fine if it's only meant to catch traffic stopping; to catch a big drop sooner, raise it (and its recovery) nearer that low",
				What:  fmt.Sprintf("Never came close to firing: over %s its data never went below %s, against a floor of %s", d.Span, fmtVal(d.Min), fmtVal(critical)),
				Why:   fmt.Sprintf("a floor that far under normal only catches traffic almost stopping (its lowest 1%% was %s)", fmtVal(d.P01)),
				Check: fmt.Sprintf("datadog monitors explain %s --since 7d", id)})
		}
	}
	if rm.NoData >= 3 && rm.Kind != "traffic" {
		o := m.Options
		switch {
		case o.OnMissingData == "show_and_notify_no_data":
			add(reviewProblem{Rule: "no-data-noise", Severity: "look", What: fmt.Sprintf("Went to No Data %d times", rm.NoData),
				Why: "its data is sparse: No Data notifications turn into noise",
				Fix: fmt.Sprintf("datadog monitors edit %s --option on_missing_data=show_no_data", id)})
		case o.OnMissingData == "" && o.NotifyNoData:
			add(reviewProblem{Rule: "no-data-noise", Severity: "look", What: fmt.Sprintf("Went to No Data %d times", rm.NoData),
				Why:  "its data is sparse: No Data notifications turn into noise",
				Fix:  fmt.Sprintf("datadog monitors edit %s --option notify_no_data=false", id),
				Hint: "or wait longer before calling it missing: --option no_data_timeframe=<minutes>"})
		}
	}
	for _, dt := range m.MatchingDowntimes {
		if dt.End == nil {
			pr := reviewProblem{Rule: "muted-forever", Severity: "look", What: "Muted with no end",
				Why: "a mute nobody lifts turns into a monitor nobody has"}
			if c.downtime != "" {
				pr.Fix = "datadog downtimes cancel " + c.downtime
			} else {
				pr.Hint = "find its downtime in 'datadog downtimes' and cancel it, or give it an end"
			}
			add(pr)
			break
		}
	}

	// To tidy.
	if !strings.Contains(m.Message, "http") {
		add(reviewProblem{Rule: "no-runbook", Severity: "tidy", What: "No runbook or dashboard link",
			Why: "whoever gets paged starts from nothing", Hint: "add a link to its runbook or dashboard to the message"})
	}
	if p != nil && !strings.Contains(m.Message, "{{value}}") {
		add(reviewProblem{Rule: "no-value", Severity: "tidy", What: "The notification doesn't say the value",
			Why: "the value against the threshold says how bad it is at a glance", Hint: "add \"{{value}} (threshold {{threshold}})\" to its message"})
	}
	owned := len(monitorServices(m)) > 0
	for _, t := range m.Tags {
		owned = owned || strings.HasPrefix(t, "team:")
	}
	if !owned {
		add(reviewProblem{Rule: "no-owner", Severity: "tidy", What: "No service or team tag",
			Why:  "coverage reports and routing can't tell whose it is",
			Hint: fmt.Sprintf("add service:<service> or team:<team> to its tags: datadog monitors edit %s --tags %s", id, shellQuote(strings.Join(append(append([]string(nil), m.Tags...), "team:<team>"), ",")))})
	}
	if reDraft.MatchString(m.Name) {
		if mod := datadog.ParseTime(m.Modified); !mod.IsZero() && c.now.Sub(mod) >= 14*24*time.Hour {
			add(reviewProblem{Rule: "old-draft", Severity: "tidy", What: "A draft, untouched for " + fmtLong(c.now.Sub(mod)),
				Why: "a draft that alerts (or doesn't) muddles what's being watched", Hint: fmt.Sprintf("finish it, or delete it: datadog monitors delete %s", id)})
		}
	}
	if len(c.twins) > 0 {
		var ids []string
		for _, t := range c.twins {
			ids = append(ids, "#"+strconv.FormatInt(t, 10))
		}
		add(reviewProblem{Rule: "duplicate", Severity: "tidy", What: "Same query as " + strings.Join(ids, ", "),
			Why: "two monitors on the same data notify twice for one problem", Hint: "keep one"})
	}
	return out
}

// stuckNow says whether a monitor has been anything but OK for days.
func stuckNow(rm *reviewedMonitor, c ruleContext) bool {
	return rm.NotOKSince != nil && c.now.Sub(*rm.NotOKSince) >= stuckAfter
}

func notifiesOf(m datadog.Monitor) []string {
	var out []string
	seen := map[string]bool{}
	for _, h := range reHandle.FindAllString(m.Message, -1) {
		h = strings.TrimRight(h, ".,;:")
		if !seen[h] && !strings.HasPrefix(h, "@is_") {
			seen[h] = true
			out = append(out, h)
		}
	}
	return out
}

func thresholdOf(m datadog.Monitor, key string) (float64, bool) {
	switch v := m.Options.Thresholds[key].(type) {
	case float64:
		return v, true
	case string:
		return parseFloat(v)
	}
	return 0, false
}

func parseFloat(s string) (float64, bool) {
	f, err := strconv.ParseFloat(strings.TrimSpace(s), 64)
	return f, err == nil
}

func normalizeQuery(q string) string { return strings.Join(strings.Fields(q), " ") }

func windowMinutes(n, unit string) int {
	v, _ := strconv.Atoi(n)
	switch unit {
	case "s":
		return max(1, v/60)
	case "h":
		return v * 60
	case "d":
		return v * 24 * 60
	case "w":
		return v * 7 * 24 * 60
	}
	return v
}

// longerWindow doubles a metric monitor's window in its query:
// "avg(last_5m):…" → "avg(last_10m):…".
func longerWindow(query string) string {
	p := reMetricMonitor.FindStringSubmatch(query)
	if p == nil {
		return ""
	}
	mm := reWindow.FindStringSubmatch(p[2])
	if mm == nil {
		return ""
	}
	n, _ := strconv.Atoi(mm[1])
	return strings.Replace(query, "("+p[2]+")", fmt.Sprintf("(last_%d%s)", 2*n, mm[2]), 1)
}

// roundTo rounds to a number of significant digits.
func roundTo(v float64, digits int) float64 {
	if v == 0 {
		return 0
	}
	mag := math.Pow(10, float64(digits)-math.Ceil(math.Log10(math.Abs(v))))
	return math.Round(v*mag) / mag
}

func shellQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }

func verdictOf(ps []reviewProblem) string {
	rank := map[string]int{"fine": 0, "tidy": 1, "look": 2, "fix": 3}
	best := "fine"
	for _, p := range ps {
		if rank[p.Severity] > rank[best] {
			best = p.Severity
		}
	}
	return best
}

// ─── output ──────────────────────────────────────────────────────

var verdictTitles = []struct{ key, title, count string }{
	{"fix", "To fix", "to fix"}, {"look", "Worth a look", "worth a look"}, {"tidy", "To tidy", "to tidy"}, {"fine", "Fine", "fine"},
}

func (r *monitorsReview) header() string {
	return fmt.Sprintf("Monitors review · %s · %s", r.Scope, fmtWindow(r.From, r.To))
}

func (r *monitorsReview) counts() string {
	if len(r.Monitors) == 0 {
		return "no monitors in scope"
	}
	parts := []string{pluralOf(len(r.Monitors), "monitor", "monitors"), pluralOf(r.Alerts, "alert", "alerts") + " in the window"}
	for _, v := range verdictTitles {
		if n := r.Summary[v.key]; n > 0 {
			parts = append(parts, fmt.Sprintf("%d %s", n, v.count))
		}
	}
	return strings.Join(parts, " · ")
}

func (rm reviewedMonitor) activity() string {
	s := orDash(rm.State) + " · " + pluralOf(rm.Alerts, "alert", "alerts")
	if rm.MinutesAlerting > 0 {
		s += " (" + fmtLong(time.Duration(rm.MinutesAlerting)*time.Minute) + " alerting)"
	}
	switch d := rm.Data; {
	case d == nil:
	case d.Points == 0:
		s += " · no data over " + d.Span
	default:
		s += fmt.Sprintf(" · data %s–%s over %s", fmtVal(d.Min), fmtVal(d.Max), d.Span)
	}
	return s
}

// fmtVal is a value for people: 42.11, 0.0329, 16.9k, 1.2M.
func fmtVal(v float64) string {
	unit, div := "", 1.0
	switch a := math.Abs(v); {
	case a >= 1e9:
		unit, div = "G", 1e9
	case a >= 1e6:
		unit, div = "M", 1e6
	case a >= 1e4:
		unit, div = "k", 1e3
	default:
		return fmtNum(v)
	}
	x := v / div
	digits := 1
	if math.Abs(x) >= 100 {
		digits = 0
	}
	return strings.TrimSuffix(strconv.FormatFloat(x, 'f', digits, 64), ".0") + unit
}

// fmtLong is a long span for people: 45 min, 5h20, 3 days.
func fmtLong(d time.Duration) string {
	if d >= 48*time.Hour {
		return fmt.Sprintf("%d days", int(d.Hours()/24))
	}
	return fmtAgo(d)
}

func (r *monitorsReview) empty() string {
	if r.Service != "" {
		return "Nothing watches " + r.Service + ": 'datadog coverage --service " + r.Service + "' says what's missing"
	}
	return "No monitors in scope."
}

func (r *monitorsReview) text(tty bool) string {
	var b strings.Builder
	width := termWidth(110)
	dim := func(s string) string {
		if tty {
			return ui.Dimmed.Render(s)
		}
		return s
	}
	section := func(s string) string {
		if tty {
			return ui.SectionHeader.Render(s)
		}
		return s
	}
	title := " " + r.header()
	if tty {
		title = ui.Title.Render(title)
	}
	b.WriteString(title + "\n " + dim(r.counts()) + "\n")
	for _, n := range r.Notes {
		b.WriteString(" " + dim(n) + "\n")
	}
	if len(r.Monitors) == 0 {
		b.WriteString("\n " + r.empty() + "\n")
	}
	for _, v := range verdictTitles[:2] {
		first := true
		for _, rm := range r.Monitors {
			if rm.Verdict != v.key {
				continue
			}
			if first {
				b.WriteString("\n " + section(v.title) + "\n")
				first = false
			}
			mark := "!"
			if v.key == "fix" {
				mark = "✗"
				if tty {
					mark = ui.ErrorStyle.Render(mark)
				}
			}
			b.WriteString(wrapIndent(mark+" "+rm.Name+"  "+dim(fmt.Sprintf("#%d · %s", rm.ID, rm.activity())), width, "   ", "     ") + "\n")
			var tidy []string
			for _, p := range rm.Problems {
				if p.Severity == "tidy" {
					tidy = append(tidy, strings.ToLower(p.What[:1])+p.What[1:])
					continue
				}
				b.WriteString(wrapIndent(p.What+" — "+p.Why, width, "     ", "       ") + "\n")
				if p.Fix != "" {
					b.WriteString("       " + dim("fix:  ") + " " + p.Fix + "\n")
				}
				if p.Check != "" {
					b.WriteString("       " + dim("check: "+p.Check) + "\n")
				}
				if p.Hint != "" {
					b.WriteString(wrapIndent(dim(p.Hint), width, "       ") + "\n")
				}
			}
			if len(tidy) > 0 {
				b.WriteString(wrapIndent(dim("also: "+strings.Join(tidy, " · ")), width, "     ", "       ") + "\n")
			}
		}
	}
	// Tidying, summed up by what's missing.
	type group struct {
		what string
		ids  []string
	}
	var groups []*group
	byWhat := map[string]*group{}
	for _, rm := range r.Monitors {
		if rm.Verdict != "tidy" {
			continue
		}
		for _, p := range rm.Problems {
			what := p.What
			switch p.Rule {
			case "old-draft":
				what = "Old drafts"
			case "duplicate":
				what = "Same query as another monitor"
			}
			g := byWhat[what]
			if g == nil {
				g = &group{what: what}
				byWhat[what] = g
				groups = append(groups, g)
			}
			g.ids = append(g.ids, fmt.Sprintf("#%d", rm.ID))
		}
	}
	if len(groups) > 0 {
		b.WriteString("\n " + section("To tidy") + " " + dim("("+pluralOf(r.Summary["tidy"], "monitor", "monitors")+"; each one with --md)") + "\n")
		for _, g := range groups {
			ids, more := g.ids, ""
			if len(ids) > 8 {
				ids, more = ids[:8], fmt.Sprintf(" +%d", len(ids)-8)
			}
			b.WriteString(wrapIndent(fmt.Sprintf("· %s (%d): %s", g.what, len(g.ids), dim(strings.Join(ids, " ")+more)), width, "   ", "     ") + "\n")
		}
	}
	if n := r.Summary["fine"]; n > 0 {
		var names []string
		for _, rm := range r.Monitors {
			if rm.Verdict == "fine" && len(names) < 6 {
				names = append(names, rm.Name)
			}
		}
		line := "Fine: " + strings.Join(names, " · ")
		if n > len(names) {
			line += fmt.Sprintf(" +%d", n-len(names))
		}
		b.WriteString("\n" + wrapIndent(dim(line), width, " ", "   ") + "\n")
	}
	for _, src := range failedKeys(r.Failed) {
		fmt.Fprintf(&b, "\n ! couldn't read the %s: %s\n", src, r.Failed[src])
	}
	if len(r.Monitors) > 0 {
		b.WriteString("\n " + dim("Nothing was changed: a fix runs after a yes. What no monitor watches: datadog coverage") + "\n")
	}
	return b.String()
}

func (r *monitorsReview) markdown() string {
	var b strings.Builder
	fmt.Fprintf(&b, "# %s\n\n%s.\n", r.header(), capitalize(r.counts()))
	for _, n := range r.Notes {
		b.WriteString("\n_" + capitalize(n) + "._\n")
	}
	if len(r.Monitors) == 0 {
		b.WriteString("\n" + r.empty() + "\n")
	}
	for _, v := range verdictTitles {
		var ms []reviewedMonitor
		for _, rm := range r.Monitors {
			if rm.Verdict == v.key {
				ms = append(ms, rm)
			}
		}
		if len(ms) == 0 {
			continue
		}
		fmt.Fprintf(&b, "\n## %s\n", v.title)
		switch v.key {
		case "tidy":
			b.WriteString("\n| Monitor | To tidy | How |\n|---|---|---|\n")
			for _, rm := range ms {
				var what, how []string
				for _, p := range rm.Problems {
					what = append(what, p.What)
					if p.Hint != "" {
						how = append(how, p.Hint)
					}
				}
				fmt.Fprintf(&b, "| [%s](%s) #%d | %s | %s |\n", mdCell(mdLinkText(rm.Name)), rm.URL, rm.ID, mdCell(strings.Join(what, "; ")), mdCell(strings.Join(how, "; ")))
			}
		case "fine":
			var names []string
			for _, rm := range ms {
				names = append(names, fmt.Sprintf("[%s](%s)", mdLinkText(rm.Name), rm.URL))
			}
			b.WriteString("\n" + strings.Join(names, " · ") + "\n")
		default:
			for _, rm := range ms {
				fmt.Fprintf(&b, "\n### [%s](%s)\n\n`%s` · #%d · %s\n\n", mdLinkText(rm.Name), rm.URL, strings.ReplaceAll(rm.Query, "`", "'"), rm.ID, rm.activity())
				for _, p := range rm.Problems {
					fmt.Fprintf(&b, "- **%s** — %s.\n", p.What, p.Why)
					if p.Fix != "" {
						b.WriteString("  ```sh\n  " + p.Fix + "\n  ```\n")
					}
					if p.Hint != "" {
						b.WriteString("  " + capitalize(p.Hint) + ".\n")
					}
					if p.Check != "" {
						b.WriteString("  Check first: `" + p.Check + "`\n")
					}
				}
			}
		}
	}
	for _, src := range failedKeys(r.Failed) {
		fmt.Fprintf(&b, "\n> Couldn't read the %s: %s\n", src, r.Failed[src])
	}
	if len(r.Monitors) > 0 {
		b.WriteString("\n_Nothing was changed: each fix is a command to run after a yes. What no monitor watches: `datadog coverage`._\n")
	}
	return b.String()
}

func failedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
