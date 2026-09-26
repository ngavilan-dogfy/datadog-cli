// Package demo is a make-believe Datadog org for 'datadog ui --demo': a web
// store whose checkout is having a bad afternoon because the payment
// provider is slow. Dashboards, monitors, logs, metrics, incidents, SLOs
// and events all tell that same story, so the UI can be tried (and
// screenshotted) without keys. Nothing here talks to the network.
package demo

import (
	"fmt"
	"math"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/ngavilan-dogfy/datadog-cli/datadog"
)

// API implements the calls 'datadog ui' makes, from generated data.
type API struct {
	start time.Time

	mu        sync.Mutex
	downtimes map[string]datadog.DowntimeData
	nextDT    int
}

// New starts the demo clock: the incident began 25 minutes ago.
func New() *API {
	a := &API{start: time.Now(), downtimes: map[string]datadog.DowntimeData{}}
	// The inventory monitor is muted while the service is being migrated.
	id := int64(4106)
	msg := "Inventory sync is being migrated to the new queue"
	a.schedule("*", msg, a.start.Add(-2*time.Hour), a.start.Add(4*time.Hour), &id)
	return a
}

func (a *API) now() time.Time { return time.Now() }

// ─── dashboards ──────────────────────────────────────────────────

func (a *API) ListDashboards() ([]datadog.DashboardSummary, error) { return a.dashboardList(), nil }

func (a *API) GetDashboard(id string) (map[string]interface{}, error) { return a.dashboard(id) }

// There's no Datadog behind the demo: links go nowhere.
func (a *API) DashboardURL(string) string { return "" }
func (a *API) BrowseURL(string) string    { return "" }

// ─── metrics ─────────────────────────────────────────────────────

func stepOf(from, to, interval int64) time.Duration {
	step := time.Duration(interval) * time.Millisecond
	if step <= 0 {
		step = time.Duration((to-from)/120) * time.Millisecond
	}
	if step < 10*time.Second {
		step = 10 * time.Second
	}
	// At most 1500 points, like the API.
	if n := time.Duration(to-from) * time.Millisecond / step; n > 1500 {
		step = time.Duration(to-from) * time.Millisecond / 1500
	}
	return step
}

// query evaluates one formula-API query (metrics or events) into a set.
func (a *API) query(q map[string]any, times []int64, step time.Duration) (set, error) {
	switch ds, _ := q["data_source"].(string); ds {
	case "metrics", "cloud_cost", "":
		text, _ := q["query"].(string)
		return a.expression(text, times, step)
	default: // logs, spans, rum, events…: counts
		search := "*"
		if s, ok := q["search"].(map[string]any); ok {
			if qs, ok := s["query"].(string); ok && strings.TrimSpace(qs) != "" {
				search = qs
			}
		}
		var by []string
		if gb, ok := q["group_by"].([]any); ok {
			for _, g := range gb {
				if gm, ok := g.(map[string]any); ok {
					if f, ok := gm["facet"].(string); ok {
						by = append(by, f)
					}
				}
			}
		}
		if ds == "spans" {
			search = strings.ReplaceAll(search, "@http.status_code:5*", "status:error")
		}
		return a.logCounts(search, by, times, step), nil
	}
}

// expression evaluates a v1 metric expression ("sum:a{x} / sum:b{x}").
func (a *API) expression(text string, times []int64, step time.Duration) (set, error) {
	e := &evaluator{src: strings.TrimSpace(text), n: len(times),
		name: func(string) (set, bool) { return set{}, false },
		term: func(m []string) set {
			q := parseTerm(m)
			_, series := a.seriesAt(q, times, step)
			out := set{byKey: map[string]namedSeries{}}
			for _, s := range series {
				k := strings.Join(s.tags, ",")
				out.order = append(out.order, k)
				out.byKey[k] = s
			}
			return out
		}}
	return e.eval()
}

func (a *API) seriesAt(q metricQuery, times []int64, step time.Duration) ([]int64, []namedSeries) {
	if len(times) == 0 {
		return nil, nil
	}
	from := time.UnixMilli(times[0])
	to := time.UnixMilli(times[len(times)-1]).Add(step)
	return a.series(q, from, to, step)
}

// unitFor guesses a formula's unit from its first query.
func unitFor(formula string, queries []map[string]any) *datadog.QueryUnit {
	if strings.Contains(formula, "/") && strings.Contains(formula, "100") {
		return unitPercent
	}
	if strings.ContainsAny(formula, "/*") {
		return nil
	}
	for _, q := range queries {
		if name, _ := q["name"].(string); name == strings.TrimSpace(formula) {
			if text, _ := q["query"].(string); text != "" {
				if m := reMetricTerm.FindStringSubmatch(text); m != nil {
					return unitOf(parseTerm(m))
				}
			}
		}
	}
	return nil
}

