package tui

import (
	"encoding/json"
	"fmt"
	"math"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ngavilan-dogfy/datadog-cli/datadog"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
)

// fakeDD is an in-memory Datadog: a couple of dashboards, monitors in every
// state, logs, metrics — and a record of what the UI asked and changed.
type fakeDD struct {
	mu        sync.Mutex
	queries   []string // every query string sent (timeseries/scalar/metrics/logs)
	downtimes map[string]datadog.DowntimeData
	scheduled []int64 // monitor ids muted
	canceled  []string
	nextDT    int
	intervals map[string]int64 // last rollup asked per query
}

func newFakeDD() *fakeDD { return &fakeDD{downtimes: map[string]datadog.DowntimeData{}} }

func (f *fakeDD) record(q string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.queries = append(f.queries, q)
}

func (f *fakeDD) sawQuery(sub string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, q := range f.queries {
		if strings.Contains(q, sub) {
			return true
		}
	}
	return false
}

const dashJSON = `{
 "id": "abc-123", "title": "Service health", "description": "The api at a glance", "layout_type": "ordered",
 "template_variables": [{"name": "env", "prefix": "env", "defaults": ["prod"], "available_values": ["prod", "staging"]}],
 "widgets": [
  {"id": 1, "definition": {"type": "note", "content": "# Runbook\nCheck **latency** first.\n- then errors"}, "layout": {"x":0,"y":0,"width":12,"height":1}},
  {"id": 2, "definition": {"type": "group", "title": "Traffic", "widgets": [
    {"id": 3, "definition": {"type": "query_value", "title": "p50", "precision": 0, "custom_unit": "ms", "autoscale": false,
      "requests": [{"response_format": "scalar", "queries": [{"data_source": "metrics", "name": "a", "query": "avg:latency{$env}", "aggregator": "avg"}],
                    "formulas": [{"formula": "a * 1000"}], "conditional_formats": [{"comparator": ">", "value": 500, "palette": "white_on_red"}]}]},
     "layout": {"x":0,"y":0,"width":3,"height":2}},
    {"id": 4, "definition": {"type": "timeseries", "title": "Requests by host", "markers": [{"value": "y = 80", "display_type": "error dashed"}],
      "requests": [{"display_type": "line", "queries": [{"data_source": "metrics", "name": "q", "query": "sum:requests{$env} by {host}"}], "formulas": [{"formula": "q"}]}]},
     "layout": {"x":3,"y":0,"width":9,"height":3}},
    {"id": 5, "definition": {"type": "toplist", "title": "Errors by endpoint",
      "requests": [{"response_format": "scalar", "queries": [{"data_source": "metrics", "name": "e", "query": "sum:errors{$env} by {endpoint}", "aggregator": "sum"}], "formulas": [{"formula": "e"}]}]},
     "layout": {"x":0,"y":2,"width":3,"height":2}}]},
   "layout": {"x":0,"y":1,"width":12,"height":5}},
  {"id": 6, "definition": {"type": "timeseries", "title": "Error logs", "requests": [{"display_type": "bars",
      "queries": [{"data_source": "logs", "name": "l", "compute": {"aggregation": "count"}, "search": {"query": "status:error $env"}, "indexes": ["*"]}],
      "formulas": [{"formula": "l"}]}]}},
  {"id": 7, "definition": {"type": "image", "title": "Architecture"}}
 ]}`

func (f *fakeDD) ListDashboards() ([]datadog.DashboardSummary, error) {
	return []datadog.DashboardSummary{
		{ID: "abc-123", Title: "Service health", AuthorHandle: "ana@acme.com", ModifiedAt: "2026-09-25T10:00:00Z"},
		{ID: "def-456", Title: "Billing", AuthorHandle: "leo@acme.com", ModifiedAt: "2026-09-20T10:00:00Z"},
	}, nil
}

func (f *fakeDD) GetDashboard(id string) (map[string]interface{}, error) {
	if id != "abc-123" {
		return map[string]interface{}{"id": id, "title": "Billing", "widgets": []any{}}, nil
	}
	var m map[string]interface{}
	return m, json.Unmarshal([]byte(dashJSON), &m)
}

func (f *fakeDD) DashboardURL(id string) string { return "https://app.example/dashboard/" + id }
func (f *fakeDD) BrowseURL(p string) string     { return "https://app.example" + p }

func queryText(q map[string]any) string {
	if s, ok := q["query"].(string); ok {
		return s
	}
	if s, ok := q["search"].(map[string]any); ok {
		return fmt.Sprint(s["query"])
	}
	return ""
}

