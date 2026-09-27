package cmd

import (
	"strings"
	"testing"
	"time"

	"github.com/ngavilan-dogfy/datadog-cli/datadog"
)

// withSite points the package's client at a made-up site for link tests.
func withSite(t *testing.T) {
	t.Helper()
	old := client
	client = datadog.NewClient("https://api.datadoghq.eu", "https://app.datadoghq.eu", "", "")
	t.Cleanup(func() { client = old })
}

// Every link a report cites must read back: same kind, same query, same
// window — so 'datadog read <reference>' shows what the report saw.
func TestReferencesReadBack(t *testing.T) {
	withSite(t)
	from := time.Date(2026, 9, 26, 9, 0, 0, 0, time.UTC)
	to := from.Add(2 * time.Hour)
	for _, c := range []struct {
		link, kind, id, query string
		window                bool
	}{
		{logsLink("service:api status:error", from, to), "logs", "", "service:api status:error", true},
		{spansLink(`service:api resource_name:"POST /orders" status:error`, from, to), "traces", "", `service:api resource_name:"POST /orders" status:error`, true},
		{serviceLink("api", "production", "fastify.request", from, to), "apm-service", "api", "", true},
		{metricLink("sum:trace.fastify.request.errors{env:production,service:api} by {resource_name}.as_count()", from, to), "metric", "",
			"sum:trace.fastify.request.errors{env:production,service:api} by {resource_name}.as_count()", true},
		{monitorLink(12345, from, to), "monitor", "12345", "", true},
		{traceLink("8202096045573558039"), "trace", "8202096045573558039", "", false},
		{eventsLink("source:alert service:api", from, to), "events", "", "source:alert service:api", true},
		{auditLink("-@asset.type:datadog_agent_configuration", from, to), "audit", "", "-@asset.type:datadog_agent_configuration", true},
		{hostLink("aef-default-1-8hw2"), "host", "aef-default-1-8hw2", "", false},
		{dashboardLink("abc-def-ghi", from, to), "dashboard", "abc-def-ghi", "", true},
		{incidentLink("42"), "incident", "42", "", false},
		{sloLink("0123abcd"), "slo", "0123abcd", "", false},
	} {
		if !strings.HasPrefix(c.link, "https://app.datadoghq.eu/") {
			t.Errorf("%s: not a link to the site: %q", c.kind, c.link)
			continue
		}
		l, ok := parseDDLink(c.link)
		if !ok || l.Kind != c.kind || l.ID != c.id || l.Query != c.query {
			t.Errorf("%s\n got kind=%s id=%s query=%q ok=%v\nwant kind=%s id=%s query=%q", c.link, l.Kind, l.ID, l.Query, ok, c.kind, c.id, c.query)
		}
		if c.window && (!l.From.Equal(from) || !l.To.Equal(to) || l.Live) {
			t.Errorf("%s: window %v → %v live=%v, want %v → %v fixed", c.kind, l.From, l.To, l.Live, from, to)
		}
	}
}

func TestMetricLinkWithoutExactQuery(t *testing.T) {
	l, ok := parseDDLink("https://app.datadoghq.eu/metric/explorer?exp_metric=system.cpu.user&exp_agg=max&exp_scope=env%3Aprod&exp_group=host")
	if !ok || l.Query != "max:system.cpu.user{env:prod} by {host}" {
		t.Errorf("got %q", l.Query)
	}
}

func TestRefList(t *testing.T) {
	var r refList
	a := r.add("errors", "https://x/1", "")
	b := r.add("latency", "https://x/2", "")
	if again := r.add("errors, again", "https://x/1", ""); again != a || a != 1 || b != 2 {
		t.Errorf("numbers: %d %d %d", a, b, again)
	}
	if r.add("nothing", "", "") != 0 {
		t.Error("a missing link isn't a reference")
	}
	if got := cite(2, 0, 1); got != " [2][1]" {
		t.Errorf("cite = %q", got)
	}
	if got := citeMD(r.list, 1); got != " [[1]](https://x/1)" {
		t.Errorf("citeMD = %q", got)
	}
}
