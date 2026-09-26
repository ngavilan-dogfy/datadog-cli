package cmd

import (
	"math"
	"strings"
	"testing"
	"time"

	"github.com/ngavilan-dogfy/datadog-cli/datadog"
)

func TestParseDDLink(t *testing.T) {
	for _, c := range []struct {
		url, kind, id, query string
		vars                 map[string]string
	}{
		{"https://app.datadoghq.eu/dashboard/abc-def-ghi/api-health?tpl_var_env%5B0%5D=production&tpl_var_service=api&from_ts=1790400000000&to_ts=1790403600000&live=true",
			"dashboard", "abc-def-ghi", "", map[string]string{"env": "production", "service": "api"}},
		{"https://app.datadoghq.com/monitors/12345?view=spans", "monitor", "12345", "", nil},
		{"https://app.datadoghq.eu/apm/trace/8202096045573558039?spanID=1", "trace", "8202096045573558039", "", nil},
		{"https://app.datadoghq.eu/apm/traces?query=env%3Aprod&traceID=71d3b2e59a0c4f17", "trace", "71d3b2e59a0c4f17", "", nil},
		{"https://app.datadoghq.eu/logs?query=service%3Aapi%20status%3Aerror&from_ts=1&to_ts=2", "logs", "", "service:api status:error", nil},
		{"https://app.datadoghq.eu/apm/traces?query=status%3Aerror", "traces", "", "status:error", nil},
		{"https://app.datadoghq.eu/apm/services/checkout/operations/web.request?env=prod", "apm-service", "checkout", "", nil},
		{"https://app.datadoghq.eu/metric/explorer?exp_metric=system.cpu.user", "metric", "", "system.cpu.user", nil},
		{"https://app.datadoghq.eu/incidents/42", "incident", "42", "", nil},
	} {
		l, ok := parseDDLink(c.url)
		if !ok || l.Kind != c.kind || l.ID != c.id || l.Query != c.query {
			t.Errorf("%s\n got %+v (ok=%v)\nwant kind=%s id=%s query=%q", c.url, l, ok, c.kind, c.id, c.query)
		}
		for k, v := range c.vars {
			if l.Vars[k] != v {
				t.Errorf("%s: var %s = %q, want %q", c.url, k, l.Vars[k], v)
			}
		}
	}
	if _, ok := parseDDLink("https://example.com/dashboard/abc-def-ghi"); ok {
		t.Error("not a Datadog link")
	}
	l, _ := parseDDLink("https://app.datadoghq.eu/dashboard/abc-def-ghi?from_ts=1790400000000&to_ts=1790403600000&live=true")
	if span, end, ok := l.window(); !ok || span != time.Hour || !end.IsZero() {
		t.Errorf("live link: span %v end %v ok %v, want 1h ending now", span, end, ok)
	}
	l, _ = parseDDLink("https://app.datadoghq.eu/dashboard/abc-def-ghi?from_ts=1790400000000&to_ts=1790403600000&live=false")
	if _, end, _ := l.window(); end.UnixMilli() != 1790403600000 {
		t.Errorf("fixed link should keep its end, got %v", end)
	}
}

func TestMonitorWindowQuery(t *testing.T) {
	for _, c := range [][4]string{
		{"avg", "last_5m", "avg:system.cpu.user{*}", "avg:system.cpu.user{*}.rollup(avg, 300)"},
		{"sum", "last_10m", "sum:a.errors{env:prod} by {service}.as_count() / sum:a.hits{env:prod} by {service}.as_count()",
			"sum:a.errors{env:prod} by {service}.as_count().rollup(sum, 600) / sum:a.hits{env:prod} by {service}.as_count().rollup(sum, 600)"},
		{"min", "last_1h", "max:q{*}by{host}", "max:q{*}by{host}.rollup(min, 3600)"},
	} {
		got, ok := monitorWindowQuery(c[0], c[1], c[2])
		if !ok || got != c[3] {
			t.Errorf("monitorWindowQuery(%s, %s, %s)\n got %q\nwant %q", c[0], c[1], c[2], got, c[3])
		}
	}
	if _, ok := monitorWindowQuery("percentile", "last_5m", "p95:trace.x{*}"); ok {
		t.Error("percentiles can't be rolled up like that")
	}
	if _, ok := monitorWindowQuery("avg", "last_5m", "avg:x{*}.rollup(max, 60)"); ok {
		t.Error("a query with its own rollup is left alone")
	}
}