func (f *fakeDD) QueryTimeseries(r datadog.FormulaRequest) (*datadog.TimeseriesResult, error) {
	for _, q := range r.Queries {
		f.record(queryText(q))
		f.mu.Lock()
		if f.intervals == nil {
			f.intervals = map[string]int64{}
		}
		f.intervals[queryText(q)] = r.Interval
		f.mu.Unlock()
	}
	res := &datadog.TimeseriesResult{}
	n := 60
	step := (r.To - r.From) / int64(n)
	for i := 0; i < n; i++ {
		res.Times = append(res.Times, r.From+int64(i)*step)
	}
	for s, tag := range []string{"host:a", "host:b"} {
		vals := make([]*float64, n)
		for i := range vals {
			v := 50 + 30*math.Sin(float64(i+s*7)/6)
			vals[i] = &v
		}
		res.Series = append(res.Series, datadog.TimeseriesSeries{GroupTags: []string{tag}, Values: vals,
			Unit: &datadog.QueryUnit{Family: "percentage", Name: "percent", ShortName: "%"}})
	}
	return res, nil
}

func (f *fakeDD) QueryScalar(r datadog.FormulaRequest) (*datadog.ScalarResult, error) {
	for _, q := range r.Queries {
		f.record(queryText(q))
	}
	v1, v2, v3 := 412.3, 120.0, 7.0
	if strings.Contains(queryText(r.Queries[0]), "latency") {
		return &datadog.ScalarResult{Columns: []datadog.ScalarColumn{{Name: "a", Type: "number", Values: []*float64{&v1}}}}, nil
	}
	return &datadog.ScalarResult{Columns: []datadog.ScalarColumn{
		{Name: "endpoint", Type: "group", Groups: [][]string{{"/checkout"}, {"/login"}, {"/health"}}},
		{Name: "e", Type: "number", Values: []*float64{&v2, &v3, nil}},
	}}, nil
}

func (f *fakeDD) QueryMetrics(q string, from, to int64) (*datadog.MetricsQueryResponse, error) {
	f.record(q)
	var pts [][]float64
	for i := 0; i < 60; i++ {
		t := float64(from*1000 + int64(i)*(to-from)*1000/60)
		pts = append(pts, []float64{t, 40 + 10*math.Cos(float64(i)/5)})
	}
	return &datadog.MetricsQueryResponse{Series: []datadog.MetricsSeries{
		{Scope: "host:a", Pointlist: pts, Unit: []datadog.MetricsUnit{{Family: "percentage", Name: "percent", ShortName: "%"}}},
	}}, nil
}

func (f *fakeDD) SearchMetrics(q string) ([]string, error) {
	var out []string
	for _, m := range []string{"system.cpu.user", "system.cpu.system", "system.mem.used", "trace.http.request.hits"} {
		if strings.Contains(m, q) {
			out = append(out, m)
		}
	}
	return out, nil
}

func prio(n int) *int { return &n }

func (f *fakeDD) monitors() []datadog.Monitor {
	return []datadog.Monitor{
		{ID: 1, Name: "High CPU on api", Type: "query alert", OverallState: "Alert", Priority: prio(1),
			Query: "avg(last_5m):avg:system.cpu.user{service:api} by {host} > 90", Tags: []string{"service:api", "team:platform"},
			Message: "{{#is_alert}}CPU is high on {{host.name}}{{/is_alert}}\n@pagerduty-platform",
			Options: datadog.MonitorOptions{Thresholds: map[string]interface{}{"critical": 90.0, "warning": 80.0}}},
		{ID: 2, Name: "Error logs on web", Type: "log alert", OverallState: "Warn", Priority: prio(2),
			Query: `logs("service:web status:error").index("*").rollup("count").last("5m") > 50`, Tags: []string{"service:web"}},
		{ID: 3, Name: "Queue depth", Type: "query alert", OverallState: "No Data", Query: "avg(last_10m):avg:queue.depth{*} > 100"},
		{ID: 4, Name: "Disk space", Type: "query alert", OverallState: "OK", Query: "avg(last_5m):avg:system.disk.in_use{*} > 0.9"},
	}
}

func (f *fakeDD) ListMonitors(string, int) ([]datadog.Monitor, error) { return f.monitors(), nil }

func (f *fakeDD) GetMonitor(id int64) (*datadog.Monitor, error) {
	for _, m := range f.monitors() {
		if m.ID == id {
			return &m, nil
		}
	}
	return nil, fmt.Errorf("monitor %d not found", id)
}

func (f *fakeDD) SearchMonitorsRich(string, int) (*datadog.MonitorSearchResponse, error) {
	var hits []datadog.MonitorSearchHit
	for _, m := range f.monitors() {
		hits = append(hits, datadog.MonitorSearchHit{ID: m.ID, Name: m.Name, Status: m.OverallState})
	}
	return &datadog.MonitorSearchResponse{Monitors: hits}, nil
}

