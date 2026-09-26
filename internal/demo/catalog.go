package demo

import (
	"time"

	"github.com/ngavilan-dogfy/datadog-cli/datadog"
)

// The org's inventory: monitors, events, incidents, SLOs and metric names.
// Times are relative to when the demo started.

func prio(n int) *int { return &n }

func (a *API) monitors() []datadog.Monitor {
	type spec struct {
		id                 int64
		name, typ, state   string
		prio               int
		query, message     string
		tags               []string
		critical, warning  any
		multi              bool
		createdDaysAgo     int
		modifiedMinutesAgo int
	}
	specs := []spec{
		{4101, "Checkout p95 latency is above 800 ms", "query alert", "Alert", 1,
			"avg(last_5m):p95:trace.http.request.duration{service:checkout,env:prod} > 0.8",
			"{{#is_alert}}Checkout p95 is {{value}} s (threshold {{threshold}} s): customers are waiting at the payment step.{{/is_alert}}\n{{#is_warning}}Checkout p95 is creeping up: {{value}} s.{{/is_warning}}\n\nRunbook: https://wiki.example.com/checkout/latency\n@slack-checkout-alerts @pagerduty-checkout",
			[]string{"service:checkout", "team:checkout", "env:prod", "tier:1"}, 0.8, 0.5, false, 210, 6 * 24 * 60},
		{4102, "Payments provider error rate above 5%", "query alert", "Alert", 1,
			"sum(last_10m):sum:payments.provider.errors{env:prod}.as_count() / sum:payments.provider.requests{env:prod}.as_count() * 100 > 5",
			"{{#is_alert}}{{value}}% of authorizations fail at the provider. Check the provider's status page; `payments.fallback` switches to the backup acquirer.{{/is_alert}}\n\n@slack-payments @pagerduty-payments",
			[]string{"service:payments", "team:payments", "env:prod", "tier:1"}, 5, 2, false, 340, 20 * 24 * 60},
		{4103, "[checkout] Error logs above 200 in 5 min", "log alert", "Warn", 2,
			`logs("service:checkout status:error").index("*").rollup("count").last("5m") > 200`,
			"{{#is_warning}}{{value}} error logs in 5 minutes on checkout.{{/is_warning}}\n{{#is_alert}}Checkout is logging {{value}} errors in 5 minutes.{{/is_alert}}\n\n@slack-checkout-alerts",
			[]string{"service:checkout", "team:checkout", "env:prod"}, 200, 100, false, 120, 3 * 24 * 60},
		{4104, "Checkout apdex below 0.8", "query alert", "Warn", 2,
			"avg(last_10m):avg:trace.http.request.apdex{service:checkout,env:prod} < 0.8",
			"Apdex is {{value}}. Slow requests hurt conversion before they show up as errors.\n@slack-checkout-alerts",
			[]string{"service:checkout", "team:checkout", "env:prod"}, 0.8, 0.9, false, 95, 45 * 24 * 60},
		{4105, "Cart pods restarting", "query alert", "Warn", 3,
			"max(last_10m):sum:kubernetes_state.container.restarts{kube_deployment:cart,env:prod} > 5",
			"{{#is_warning}}Cart containers restarted {{value}} times. Look for OOMKilled in the pod events.{{/is_warning}}\n@slack-platform",
			[]string{"service:cart", "team:checkout", "env:prod", "kube_deployment:cart"}, 5, 3, false, 60, 9 * 24 * 60},
		{4106, "Inventory sync queue backlog", "query alert", "No Data", 3,
			"avg(last_15m):avg:inventory.sync.queue.depth{env:prod} > 5000",
			"The inventory sync queue has {{value}} messages waiting. Stock levels on the site may be stale.\n@slack-inventory",
			[]string{"service:inventory", "team:platform", "env:prod"}, 5000, 2000, false, 400, 2 * 24 * 60},
		{4107, "Search p99 latency above 1.5 s", "query alert", "OK", 2,
			"avg(last_5m):p99:trace.http.request.duration{service:search,env:prod} > 1.5",
			"Search p99 is {{value}} s.\n@slack-discovery",
			[]string{"service:search", "team:discovery", "env:prod"}, 1.5, 1, false, 300, 30 * 24 * 60},
		{4108, "Disk usage above 90% on database hosts", "query alert", "OK", 2,
			"avg(last_5m):avg:system.disk.in_use{role:db} by {host} > 0.9",
			"{{host.name}} is {{value}} full.\n@slack-platform @pagerduty-platform",
			[]string{"team:platform", "role:db", "env:prod"}, 0.9, 0.8, true, 500, 90 * 24 * 60},
		{4109, "Auth: failed logins spike", "log alert", "OK", 2,
			`logs("service:auth @evt.outcome:failure").index("*").rollup("count").last("10m") > 500`,
			"{{value}} failed logins in 10 minutes: a credential-stuffing attempt looks like this.\n@slack-security",
			[]string{"service:auth", "team:identity", "env:prod", "security"}, 500, 300, false, 150, 12 * 24 * 60},
		{4110, "Catalog 5xx above 1%", "query alert", "OK", 3,
			"sum(last_5m):sum:trace.http.request.errors{service:catalog,env:prod}.as_count() / sum:trace.http.request.hits{service:catalog,env:prod}.as_count() * 100 > 1",
			"Catalog answers {{value}}% of requests with a 5xx.\n@slack-discovery",
			[]string{"service:catalog", "team:discovery", "env:prod"}, 1, 0.5, false, 280, 50 * 24 * 60},
		{4111, "Web store LCP above 2.5 s for many views", "rum alert", "OK", 3,
			`rum("@type:view @view.largest_contentful_paint:>2500000000").rollup("count").last("15m") > 300`,
			"{{value}} page views took more than 2.5 s to show their main content.\n@slack-web",
			[]string{"service:web-store", "team:web", "env:prod"}, 300, 150, false, 90, 15 * 24 * 60},
		{4112, "Postgres connections above 180", "query alert", "OK", 2,
			"avg(last_5m):avg:postgresql.connections{role:db} by {host} > 180",
			"{{host.name}} has {{value}} connections; the pool limit is 200.\n@slack-platform",
			[]string{"team:platform", "role:db", "env:prod"}, 180, 150, true, 610, 70 * 24 * 60},
		{4113, "Redis memory above 6 GB", "query alert", "OK", 3,
			"avg(last_10m):avg:redis.mem.used{env:prod} > 6000000000",
			"Redis uses {{value}} bytes.\n@slack-platform",
			[]string{"team:platform", "env:prod"}, 6e9, 5e9, false, 610, 70 * 24 * 60},
		{4114, "Load balancer 5xx", "query alert", "OK", 2,
			"sum(last_5m):sum:aws.elb.httpcode_elb_5xx{env:prod}.as_count() > 2000",
			"The load balancer itself returned {{value}} 5xx in 5 minutes.\n@slack-platform",
			[]string{"team:platform", "env:prod"}, 2000, 1000, false, 700, 200 * 24 * 60},
		{4115, "Checkout availability SLO: fast burn", "slo alert", "OK", 1,
			`burn_rate("slo-chk-avail").over("30d").long_window("1h").short_window("5m") > 14.4`,
			"The checkout availability SLO is burning its error budget 14× too fast.\n@pagerduty-checkout",
			[]string{"service:checkout", "team:checkout", "slo"}, 14.4, nil, false, 200, 40 * 24 * 60},
		{4116, "Orders per minute look unusual", "query alert", "OK", 2,
			"avg(last_4h):anomalies(sum:checkout.orders.placed{env:prod}.as_rate(), 'agile', 3) >= 1",
			"Orders per minute are outside their usual range for this time of day.\n@slack-checkout-alerts",
			[]string{"service:checkout", "team:checkout", "env:prod", "business"}, 1, nil, false, 180, 25 * 24 * 60},
		{4117, "Kubernetes nodes not ready", "service check", "OK", 1,
			`"kubernetes_state.node.ready".over("*").by("node").last(2).count_by_status()`,
			"A node is not ready: pods on it will be rescheduled.\n@slack-platform",
			[]string{"team:platform", "env:prod"}, nil, nil, true, 720, 300 * 24 * 60},
		{4118, "Search index lag above 60 s", "query alert", "OK", 3,
			"avg(last_5m):avg:search.index.lag{env:prod} > 60",
			"New products take {{value}} s to become searchable.\n@slack-discovery",
			[]string{"service:search", "team:discovery", "env:prod"}, 60, 30, false, 260, 33 * 24 * 60},
	}
	out := make([]datadog.Monitor, 0, len(specs))
	for _, s := range specs {
		th := map[string]interface{}{}
		if s.critical != nil {
			th["critical"] = s.critical
		}
		if s.warning != nil {
			th["warning"] = s.warning
		}
		out = append(out, datadog.Monitor{
			ID: s.id, Name: s.name, Type: s.typ, OverallState: s.state, Priority: prio(s.prio),
			Query: s.query, Message: s.message, Tags: s.tags, Multi: s.multi,
			Created:  a.start.AddDate(0, 0, -s.createdDaysAgo).UTC().Format(time.RFC3339),
			Modified: a.start.Add(-time.Duration(s.modifiedMinutesAgo) * time.Minute).UTC().Format(time.RFC3339),
			Creator:  datadog.Creator{Name: "Platform team", Handle: "platform@acme.example"},
			Options:  datadog.MonitorOptions{Thresholds: th, NotifyNoData: s.state == "No Data"},
		})
	}
	return out
}

