package cmd

import (
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/ngavilan-dogfy/datadog-cli/datadog"
	"github.com/ngavilan-dogfy/datadog-cli/internal/series"
)

// The analysis is tested on made-up facts: a made-up service, "checkout",
// over a two-hour window starting at 09:00, compared with the day before.

var invStart = time.Date(2026, 9, 24, 9, 0, 0, 0, time.Local)

// minutes builds a series with a point a minute over the window (or the
// baseline window, a day earlier).
func minutes(base bool, f func(i int) float64) []series.Point {
	start := invStart
	if base {
		start = start.Add(-24 * time.Hour)
	}
	pts := make([]series.Point, 120)
	for i := range pts {
		pts[i] = series.Point{T: start.Add(time.Duration(i) * time.Minute).UnixMilli(), V: f(i)}
	}
	return pts
}

func flat(v float64) func(int) float64 { return func(int) float64 { return v } }

// step is v before minute at and w from it on.
func step(at int, v, w float64) func(int) float64 {
	return func(i int) float64 {
		if i < at {
			return v
		}
		return w
	}
}

// spikes is v, and w at the given minutes.
func spikes(v, w float64, at ...int) func(int) float64 {
	return func(i int) float64 {
		for _, a := range at {
			if i == a {
				return w
			}
		}
		return v
	}
}

func minute(i int) time.Time { return invStart.Add(time.Duration(i) * time.Minute) }

// calmService is checkout on a quiet, normal day: 600 requests a minute,
// 0.2% errors, p95 around 80 ms, two instances, the same as the day before.
func calmService() *invServiceFacts {
	return &invServiceFacts{
		Name: "checkout", Env: "prod", Entry: "web.request",
		Hits:   &invSeries{Query: "sum:trace.web.request.hits{env:prod,service:checkout}.as_count()", Points: minutes(false, flat(600)), Base: minutes(true, flat(600))},
		Errors: &invSeries{Query: "sum:trace.web.request.errors{env:prod,service:checkout}.as_count()", Points: minutes(false, flat(1.2)), Base: minutes(true, flat(1.2))},
		P95:    &invSeries{Query: "p95:trace.web.request{env:prod,service:checkout}", Points: minutes(false, flat(0.08)), Base: minutes(true, flat(0.08))},
		Resources: []invGroup{
			{Name: "POST /orders", Count: 36000, Errors: 70, BaseCount: 36000, BaseErrors: 70, P95: 80 * time.Millisecond, BaseP95: 80 * time.Millisecond},
			{Name: "GET /cart", Count: 36000, Errors: 74, BaseCount: 36000, BaseErrors: 74, P95: 40 * time.Millisecond, BaseP95: 40 * time.Millisecond},
		},
		Operations: []invGroup{
			{Name: "postgres.query → orders-db", Count: 90000, BaseCount: 90000, P95: 5 * time.Millisecond, BaseP95: 5 * time.Millisecond, P95Series: minutes(false, flat(0.005))},
			{Name: "http.request → payments", Count: 30000, BaseCount: 30000, P95: 120 * time.Millisecond, BaseP95: 120 * time.Millisecond, P95Series: minutes(false, flat(0.12))},
			{Name: "redis.command", Count: 60000, BaseCount: 60000, P95: time.Millisecond, BaseP95: time.Millisecond, P95Series: minutes(false, flat(0.001))},
		},
		Hosts:     []invGroup{{Name: "web-1", Count: 36000, BaseCount: 36000}, {Name: "web-2", Count: 36000, BaseCount: 36000}},
		Versions:  []invGroup{{Name: "1.4.2", Count: 72000, BaseCount: 72000}},
		LogQuery:  "service:checkout status:error env:prod",
		ErrorLogs: &patternsReport{Query: "service:checkout status:error env:prod", Total: 140, TotalBefore: 150, Compare: "1d"},
		WatchedBy: []datadog.Monitor{{ID: 11, Name: "checkout error rate", Type: "query alert", Query: "sum(last_10m):sum:trace.web.request.errors{service:checkout}.as_count() / sum:trace.web.request.hits{service:checkout}.as_count() > 0.05"}},
		Catalog:   &datadog.ServiceCatalogEntry{},
	}
}

func investigationOf(s *invServiceFacts, edit func(f *invFacts)) *investigation {
	f := &invFacts{
		Now: invStart.Add(2 * time.Hour), Target: s.Name, From: invStart, To: invStart.Add(2 * time.Hour),
		Compare: 24 * time.Hour, Lookback: 2 * time.Hour, Services: []*invServiceFacts{s},
	}
	if edit != nil {
		edit(f)
	}
	return analyzeInvestigation(f)
}

func hasLead(inv *investigation, prefix string) *invLead {
	for i := range inv.Leads {
		if strings.HasPrefix(inv.Leads[i].Title, prefix) {
			return &inv.Leads[i]
		}
	}
	return nil
}

func hasFinding(inv *investigation, area, contains string) bool {
	for _, fi := range inv.Findings {
		if fi.Area == area && strings.Contains(fi.Text, contains) {
			return true
		}
	}
	return false
}

