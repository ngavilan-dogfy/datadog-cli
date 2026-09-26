package demo

import (
	"math"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/ngavilan-dogfy/datadog-cli/datadog"
)

// The demo only convinces if its parts agree: a monitor that says Alert
// must be over its threshold in the data it charts, for as long as a demo
// session lasts.
func TestMonitorStatesMatchTheirData(t *testing.T) {
	// Traffic follows the clock, so try sessions at every time of day, on a
	// weekday and on a Saturday.
	day := time.Date(2026, 9, 23, 0, 0, 0, 0, time.Local) // a Wednesday
	var starts []time.Time
	for h := 0; h < 24; h += 3 {
		starts = append(starts, day.Add(time.Duration(h)*time.Hour+17*time.Minute))
	}
	starts = append(starts, day.AddDate(0, 0, 3).Add(15*time.Hour))
	for _, start := range starts {
		a := New()
		a.start = start
		checkStates(t, a)
	}
}

func checkStates(t *testing.T, a *API) {
	t.Helper()
	checked := 0
	defer func() {
		if checked < 30 {
			t.Fatalf("only %d monitor evaluations ran", checked)
		}
	}()
	for _, later := range []time.Duration{0, 30 * time.Minute, 55 * time.Minute} {
		at := a.start.Add(later)
		for _, m := range a.monitors() {
			if m.OverallState == "No Data" || m.Options.Thresholds["critical"] == nil {
				continue
			}
			value, ok := monitorValue(t, a, m.Query, at)
			if !ok {
				continue
			}
			checked++
			below := strings.Contains(m.Query, " < ")
			beyond := func(th any) bool {
				if below {
					return value < toFloat(th)
				}
				return value > toFloat(th)
			}
			want := "OK"
			if w := m.Options.Thresholds["warning"]; w != nil && beyond(w) {
				want = "Warn"
			}
			if beyond(m.Options.Thresholds["critical"]) {
				want = "Alert"
			}
			if want != m.OverallState {
				t.Errorf("%s +%v: %s is %s but its data (%.4g vs %v) says %s", a.start.Format("Mon 15:04"), later, m.Name, m.OverallState, value, m.Options.Thresholds["critical"], want)
			}
		}
	}
}

var (
	reWindow = regexp.MustCompile(`^(\w+)\(last_(\d+)m\):(.*?)\s*[<>]=?\s*[\d.]+$`)
	reLogMon = regexp.MustCompile(`^logs\("([^"]*)"\).*\.last\("(\d+)m"\)`)
)

// monitorValue evaluates a monitor's query at a moment, roughly the way
// Datadog does: the time aggregation over the window, the worst group.
func monitorValue(t *testing.T, a *API, query string, at time.Time) (float64, bool) {
	t.Helper()
	if m := reLogMon.FindStringSubmatch(query); m != nil {
		mins, _ := strconv.Atoi(m[2])
		res, err := a.AggregateLogs(m[1], strconv.FormatInt(at.Add(-time.Duration(mins)*time.Minute).UnixMilli(), 10), strconv.FormatInt(at.UnixMilli(), 10), nil, 1)
		if err != nil || len(res.Data.Buckets) == 0 {
			t.Fatalf("%s: %v", query, err)
		}
		return res.Data.Buckets[0].Computes["c0"].(float64), true
	}
	m := reWindow.FindStringSubmatch(query)
	if m == nil || strings.Contains(query, "anomalies(") {
		return 0, false
	}
	mins, _ := strconv.Atoi(m[2])
	res, err := a.QueryMetrics(m[3], at.Add(-time.Duration(mins)*time.Minute).Unix(), at.Unix())
	if err != nil || len(res.Series) == 0 {
		t.Fatalf("%s: no data (%v)", query, err)
	}
	aggr := m[1]
	if strings.Contains(m[3], "/") {
		aggr = "avg" // a ratio of sums is about the average ratio
	}
	below := strings.Contains(query, " < ")
	worst := math.Inf(-1)
	if below {
		worst = math.Inf(1)
	}
	for _, s := range res.Series {
		var vals []float64
		for _, p := range s.Pointlist {
			vals = append(vals, p[1])
		}
		v := reduce(vals, aggr)
		if below {
			worst = math.Min(worst, v)
		} else {
			worst = math.Max(worst, v)
		}
	}
	return worst, true
}