// lastTriggered says when a monitor last left OK (0 = not recently).
func (a *API) lastTriggered(m datadog.Monitor) int64 {
	switch m.ID {
	case 4101:
		return a.start.Add(-21 * time.Minute).Unix()
	case 4102:
		return a.start.Add(-24 * time.Minute).Unix()
	case 4103:
		return a.start.Add(-19 * time.Minute).Unix()
	case 4104:
		return a.start.Add(-9 * time.Minute).Unix()
	case 4105:
		return a.start.Add(-47 * time.Minute).Unix()
	case 4106:
		return a.start.Add(-2 * time.Hour).Unix()
	}
	return 0
}

func (a *API) events() []datadog.Event {
	ago := func(d time.Duration) int64 { return a.start.Add(-d).Unix() }
	evs := []datadog.Event{
		{ID: 1, Title: "[Warn] Checkout apdex below 0.8", AlertType: "warning", Source: "Monitor Alert", DateHappened: ago(9 * time.Minute), Tags: []string{"service:checkout"}},
		{ID: 2, Title: "payments.timeout changed from 2s to 5s", AlertType: "warning", Source: "Configuration", DateHappened: ago(14 * time.Minute), Tags: []string{"service:payments"}},
		{ID: 3, Title: "Incident #214 declared: Checkout latency degraded", AlertType: "error", Source: "Incidents", DateHappened: ago(20 * time.Minute)},
		{ID: 4, Title: "[Triggered] Checkout p95 latency is above 800 ms", AlertType: "error", Source: "Monitor Alert", DateHappened: ago(21 * time.Minute), Tags: []string{"service:checkout"}},
		{ID: 5, Title: "Autoscaled checkout from 6 to 9 pods", AlertType: "info", Source: "Kubernetes", DateHappened: ago(22 * time.Minute), Tags: []string{"kube_deployment:checkout"}},
		{ID: 6, Title: "[Triggered] Payments provider error rate above 5%", AlertType: "error", Source: "Monitor Alert", DateHappened: ago(24 * time.Minute), Tags: []string{"service:payments"}},
		{ID: 7, Title: "Deployed checkout v2.31.0", AlertType: "success", Source: "Deploys", DateHappened: ago(41 * time.Minute), Tags: []string{"service:checkout", "version:v2.31.0"}},
		{ID: 8, Title: "Container cart-7d9f4-x2k9p restarted (OOMKilled)", AlertType: "warning", Source: "Kubernetes", DateHappened: ago(47 * time.Minute)},
		{ID: 9, Title: "Container cart-7d9f4-m4q8z restarted (OOMKilled)", AlertType: "warning", Source: "Kubernetes", DateHappened: ago(63 * time.Minute)},
		{ID: 10, Title: "Container cart-7d9f4-h7t2c restarted (OOMKilled)", AlertType: "warning", Source: "Kubernetes", DateHappened: ago(78 * time.Minute)},
		{ID: 11, Title: "Feature flag new-3ds-flow enabled for 50% of checkouts", AlertType: "info", Source: "Feature flags", DateHappened: ago(52 * time.Minute)},
		{ID: 12, Title: "Deployed search v1.18.2", AlertType: "success", Source: "Deploys", DateHappened: ago(95 * time.Minute), Tags: []string{"service:search"}},
		{ID: 13, Title: "Scaled search from 6 to 4 pods", AlertType: "info", Source: "Kubernetes", DateHappened: ago(130 * time.Minute)},
		{ID: 14, Title: "Nightly catalog import finished: 48,210 products", AlertType: "success", Source: "Jobs", DateHappened: ago(185 * time.Minute)},
		{ID: 15, Title: "Deployed web-store v5.4.0", AlertType: "success", Source: "Deploys", DateHappened: ago(260 * time.Minute), Tags: []string{"service:web-store"}},
	}
	return evs
}