func dump(inv *investigation) string { return inv.text(false) }

func TestInvestigateNormalDay(t *testing.T) {
	inv := investigationOf(calmService(), nil)
	if inv.Status != "normal" || len(inv.Leads) != 0 {
		t.Fatalf("a calm day: status %s, %d leads\n%s", inv.Status, len(inv.Leads), dump(inv))
	}
	if !strings.HasPrefix(inv.Summary, "checkout looks normal") || !strings.Contains(inv.Summary, "No deploys or changes") {
		t.Errorf("summary = %q", inv.Summary)
	}
	for _, want := range []string{"Traffic 600/min", "Error rate 0.2%", "p95 latency typically 80 ms", "Same instances", "Same versions", "What checkout calls"} {
		found := false
		for _, n := range inv.Normal {
			found = found || strings.Contains(n.Text, want)
		}
		if !found {
			t.Errorf("checked-normal lacks %q:\n%s", want, dump(inv))
		}
	}
}

// A deploy at 09:25, then errors from 09:30, a new error in the logs, and
// a monitor that alerted at 09:50: the deploy is the lead, the monitor was
// late, and the errors come from one endpoint.
func TestInvestigateDeployThenErrors(t *testing.T) {
	s := calmService()
	s.Errors.Points = minutes(false, step(30, 1.2, 60)) // 0.2% → 10% of requests
	s.Resources[0].Errors, s.Resources[0].BaseErrors = 5400, 70
	s.Hosts = append(s.Hosts, invGroup{Name: "web-3", Count: 20000, FirstSeen: minute(25)})
	s.Hosts[0].Count = 0 // web-1 went away
	s.ErrorLogs = &patternsReport{Query: s.LogQuery, Total: 5000, TotalBefore: 150, Compare: "1d",
		volume:   minutes(false, step(30, 1, 60)),
		Patterns: []logPattern{{Pattern: "payment authorization timed out after <num>ms", Estimate: 4500, Share: 90, New: true, First: minute(30), Last: minute(119)}}}
	inv := investigationOf(s, func(f *invFacts) {
		f.Alerts = []datadog.EventV2{{ID: "a1", Timestamp: minute(50), Tags: []string{"service:checkout"},
			Monitor: &datadog.EventMonitor{ID: 11, Name: "checkout error rate", FromState: "OK", ToState: "Alert"}}}
	})

	if inv.Status != "degraded" || inv.Onset == nil || !inv.Onset.Equal(minute(30)) {
		t.Fatalf("status %s, onset %v: want degraded from 09:30\n%s", inv.Status, inv.Onset, dump(inv))
	}
	lead := inv.Leads[0]
	if !strings.HasPrefix(lead.Title, "A change 5 min before it started") || lead.Confidence != "high" {
		t.Errorf("the top lead should be the deploy 5 min before, got %q (%s)\n%s", lead.Title, lead.Confidence, dump(inv))
	}
	if hasLead(inv, "It's one endpoint: POST /orders") == nil {
		t.Errorf("the errors are all POST /orders:\n%s", dump(inv))
	}
	if hasLead(inv, "The failure says") == nil {
		t.Errorf("the new log pattern at the onset is a lead:\n%s", dump(inv))
	}
	if !hasFinding(inv, "errors", "it rose at 09:30") || !hasFinding(inv, "endpoints", "POST /orders") {
		t.Errorf("findings:\n%s", dump(inv))
	}
	found := false
	for _, sg := range inv.Suggestions {
		found = found || strings.Contains(sg.What, "Catch it sooner") && strings.Contains(sg.What, "20 min")
	}
	if !found {
		t.Errorf("the monitor alerted 20 min late: suggest catching it sooner\n%s", dump(inv))
	}
	if !strings.Contains(inv.Summary, `"checkout error rate" alerted 20 min after it started`) {
		t.Errorf("summary = %q", inv.Summary)
	}
	for i := 1; i < len(inv.Timeline); i++ {
		if inv.Timeline[i].At.Before(inv.Timeline[i-1].At) {
			t.Fatalf("the timeline isn't in order")
		}
	}
}

// Latency spikes that one dependency shares: it is the lead. When several
// unrelated calls share them, the process or its host is.
func TestInvestigateLatencySpikes(t *testing.T) {
	s := calmService()
	s.P95.Points = minutes(false, spikes(0.08, 4, 40, 70))
	s.Operations[0].P95Series = minutes(false, spikes(0.005, 3.5, 40, 70)) // the database
	inv := investigationOf(s, nil)
	if !inv.Onset.Equal(minute(40)) || inv.Status != "recovered" {
		t.Errorf("onset %v, status %s: want the first spike, recovered\n%s", inv.Onset, inv.Status, dump(inv))
	}
	l := hasLead(inv, "The slowness comes from postgres.query → orders-db")
	if l == nil || l.Confidence != "high" || inv.Leads[0].Title != l.Title {
		t.Fatalf("the database spiked with the service: it should be the top lead\n%s", dump(inv))
	}

	for i := range s.Operations {
		s.Operations[i].P95Series = minutes(false, spikes(s.Operations[i].P95.Seconds(), 3, 40, 70))
	}
	inv = investigationOf(s, nil)
	if !strings.HasPrefix(inv.Leads[0].Title, "Everything slowed down at once") {
		t.Errorf("three unrelated calls spiking together point at the process:\n%s", dump(inv))
	}
}