func timesFor(from, to int64, step time.Duration) []int64 {
	var times []int64
	start := time.UnixMilli(from).Truncate(step)
	for t := start; t.UnixMilli() < to; t = t.Add(step) {
		times = append(times, t.UnixMilli())
	}
	return times
}

// formulasOf is the request's formulas, or one per query when it has none.
func formulasOf(r datadog.FormulaRequest) []string {
	var out []string
	for _, f := range r.Formulas {
		text, _ := f["formula"].(string)
		out = append(out, text)
	}
	if len(out) == 0 {
		for _, q := range r.Queries {
			name, _ := q["name"].(string)
			out = append(out, name)
		}
	}
	return out
}

// evalFormulas runs each query, then each formula over the results.
func (a *API) evalFormulas(r datadog.FormulaRequest, times []int64, step time.Duration, reduceBy map[string]string) ([]set, error) {
	byName := map[string]set{}
	for _, q := range r.Queries {
		name, _ := q["name"].(string)
		s, err := a.query(q, times, step)
		if err != nil {
			return nil, err
		}
		if reduceBy != nil { // scalar: aggregate each series first
			aggr := reduceBy[name]
			for k, ns := range s.byKey {
				ns.values = []float64{reduce(ns.values, aggr)}
				s.byKey[k] = ns
			}
		}
		byName[name] = s
	}
	n := len(times)
	if reduceBy != nil {
		n = 1
	}
	var out []set
	for _, text := range formulasOf(r) {
		e := &evaluator{src: text, n: n, name: func(id string) (set, bool) { s, ok := byName[id]; return s, ok },
			term: func([]string) set { return constant(math.NaN(), n) }}
		s, err := e.eval()
		if err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, nil
}

func (a *API) QueryTimeseries(r datadog.FormulaRequest) (*datadog.TimeseriesResult, error) {
	step := stepOf(r.From, r.To, r.Interval)
	times := timesFor(r.From, r.To, step)
	sets, err := a.evalFormulas(r, times, step, nil)
	if err != nil {
		return nil, err
	}
	res := &datadog.TimeseriesResult{Times: times}
	formulas := formulasOf(r)
	for i, s := range sets {
		unit := unitFor(formulas[i], r.Queries)
		for _, k := range s.order {
			ns := s.byKey[k]
			vals := make([]*float64, len(ns.values))
			for j, v := range ns.values {
				if !math.IsNaN(v) && !math.IsInf(v, 0) {
					v := v
					vals[j] = &v
				}
			}
			tags := ns.tags
			if len(tags) == 0 {
				tags = []string{"*"}
			}
			res.Series = append(res.Series, datadog.TimeseriesSeries{GroupTags: tags, QueryIndex: i, Unit: unit, Values: vals})
		}
	}
	return res, nil
}

func (a *API) QueryScalar(r datadog.FormulaRequest) (*datadog.ScalarResult, error) {
	step := stepOf(r.From, r.To, 0)
	times := timesFor(r.From, r.To, step)
	aggr := map[string]string{}
	for _, q := range r.Queries {
		name, _ := q["name"].(string)
		ag, _ := q["aggregator"].(string)
		if ds, _ := q["data_source"].(string); ds != "" && ds != "metrics" {
			ag = "sum" // event counts add up over the window
		}
		aggr[name] = ag
	}
	sets, err := a.evalFormulas(r, times, step, aggr)
	if err != nil {
		return nil, err
	}
	res := &datadog.ScalarResult{}
	if len(sets) == 0 {
		return res, nil
	}
	s := sets[0]
	formula := formulasOf(r)[0]
	group := datadog.ScalarColumn{Name: "group", Type: "group"}
	numbers := datadog.ScalarColumn{Name: formula, Type: "number", Unit: unitFor(formula, r.Queries)}
	grouped := false
	for _, k := range s.order {
		ns := s.byKey[k]
		var vals []string
		for _, t := range ns.tags {
			_, v, _ := strings.Cut(t, ":")
			vals = append(vals, v)
		}
		if len(vals) > 0 {
			grouped = true
		}
		group.Groups = append(group.Groups, vals)
		var p *float64
		if len(ns.values) > 0 && !math.IsNaN(ns.values[0]) {
			v := ns.values[0]
			p = &v
		}
		numbers.Values = append(numbers.Values, p)
	}
	if grouped {
		res.Columns = append(res.Columns, group)
	}
	res.Columns = append(res.Columns, numbers)
	return res, nil
}

func (a *API) QueryMetrics(query string, from, to int64) (*datadog.MetricsQueryResponse, error) {
	step := stepOf(from*1000, to*1000, 0)
	times := timesFor(from*1000, to*1000, step)
	s, err := a.expression(query, times, step)
	if err != nil {
		return nil, err
	}
	var unit []datadog.MetricsUnit
	if m := reMetricTerm.FindStringSubmatch(query); m != nil {
		if u := unitOf(parseTerm(m)); u != nil && !strings.ContainsAny(strings.TrimSpace(query[len(m[0]):]), "/*") {
			unit = []datadog.MetricsUnit{{Family: u.Family, Name: u.Name, ShortName: u.ShortName, ScaleFactor: u.ScaleFactor}}
		}
	}
	resp := &datadog.MetricsQueryResponse{Status: "ok", FromDate: from * 1000, ToDate: to * 1000, Query: query}
	for _, k := range s.order {
		ns := s.byKey[k]
		var pts [][]float64
		for i, v := range ns.values {
			if !math.IsNaN(v) && !math.IsInf(v, 0) {
				pts = append(pts, []float64{float64(times[i]), v})
			}
		}
		if len(pts) == 0 {
			continue // no data, like a metric that stopped reporting
		}
		scope := strings.Join(ns.tags, ",")
		if scope == "" {
			scope = "*"
		}
		resp.Series = append(resp.Series, datadog.MetricsSeries{Metric: query, DisplayName: query, Scope: scope, Expression: query,
			Pointlist: pts, Unit: unit, Length: len(pts), Interval: int(step.Seconds())})
	}
	return resp, nil
}

func (a *API) SearchMetrics(q string) ([]string, error) {
	q = strings.ToLower(strings.TrimSpace(q))
	var out []string
	for _, m := range metricNames {
		if strings.Contains(m, q) {
			out = append(out, m)
		}
	}
	return out, nil
}

// ─── monitors ────────────────────────────────────────────────────

func (a *API) ListMonitors(query string, limit int) ([]datadog.Monitor, error) {
	mons := a.monitors()
	if limit > 0 && len(mons) > limit {
		mons = mons[:limit]
	}
	return mons, nil
}

func (a *API) GetMonitor(id int64) (*datadog.Monitor, error) {
	for _, m := range a.monitors() {
		if m.ID == id {
			return &m, nil
		}
	}
	return nil, fmt.Errorf("monitor %d not found", id)
}

// SearchMonitorsRich understands what manage_status widgets ask:
// status:alert, tags (service:checkout), and words from the name.
func (a *API) SearchMonitorsRich(query string, perPage int) (*datadog.MonitorSearchResponse, error) {
	var states, tags, words []string
	for _, tok := range splitQuery(strings.ToLower(query)) {
		tok = strings.Trim(tok, `()"`)
		switch {
		case tok == "" || tok == "and" || tok == "or" || tok == "*":
		case strings.HasPrefix(tok, "status:"):
			states = append(states, strings.ReplaceAll(strings.TrimPrefix(tok, "status:"), "_", " "))
		case strings.HasPrefix(tok, "tag:"):
			tags = append(tags, strings.TrimPrefix(tok, "tag:"))
		case strings.Contains(tok, ":"):
			tags = append(tags, tok)
		default:
			words = append(words, tok)
		}
	}
	res := &datadog.MonitorSearchResponse{}
	for _, m := range a.monitors() {
		state := strings.ToLower(m.OverallState)
		if len(states) > 0 && !contains(states, state) && !(state == "warn" && contains(states, "warning")) {
			continue
		}
		ok := true
		for _, t := range tags {
			if !contains(m.Tags, t) {
				ok = false
			}
		}
		for _, w := range words {
			if !strings.Contains(strings.ToLower(m.Name), w) {
				ok = false
			}
		}
		if !ok {
			continue
		}
		res.Monitors = append(res.Monitors, datadog.MonitorSearchHit{ID: m.ID, Name: m.Name, Type: m.Type, Status: m.OverallState,
			Tags: m.Tags, Last_triggered_ts: a.lastTriggered(m)})
		if perPage > 0 && len(res.Monitors) == perPage {
			break
		}
	}
	return res, nil
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

func (a *API) GetMonitorGroups(id int64) ([]datadog.MonitorGroup, error) {
	switch id {
	case 4108:
		return []datadog.MonitorGroup{{Name: "host:db-primary-1", Status: "OK"}, {Name: "host:db-replica-1", Status: "OK"}, {Name: "host:db-replica-2", Status: "OK"}}, nil
	case 4112:
		return []datadog.MonitorGroup{{Name: "host:db-primary-1", Status: "OK"}, {Name: "host:db-replica-1", Status: "OK"}, {Name: "host:db-replica-2", Status: "OK"}}, nil
	case 4117:
		return []datadog.MonitorGroup{{Name: "node:gke-pool-a-1", Status: "OK"}, {Name: "node:gke-pool-a-2", Status: "OK"}, {Name: "node:gke-pool-b-1", Status: "OK"}}, nil
	}
	m, err := a.GetMonitor(id)
	if err != nil {
		return nil, err
	}
	return []datadog.MonitorGroup{{Name: "*", Status: m.OverallState, LastTriggeredTS: a.lastTriggered(*m)}}, nil
}

func (a *API) ListDowntimes() ([]datadog.DowntimeData, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	var out []datadog.DowntimeData
	for _, d := range a.downtimes {
		out = append(out, d)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

func (a *API) schedule(scope, message string, start, end time.Time, monitorID *int64) datadog.DowntimeData {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.nextDT++
	msg := message
	d := datadog.DowntimeData{ID: fmt.Sprintf("demo-dt-%d", a.nextDT), Type: "downtime", Attributes: datadog.DowntimeAttributes{
		Scope: scope, Message: &msg, Status: "active",
		MonitorIdentifier: &datadog.DowntimeMonitorID{MonitorID: monitorID},
		Schedule:          &datadog.DowntimeSchedule{Start: start.UTC().Format(time.RFC3339), End: ptr(end.UTC().Format(time.RFC3339))},
		CreatedAt:         a.now().UTC().Format(time.RFC3339),
	}}
	a.downtimes[d.ID] = d
	return d
}

func ptr[T any](v T) *T { return &v }

func (a *API) ScheduleDowntime(scope, message string, start, end time.Time, monitorID *int64) (*datadog.DowntimeData, error) {
	d := a.schedule(scope, message, start, end, monitorID)
	return &d, nil
}

func (a *API) CancelDowntime(id string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if _, ok := a.downtimes[id]; !ok {
		return fmt.Errorf("downtime %s not found", id)
	}
	delete(a.downtimes, id)
	return nil
}

// ─── logs ────────────────────────────────────────────────────────

func (a *API) SearchLogs(query, from, to string, limit int) (*datadog.LogsResponse, error) {
	return a.SearchLogsCursor(query, from, to, limit, "")
}

func (a *API) SearchLogsCursor(query, from, to string, limit int, cursor string) (*datadog.LogsResponse, error) {
	now := a.now()
	if limit <= 0 {
		limit = 25
	}
	logs, next := a.searchLogs(query, parseTime(from, now), parseTime(to, now), limit, cursor)
	resp := &datadog.LogsResponse{Data: logs}
	resp.Meta.Page.After = next
	return resp, nil
}

func (a *API) AggregateLogs(query, from, to string, groupBy []string, limit int) (*datadog.LogsAggregateResponse, error) {
	now := a.now()
	f, t := parseTime(from, now), parseTime(to, now)
	step := time.Minute
	times := timesFor(f.UnixMilli(), t.UnixMilli(), step)
	s := a.logCounts(query, groupBy, times, step)
	resp := &datadog.LogsAggregateResponse{}
	for i, k := range s.order {
		if limit > 0 && i == limit {
			break
		}
		ns := s.byKey[k]
		if sum(ns.values) == 0 {
			continue // like the API: no empty buckets
		}
		by := map[string]string{}
		for _, tag := range ns.tags {
			key, v, _ := strings.Cut(tag, ":")
			by[key] = v
		}
		resp.Data.Buckets = append(resp.Data.Buckets, datadog.LogsAggregateBucket{By: by, Computes: map[string]interface{}{"c0": sum(ns.values)}})
	}
	return resp, nil
}

// ─── the rest ────────────────────────────────────────────────────

func (a *API) ListIncidents() ([]datadog.IncidentData, error) { return a.incidents(), nil }

func (a *API) ListSLOs(string) ([]datadog.SLO, error) { return a.slos(), nil }

func (a *API) ListEvents(start, end int64, _ string) ([]datadog.Event, error) {
	var out []datadog.Event
	for _, e := range a.events() {
		if e.DateHappened >= start && e.DateHappened <= end {
			out = append(out, e)
		}
	}
	return out, nil
}