func (a *API) incidents() []datadog.IncidentData {
	at := func(d time.Duration) string { return a.start.Add(-d).UTC().Format(time.RFC3339) }
	resolved := at(24 * time.Hour)
	return []datadog.IncidentData{
		{ID: "214", Type: "incidents", Attributes: datadog.IncidentAttributes{
			Title: "Checkout latency degraded: payment provider slow", Status: "active", Severity: "SEV-2",
			Created: at(20 * time.Minute), Modified: at(4 * time.Minute), Detected: at(24 * time.Minute),
			CustomerImpacted: true, CustomerImpactScope: "Some customers can't complete payment",
		}},
		{ID: "213", Type: "incidents", Attributes: datadog.IncidentAttributes{
			Title: "Search results missing new products", Status: "resolved", Severity: "SEV-4",
			Created: at(26 * time.Hour), Modified: resolved, Detected: at(26 * time.Hour), Resolved: &resolved,
		}},
	}
}

func (a *API) slos() []datadog.SLO {
	slo := func(id, name, desc string, tags []string, target, sli, budget float64, status string) datadog.SLO {
		return datadog.SLO{ID: id, Name: name, Description: desc, Tags: tags, Type: "metric",
			Thresholds: []datadog.SLOThreshold{{Target: target, Timeframe: "30d"}},
			OverallStatus: []datadog.SLOOverallStatus{{SLI: sli, Target: target, Timeframe: "30d",
				ErrorBudgetRemaining: budget, Status: status}}}
	}
	return []datadog.SLO{
		slo("slo-chk-avail", "Checkout availability", "Checkout requests that don't fail", []string{"service:checkout"}, 99.9, 99.93, 31.2, "OK"),
		slo("slo-pay-auth", "Payments authorization success", "Authorizations the provider accepts", []string{"service:payments"}, 99.5, 99.52, 3.9, "WARNING"),
		slo("slo-srh-p95", "Search p95 under 300 ms", "Searches that answer in time", []string{"service:search"}, 99, 99.71, 71.4, "OK"),
		slo("slo-web-lcp", "Web store LCP under 2.5 s", "Page views that show their content in time", []string{"service:web-store"}, 95, 97.2, 44.0, "OK"),
	}
}

var metricNames = []string{
	"aws.elb.httpcode_elb_5xx", "aws.elb.request_count",
	"checkout.cart.abandoned", "checkout.orders.placed", "checkout.orders.value",
	"inventory.sync.queue.depth",
	"kubernetes.cpu.usage.total", "kubernetes.memory.usage",
	"kubernetes_state.container.restarts", "kubernetes_state.deployment.replicas_available", "kubernetes_state.pod.ready",
	"nginx.net.request_per_s",
	"payments.authorize.duration", "payments.provider.errors", "payments.provider.requests",
	"postgresql.connections", "postgresql.rows_fetched",
	"redis.mem.used", "redis.net.clients",
	"search.index.lag", "search.query.duration",
	"system.cpu.idle", "system.cpu.system", "system.cpu.user", "system.disk.in_use", "system.load.1",
	"system.mem.usable", "system.mem.used", "system.net.bytes_rcvd", "system.net.bytes_sent",
	"trace.http.request.apdex", "trace.http.request.duration", "trace.http.request.errors", "trace.http.request.hits",
}