func TestInvestigateTrafficDrop(t *testing.T) {
	s := calmService()
	s.Hits.Points = minutes(false, step(60, 600, 100))
	s.Errors.Points = minutes(false, step(60, 1.2, 0.2))
	inv := investigationOf(s, nil)
	if !hasFinding(inv, "traffic", "Traffic dropped at 10:00") || !hasFinding(inv, "traffic", "over the window") {
		t.Errorf("findings:\n%s", dump(inv))
	}
	if inv.Status != "degraded" {
		t.Errorf("status %s", inv.Status)
	}
}

// A quiet service: one slow request is the whole p95 of its minute. That
// isn't an incident.
func TestInvestigateIgnoresQuietNoise(t *testing.T) {
	s := calmService()
	s.Hits.Points = minutes(false, flat(3))
	s.Hits.Base = minutes(true, flat(3))
	s.Errors.Points = minutes(false, spikes(0, 1, 50))
	s.Errors.Base = minutes(true, flat(0))
	s.P95.Points = minutes(false, spikes(0.08, 2.5, 50))
	inv := investigationOf(s, nil)
	if inv.Status != "normal" {
		t.Errorf("3 requests a minute: one slow or failed one is noise, got %s\n%s", inv.Status, dump(inv))
	}
}

var reCitation = regexp.MustCompile(`\[\[(\d+)\]\]\(([^)]+)\)`)

// Every citation in the markdown points at a listed reference, and every
// reference reads back with 'datadog read'.
func TestInvestigateCitesItsReferences(t *testing.T) {
	withSite(t)
	s := calmService()
	s.Errors.Points = minutes(false, step(30, 1.2, 60))
	s.Resources[0].Errors = 5400
	inv := investigationOf(s, nil)
	md := inv.markdown()
	cites := reCitation.FindAllStringSubmatch(md, -1)
	if len(cites) == 0 {
		t.Fatalf("no citations:\n%s", md)
	}
	for _, c := range cites {
		n, _ := strconv.Atoi(c[1])
		if n < 1 || n > len(inv.References) || inv.References[n-1].URL != c[2] {
			t.Errorf("citation [%s] → %s isn't reference %s", c[1], c[2], c[1])
		}
	}
	for _, r := range inv.References {
		if _, ok := parseDDLink(r.URL); !ok {
			t.Errorf("reference %d doesn't read back: %s", r.N, r.URL)
		}
	}
	for _, section := range []string{"## Summary", "## Timeline", "## Leads", "## Findings", "## Checked, nothing unusual", "## Next steps", "## References"} {
		if !strings.Contains(md, section) {
			t.Errorf("the report lacks %q", section)
		}
	}
}

func TestChangeRelevance(t *testing.T) {
	deploy := datadog.EventV2{Changed: "checkout-00321-abc", ResourceType: "gcp_run_revision", ResourceKey: "//run.googleapis.com/projects/shop-prod/revisions/checkout-00321-abc"}
	if !changeAbout(deploy, "checkout") {
		t.Error("a revision of checkout is about checkout")
	}
	if changeAbout(datadog.EventV2{Changed: "checkout-analytics-table", ResourceType: "gcp_bigquery_table"}, "cart") {
		t.Error("not about cart")
	}
	if !relevantElsewhere(deploy, "production") {
		t.Error("a Cloud Run revision in production is relevant elsewhere")
	}
	if relevantElsewhere(datadog.EventV2{Changed: "orders", ResourceType: "gcp_bigquery_table"}, "production") {
		t.Error("a data table isn't")
	}
	staging := datadog.EventV2{Changed: "web-00012", ResourceType: "gcp_run_revision", ResourceKey: "//run.googleapis.com/projects/shop-stg/revisions/web-00012"}
	if relevantElsewhere(staging, "production") {
		t.Error("a staging deploy doesn't matter to production")
	}
}

func TestNormalizeTagAndPrettyResource(t *testing.T) {
	for in, want := range map[string]string{
		"GET /v1/orders/:orderId": "get_/v1/orders/:orderid",
		"POST  /checkout?x":       "post_/checkout_x",
	} {
		if got := normalizeTag(in); got != want {
			t.Errorf("normalizeTag(%q) = %q, want %q", in, got, want)
		}
	}
	for in, want := range map[string]string{"get_/v1/orders": "GET /v1/orders", "get": "GET", "consume_orders": "consume_orders"} {
		if got := prettyResource(in); got != want {
			t.Errorf("prettyResource(%q) = %q, want %q", in, got, want)
		}
	}
	if got := resourceHint("GET /v1/config/geocontext/:ip?"); got != " resource_name:*geocontext*" {
		t.Errorf("resourceHint = %q", got)
	}
}