func (f *fakeDD) GetMonitorGroups(id int64) ([]datadog.MonitorGroup, error) {
	return []datadog.MonitorGroup{{Name: "host:web-1", Status: "Alert", LastTriggeredTS: time.Now().Add(-5 * time.Minute).Unix()},
		{Name: "host:web-2", Status: "OK"}}, nil
}

func (f *fakeDD) ListDowntimes() ([]datadog.DowntimeData, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []datadog.DowntimeData
	for _, d := range f.downtimes {
		out = append(out, d)
	}
	return out, nil
}

func (f *fakeDD) ScheduleDowntime(scope, msg string, start, end time.Time, id *int64) (*datadog.DowntimeData, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.nextDT++
	d := datadog.DowntimeData{ID: fmt.Sprintf("dt-%d", f.nextDT), Attributes: datadog.DowntimeAttributes{
		Status: "active", MonitorIdentifier: &datadog.DowntimeMonitorID{MonitorID: id}}}
	f.downtimes[d.ID] = d
	f.scheduled = append(f.scheduled, *id)
	return &d, nil
}

func (f *fakeDD) CancelDowntime(id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.downtimes, id)
	f.canceled = append(f.canceled, id)
	return nil
}

func (f *fakeDD) logs() []datadog.LogData {
	ts := time.Now().Add(-2 * time.Minute).UTC().Format(time.RFC3339Nano)
	return []datadog.LogData{
		{ID: "l1", Attributes: datadog.LogAttributes{Timestamp: ts, Service: "api", Status: "error", Host: "web-1",
			Message: "Could not verify token", Attributes: map[string]any{"err": map[string]any{"name": "TokenExpiredError",
				"stack": "TokenExpiredError: jwt expired\n    at verify (verify.js:190)\n    at next (router.js:12)"}, "http": map[string]any{"status_code": 401.0}}}},
		{ID: "l2", Attributes: datadog.LogAttributes{Timestamp: ts, Service: "web", Status: "warn", Message: "Slow render 1.2s"}},
		{ID: "l3", Attributes: datadog.LogAttributes{Timestamp: ts, Service: "api", Status: "info", Message: "GET /health 200"}},
	}
}

func (f *fakeDD) SearchLogs(q, from, to string, limit int) (*datadog.LogsResponse, error) {
	return f.SearchLogsCursor(q, from, to, limit, "")
}

func (f *fakeDD) SearchLogsCursor(q, from, to string, limit int, cursor string) (*datadog.LogsResponse, error) {
	f.record("logs:" + q)
	return &datadog.LogsResponse{Data: f.logs()}, nil
}

func (f *fakeDD) AggregateLogs(q, from, to string, groupBy []string, limit int) (*datadog.LogsAggregateResponse, error) {
	return &datadog.LogsAggregateResponse{Data: datadog.LogsAggregateData{Buckets: []datadog.LogsAggregateBucket{
		{By: map[string]string{"service": "api"}, Computes: map[string]interface{}{"c0": 852.0}},
		{By: map[string]string{"service": "web"}, Computes: map[string]interface{}{"c0": 40.0}},
	}}}, nil
}

func (f *fakeDD) ListIncidents() ([]datadog.IncidentData, error) {
	return []datadog.IncidentData{{ID: "i1", Attributes: datadog.IncidentAttributes{Title: "Checkout down", Status: "active"}}}, nil
}

func (f *fakeDD) ListSLOs(string) ([]datadog.SLO, error) {
	return []datadog.SLO{{ID: "s1", Name: "API availability", OverallStatus: []datadog.SLOOverallStatus{{Status: "WARNING"}}}}, nil
}

func (f *fakeDD) ListEvents(start, end int64, p string) ([]datadog.Event, error) {
	now := time.Now().Unix()
	var evs []datadog.Event
	for i, t := range []string{"orders", "leads", "customers"} {
		evs = append(evs, datadog.Event{Title: "gcp_bigquery_table " + t + " was updated", Source: "Google Cloud", DateHappened: now - int64(600+i)})
	}
	evs = append(evs, datadog.Event{Title: "Deployed api v1.4.2", Source: "deployment", AlertType: "success", DateHappened: now - 120})
	return evs, nil
}

var _ API = (*fakeDD)(nil)

// ─── harness ─────────────────────────────────────────────────────

type harness struct {
	t         *testing.T
	app       *Model
	dd        *fakeDD
	w, h      int
	quit      bool
	depth     int
	opened    []string
	clipboard string
}