func toFloat(v any) float64 {
	switch x := v.(type) {
	case float64:
		return x
	case int:
		return float64(x)
	}
	return math.NaN()
}

func TestFormulasAndGroups(t *testing.T) {
	a := New()
	to := time.Now().UnixMilli()
	from := to - time.Hour.Milliseconds()
	res, err := a.QueryTimeseries(datadog.FormulaRequest{From: from, To: to, Interval: 60000,
		Queries: []map[string]any{
			{"data_source": "metrics", "name": "e", "query": "sum:trace.http.request.errors{env:prod,service:checkout}.as_count()"},
			{"data_source": "metrics", "name": "h", "query": "sum:trace.http.request.hits{env:prod,service:checkout}.as_count()"},
		},
		Formulas: []map[string]any{{"formula": "100 * e / h"}}})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Series) != 1 || len(res.Times) < 55 || res.Series[0].Unit == nil || res.Series[0].Unit.Family != "percentage" {
		t.Fatalf("unexpected result: %d series, %d points", len(res.Series), len(res.Times))
	}
	last := *res.Series[0].Values[len(res.Series[0].Values)-1]
	if last < 2 || last > 20 {
		t.Errorf("checkout error rate during the incident = %.2f%%, want a few percent", last)
	}

	sc, err := a.QueryScalar(datadog.FormulaRequest{From: from, To: to,
		Queries:  []map[string]any{{"data_source": "logs", "name": "l", "search": map[string]any{"query": "status:error"}, "group_by": []any{map[string]any{"facet": "service"}}}},
		Formulas: []map[string]any{{"formula": "l"}}})
	if err != nil {
		t.Fatal(err)
	}
	if len(sc.Columns) != 2 || sc.Columns[0].Groups[0][0] != "checkout" {
		t.Fatalf("error logs by service should put checkout first: %+v", sc.Columns)
	}
}

func TestLogsSearchAndPages(t *testing.T) {
	a := New()
	page, err := a.SearchLogsCursor("service:checkout status:error", "now-1h", "now", 20, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Data) != 20 || page.Meta.Page.After == "" {
		t.Fatalf("want a full page and a cursor, got %d and %q", len(page.Data), page.Meta.Page.After)
	}
	for _, l := range page.Data {
		if l.Attributes.Service != "checkout" || l.Attributes.Status != "error" {
			t.Fatalf("filter leaked %s/%s", l.Attributes.Service, l.Attributes.Status)
		}
	}
	next, _ := a.SearchLogsCursor("service:checkout status:error", "now-1h", "now", 20, page.Meta.Page.After)
	if len(next.Data) == 0 || next.Data[0].Attributes.Timestamp > page.Data[len(page.Data)-1].Attributes.Timestamp {
		t.Fatal("the next page should continue where the first ended")
	}
	again, _ := a.SearchLogsCursor("service:checkout status:error", "now-1h", "now", 20, "")
	if again.Data[5].ID != page.Data[5].ID {
		t.Error("the same search should return the same lines")
	}
	words, _ := a.SearchLogs("timed out", "now-1h", "now", 10)
	for _, l := range words.Data {
		if !strings.Contains(strings.ToLower(l.Attributes.Message), "timed out") {
			t.Fatalf("free text didn't filter: %q", l.Attributes.Message)
		}
	}
}

func TestMuteAndUnmute(t *testing.T) {
	a := New()
	id := int64(4101)
	d, err := a.ScheduleDowntime("*", "test", time.Now(), time.Now().Add(time.Hour), &id)
	if err != nil {
		t.Fatal(err)
	}
	dts, _ := a.ListDowntimes()
	if len(dts) != 2 {
		t.Fatalf("want the seeded downtime and the new one, got %d", len(dts))
	}
	if err := a.CancelDowntime(d.ID); err != nil {
		t.Fatal(err)
	}
	dts, _ = a.ListDowntimes()
	if len(dts) != 1 {
		t.Fatalf("cancel didn't remove it: %d left", len(dts))
	}
}