func TestMonitorKindAndServices(t *testing.T) {
	for q, want := range map[string]string{
		"sum(last_10m):sum:trace.web.request.errors{service:api}.as_count() > 5":                         "errors",
		"percentile(last_5m):p95:trace.web.request{service:api} > 1":                                     "latency",
		"avg(last_5m):avg:trace.web.request.duration{service:api} > 1":                                   "latency",
		"sum(last_30m):sum:trace.web.request.hits{service:api}.as_count() < 10":                          "traffic",
		"avg(last_5m):avg:system.cpu.user{*} > 90":                                                       "other",
		"sum(last_5m):sum:gcp.loadbalancing.https.request_count{response_code_class:500}.as_count() > 5": "errors",
	} {
		if got := monitorKind(datadog.Monitor{Query: q, Type: "query alert"}); got != want {
			t.Errorf("monitorKind(%s) = %s, want %s", q, got, want)
		}
	}
	if monitorKind(datadog.Monitor{Type: "log alert", Query: `logs("status:error").index("*").rollup("count").last("5m") > 1`}) != "logs" {
		t.Error("log alerts are logs")
	}
	m := datadog.Monitor{Query: "avg(last_5m):avg:trace.x{env:prod,service:checkout} > 1", Tags: []string{"service:cart"}}
	if s := monitorServices(m); !s["checkout"] || !s["cart"] || len(s) != 2 {
		t.Errorf("monitorServices = %v", s)
	}
	if !rePerService.MatchString("sum(last_5m):sum:trace.x.errors{env:prod} by {service,env}.as_count() > 1") {
		t.Error("by {service,…} alerts per service")
	}
}

func TestCoverageGaps(t *testing.T) {
	s := &coverageService{Service: "checkout", Entry: "web.request", Spans: 1000, ErrorLogs: 12,
		Monitors: map[string][]string{"errors": {"Checkout errors"}}, Silent: []string{"Checkout errors"}}
	gaps := coverageGaps(s, "prod")
	var whats []string
	for _, g := range gaps {
		whats = append(whats, g.What)
	}
	all := strings.Join(whats, "\n")
	for _, want := range []string{"no latency monitor", "no traffic-drop monitor", "12 error logs, and no log monitor", "notify no one", "no SLO", "not in the Service Catalog"} {
		if !strings.Contains(all, want) {
			t.Errorf("gaps lack %q:\n%s", want, all)
		}
	}
	if strings.Contains(all, "error-rate") {
		t.Errorf("it has an error monitor:\n%s", all)
	}
	for _, g := range gaps {
		if strings.HasPrefix(g.What, "no latency") && !strings.Contains(g.Fix, "p95:trace.web.request{env:prod,service:checkout}") {
			t.Errorf("the fix should use the service's own metric: %s", g.Fix)
		}
	}
}

func TestCountsFillZeros(t *testing.T) {
	s := datadog.MetricsSeries{Interval: 60, Start: 1_000, End: 300_000,
		Pointlist: datadog.PointList{{60_000, 2}, {240_000, 1}}}
	pts := seriesPoints(s, true)
	if len(pts) != 5 || pts[0].V != 2 || pts[1].V != 0 || pts[2].V != 0 || pts[3].V != 1 || pts[4].V != 0 {
		t.Errorf("zero-filled counts = %+v", pts)
	}
	if got := seriesPoints(s, false); len(got) != 2 {
		t.Errorf("gauges keep their gaps: %+v", got)
	}
	nan := datadog.MetricsSeries{Pointlist: datadog.PointList{{0, math.NaN()}}}
	if pts := seriesPoints(nan, false); !math.IsNaN(pts[0].V) {
		t.Error("a null stays missing")
	}
}

func TestFmtWindow(t *testing.T) {
	a := time.Date(2026, 5, 19, 10, 0, 0, 0, time.Local)
	if got := fmtWindow(a, a.Add(2*time.Hour)); !strings.HasPrefix(got, "May 19 10:00 → 12:00") {
		t.Errorf("same day: %s", got)
	}
	if got := fmtWindow(a, a.Add(24*time.Hour)); !strings.HasPrefix(got, "May 19 10:00 → May 20 10:00") {
		t.Errorf("across days: %s", got)
	}
}