func newHarness(t *testing.T, width, height int) *harness {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	t.Setenv("NO_COLOR", "1")
	t.Setenv("TERM_PROGRAM", "")
	t.Setenv("DATADOG_CHARTS", "")
	h := &harness{t: t, dd: newFakeDD(), w: width, h: height}
	origTick, origSpin, origOpen, origClip, origLook := tickFn, spinFn, openURL, writeClipboard, lookPath
	// Debounces fire at once; periodic refreshes and the spinner don't run.
	tickFn = func(d time.Duration, fn func(time.Time) tea.Msg) tea.Cmd {
		if d < time.Second {
			return func() tea.Msg { return fn(time.Now()) }
		}
		return nil
	}
	spinFn = func(time.Duration, func(time.Time) tea.Msg) tea.Cmd { return nil }
	openURL = func(u string) tea.Cmd { h.opened = append(h.opened, u); return nil }
	writeClipboard = func(s string) error { h.clipboard = s; return nil }
	lookPath = func(name string) (string, error) { return "/usr/local/bin/" + name, nil }
	t.Cleanup(func() {
		tickFn, spinFn, openURL, writeClipboard, lookPath = origTick, origSpin, origOpen, origClip, origLook
	})
	h.app = New(h.dd, Options{Site: "datadoghq.eu", Profile: "test"})
	h.run(h.app.Init())
	h.dispatch(tea.WindowSizeMsg{Width: width, Height: height})
	return h
}

func (h *harness) run(cmd tea.Cmd) {
	if cmd == nil || h.quit {
		return
	}
	done := make(chan tea.Msg, 1)
	go func() { done <- cmd() }()
	select {
	case msg := <-done:
		h.dispatch(msg)
	case <-time.After(5 * time.Second):
		h.t.Fatalf("a command blocked for 5s (an unexpected timer?)")
	}
}

func (h *harness) dispatch(msg tea.Msg) {
	if msg == nil || h.quit {
		return
	}
	h.depth++
	defer func() { h.depth-- }()
	if h.depth > 300 {
		h.t.Fatalf("message loop too deep (last %T)", msg)
	}
	switch m := msg.(type) {
	case tea.BatchMsg:
		for _, c := range m {
			h.run(c)
		}
		return
	case tea.QuitMsg:
		h.quit = true
		return
	}
	if v := reflect.ValueOf(msg); v.Kind() == reflect.Slice && strings.HasSuffix(v.Type().String(), "sequenceMsg") {
		for i := 0; i < v.Len(); i++ {
			if c, ok := v.Index(i).Interface().(tea.Cmd); ok {
				h.run(c)
			}
		}
		return
	}
	_, cmd := h.app.Update(msg)
	h.run(cmd)
}

// keys types a sequence: named keys in angle brackets, the rest rune by rune.
func (h *harness) keys(seq string) {
	h.t.Helper()
	for len(seq) > 0 {
		if strings.HasPrefix(seq, "<") {
			end := strings.Index(seq, ">")
			h.dispatch(namedKey(seq[1:end]))
			seq = seq[end+1:]
			continue
		}
		r := []rune(seq)[0]
		h.dispatch(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
		seq = seq[len(string(r)):]
	}
}

func namedKey(name string) tea.KeyMsg {
	switch name {
	case "enter":
		return tea.KeyMsg{Type: tea.KeyEnter}
	case "esc":
		return tea.KeyMsg{Type: tea.KeyEscape}
	case "tab":
		return tea.KeyMsg{Type: tea.KeyTab}
	case "down":
		return tea.KeyMsg{Type: tea.KeyDown}
	case "up":
		return tea.KeyMsg{Type: tea.KeyUp}
	case "left":
		return tea.KeyMsg{Type: tea.KeyLeft}
	case "right":
		return tea.KeyMsg{Type: tea.KeyRight}
	case "space":
		return tea.KeyMsg{Type: tea.KeySpace, Runes: []rune{' '}}
	case "backspace":
		return tea.KeyMsg{Type: tea.KeyBackspace}
	}
	panic("unknown key " + name)
}

// screen renders the frame and checks it's exactly w × h cells.
func (h *harness) screen() string {
	h.t.Helper()
	view := h.app.View()
	lines := strings.Split(view, "\n")
	if len(lines) != h.h {
		h.t.Fatalf("frame has %d lines, want %d:\n%s", len(lines), h.h, ansi.Strip(view))
	}
	for i, l := range lines {
		if got := ansi.StringWidth(l); got != h.w {
			h.t.Fatalf("line %d is %d cells wide, want %d: %q\n%s", i, got, h.w, ansi.Strip(l), ansi.Strip(view))
		}
	}
	return ansi.Strip(view)
}

func (h *harness) expect(want ...string) {
	h.t.Helper()
	s := h.screen()
	for _, w := range want {
		if !strings.Contains(s, w) {
			h.t.Fatalf("screen lacks %q:\n%s", w, s)
		}
	}
}

func (h *harness) reject(unwanted ...string) {
	h.t.Helper()
	s := h.screen()
	for _, w := range unwanted {
		if strings.Contains(s, w) {
			h.t.Fatalf("screen unexpectedly shows %q:\n%s", w, s)
		}
	}
}
