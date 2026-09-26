package demo

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/ngavilan-dogfy/datadog-cli/datadog"
)

// Dashboards: two hand-made ones (checkout, on-call) with every widget type
// the viewer draws, and a few smaller ones built from a list of charts.

type dashInfo struct {
	id, title, desc, author string
	modified                time.Duration // before start
}

var dashboards = []dashInfo{
	{"a2c-9xk-4pd", "Checkout · service health", "Golden signals, errors and pods for one service at a time.", "maria@acme.example", 2 * time.Hour},
	{"p7f-0n3-c4l", "Platform · on-call", "What's on fire across the store, at a glance.", "leo@acme.example", 26 * time.Hour},
	{"k8s-c1u-5tr", "Kubernetes · cluster overview", "Nodes, pods and restarts.", "leo@acme.example", 3 * 24 * time.Hour},
	{"pay-pr0-v1d", "Payments · provider health", "Authorizations, declines and provider latency.", "sam@acme.example", 5 * 24 * time.Hour},
	{"srh-lat-q2x", "Search · latency and index", "How fast and how fresh search is.", "ana@acme.example", 8 * 24 * time.Hour},
	{"web-v1t-4ls", "Web store · Core Web Vitals", "What shoppers feel in the browser.", "ana@acme.example", 12 * 24 * time.Hour},
	{"db-pg0-rd5", "Databases · Postgres and Redis", "Connections, memory and disk.", "leo@acme.example", 20 * 24 * time.Hour},
	{"biz-0rd-rv3", "Business · orders and revenue", "Orders, basket size and abandonment.", "maria@acme.example", 33 * 24 * time.Hour},
}

func (a *API) dashboardList() []datadog.DashboardSummary {
	out := make([]datadog.DashboardSummary, 0, len(dashboards))
	for _, d := range dashboards {
		out = append(out, datadog.DashboardSummary{
			ID: d.id, Title: d.title, Description: d.desc, AuthorHandle: d.author, LayoutType: "ordered",
			ModifiedAt: a.start.Add(-d.modified).UTC().Format(time.RFC3339Nano),
			CreatedAt:  a.start.Add(-d.modified - 90*24*time.Hour).UTC().Format(time.RFC3339Nano),
		})
	}
	return out
}

func (a *API) dashboard(id string) (map[string]any, error) {
	var src string
	switch id {
	case "a2c-9xk-4pd":
		src = checkoutJSON
	case "p7f-0n3-c4l":
		src = oncallJSON
	default:
		for _, d := range dashboards {
			if d.id == id {
				return simpleDashboard(d), nil
			}
		}
		return nil, fmt.Errorf("dashboard %s not found (this is the demo: try the list)", id)
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(src), &m); err != nil {
		return nil, err
	}
	return m, nil
}

const checkoutJSON = `{
 "id": "a2c-9xk-4pd", "title": "Checkout · service health", "layout_type": "ordered", "reflow_type": "fixed",
 "description": "Golden signals, errors and pods for one service at a time.",
 "template_variables": [
  {"name": "env", "prefix": "env", "available_values": ["prod", "staging"], "defaults": ["prod"]},
  {"name": "service", "prefix": "service", "available_values": ["checkout", "payments", "cart", "catalog", "search", "auth", "web-store"], "defaults": ["checkout"]}
 ],
 "widgets": [
  {"id": 1, "layout": {"x": 0, "y": 0, "width": 12, "height": 1}, "definition": {"type": "note",
   "content": "**Checkout** turns carts into orders. Owned by *team-checkout* · on-call in #checkout-oncall · runbook: wiki/checkout"}},
  {"id": 2, "layout": {"x": 0, "y": 1, "width": 12, "height": 5}, "definition": {"type": "group", "title": "Golden signals", "layout_type": "ordered", "widgets": [
   {"id": 21, "layout": {"x": 0, "y": 0, "width": 3, "height": 2}, "definition": {"type": "query_value", "title": "Requests / s", "autoscale": true, "precision": 0,
    "requests": [{"response_format": "scalar", "queries": [{"data_source": "metrics", "name": "hits", "query": "sum:trace.http.request.hits{$env,$service}.as_rate()", "aggregator": "avg"}], "formulas": [{"formula": "hits"}]}],
    "timeseries_background": {"type": "area"}}},
   {"id": 22, "layout": {"x": 3, "y": 0, "width": 3, "height": 2}, "definition": {"type": "query_value", "title": "p95 latency", "autoscale": true, "precision": 2,
    "requests": [{"response_format": "scalar", "queries": [{"data_source": "metrics", "name": "p95", "query": "p95:trace.http.request.duration{$env,$service}", "aggregator": "last"}], "formulas": [{"formula": "p95"}],
     "conditional_formats": [{"comparator": ">", "value": 0.8, "palette": "white_on_red"}, {"comparator": ">", "value": 0.5, "palette": "white_on_yellow"}, {"comparator": "<=", "value": 0.5, "palette": "white_on_green"}]}],
    "timeseries_background": {"type": "area"}}},
   {"id": 23, "layout": {"x": 6, "y": 0, "width": 3, "height": 2}, "definition": {"type": "query_value", "title": "Error rate", "autoscale": false, "precision": 2, "custom_unit": "%",
    "requests": [{"response_format": "scalar", "queries": [
      {"data_source": "metrics", "name": "errors", "query": "sum:trace.http.request.errors{$env,$service}.as_count()", "aggregator": "sum"},
      {"data_source": "metrics", "name": "hits", "query": "sum:trace.http.request.hits{$env,$service}.as_count()", "aggregator": "sum"}],
     "formulas": [{"formula": "100 * errors / hits"}],
     "conditional_formats": [{"comparator": ">", "value": 2, "palette": "white_on_red"}, {"comparator": ">", "value": 0.5, "palette": "white_on_yellow"}, {"comparator": "<=", "value": 0.5, "palette": "white_on_green"}]}]}},
   {"id": 24, "layout": {"x": 9, "y": 0, "width": 3, "height": 2}, "definition": {"type": "query_value", "title": "Apdex", "autoscale": false, "precision": 2,
    "requests": [{"response_format": "scalar", "queries": [{"data_source": "metrics", "name": "apdex", "query": "avg:trace.http.request.apdex{$env,$service}", "aggregator": "last"}], "formulas": [{"formula": "apdex"}],
     "conditional_formats": [{"comparator": "<", "value": 0.8, "palette": "white_on_red"}, {"comparator": "<", "value": 0.9, "palette": "white_on_yellow"}, {"comparator": ">=", "value": 0.9, "palette": "white_on_green"}]}]}},
   {"id": 25, "layout": {"x": 0, "y": 2, "width": 6, "height": 3}, "definition": {"type": "timeseries", "title": "Requests by endpoint", "show_legend": true,
    "requests": [{"display_type": "area", "queries": [{"data_source": "metrics", "name": "q", "query": "sum:trace.http.request.hits{$env,$service} by {resource_name}.as_rate()"}], "formulas": [{"formula": "q"}], "style": {"palette": "cool"}}]}},
   {"id": 26, "layout": {"x": 6, "y": 2, "width": 6, "height": 3}, "definition": {"type": "timeseries", "title": "Latency p50 · p95 · p99", "show_legend": true,
    "requests": [{"display_type": "line", "queries": [
      {"data_source": "metrics", "name": "p50", "query": "p50:trace.http.request.duration{$env,$service}"},
      {"data_source": "metrics", "name": "p95", "query": "p95:trace.http.request.duration{$env,$service}"},
      {"data_source": "metrics", "name": "p99", "query": "p99:trace.http.request.duration{$env,$service}"}],
     "formulas": [{"formula": "p50", "alias": "p50"}, {"formula": "p95", "alias": "p95"}, {"formula": "p99", "alias": "p99"}], "style": {"palette": "warm"}}],
    "markers": [{"value": "y = 0.8", "display_type": "error dashed", "label": "p95 alert"}]}}
  ]}},
  {"id": 3, "layout": {"x": 0, "y": 6, "width": 12, "height": 6}, "definition": {"type": "group", "title": "Errors", "layout_type": "ordered", "widgets": [
   {"id": 31, "layout": {"x": 0, "y": 0, "width": 6, "height": 3}, "definition": {"type": "timeseries", "title": "Error logs by service",
    "requests": [{"display_type": "bars", "queries": [{"data_source": "logs", "name": "l", "indexes": ["*"], "search": {"query": "status:error $env"}, "compute": {"aggregation": "count"},
      "group_by": [{"facet": "service", "limit": 5, "sort": {"aggregation": "count", "order": "desc"}}]}], "formulas": [{"formula": "l"}], "style": {"palette": "warm"}}]}},
   {"id": 32, "layout": {"x": 6, "y": 0, "width": 3, "height": 3}, "definition": {"type": "toplist", "title": "Errors by endpoint",
    "requests": [{"response_format": "scalar", "queries": [{"data_source": "metrics", "name": "e", "query": "sum:trace.http.request.errors{$env,$service} by {resource_name}.as_count()", "aggregator": "sum"}],
     "formulas": [{"formula": "e", "limit": {"count": 10, "order": "desc"}}]}]}},
   {"id": 33, "layout": {"x": 9, "y": 0, "width": 3, "height": 3}, "definition": {"type": "change", "title": "Errors vs. yesterday",
    "requests": [{"response_format": "scalar", "compare_to": "day_before", "increase_good": false, "order_by": "change", "change_type": "relative",
     "queries": [{"data_source": "metrics", "name": "e", "query": "sum:trace.http.request.errors{$env} by {service}.as_count()", "aggregator": "sum"}], "formulas": [{"formula": "e"}]}]}},
   {"id": 34, "layout": {"x": 0, "y": 3, "width": 12, "height": 3}, "definition": {"type": "list_stream", "title": "Latest errors",
    "requests": [{"response_format": "event_list", "query": {"data_source": "logs_stream", "query_string": "status:error $service $env", "indexes": []},
     "columns": [{"field": "status_line", "width": "auto"}, {"field": "timestamp", "width": "auto"}, {"field": "service", "width": "auto"}, {"field": "content", "width": "full"}]}]}}
  ]}},
  {"id": 4, "layout": {"x": 0, "y": 12, "width": 12, "height": 5}, "definition": {"type": "group", "title": "Infrastructure", "layout_type": "ordered", "widgets": [
   {"id": 41, "layout": {"x": 0, "y": 0, "width": 6, "height": 3}, "definition": {"type": "timeseries", "title": "CPU by pod",
    "requests": [{"display_type": "line", "queries": [{"data_source": "metrics", "name": "c", "query": "avg:kubernetes.cpu.usage.total{$env,kube_deployment:$service.value} by {pod_name}"}], "formulas": [{"formula": "c"}]}],
    "yaxis": {"min": "0", "max": "100"}}},
   {"id": 42, "layout": {"x": 6, "y": 0, "width": 6, "height": 3}, "definition": {"type": "timeseries", "title": "Memory by pod",
    "requests": [{"display_type": "line", "queries": [{"data_source": "metrics", "name": "m", "query": "avg:kubernetes.memory.usage{$env,kube_deployment:$service.value} by {pod_name}"}], "formulas": [{"formula": "m"}], "style": {"palette": "purple"}}]}},
   {"id": 43, "layout": {"x": 0, "y": 3, "width": 3, "height": 2}, "definition": {"type": "query_value", "title": "Pods ready", "precision": 0, "autoscale": false,
    "requests": [{"response_format": "scalar", "queries": [{"data_source": "metrics", "name": "p", "query": "sum:kubernetes_state.pod.ready{$env,kube_deployment:$service.value}", "aggregator": "last"}], "formulas": [{"formula": "p"}]}]}},
   {"id": 44, "layout": {"x": 3, "y": 3, "width": 9, "height": 2}, "definition": {"type": "manage_status", "title": "Monitors", "summary_type": "monitors",
    "display_format": "countsAndList", "color_preference": "text", "hide_zero_counts": true, "query": "$service", "sort": "status,asc"}}
  ]}}
 ]
}`

const oncallJSON = `{
 "id": "p7f-0n3-c4l", "title": "Platform · on-call", "layout_type": "ordered", "reflow_type": "fixed",
 "description": "What's on fire across the store, at a glance.",
 "template_variables": [{"name": "env", "prefix": "env", "available_values": ["prod", "staging"], "defaults": ["prod"]}],
 "widgets": [
  {"id": 1, "layout": {"x": 0, "y": 0, "width": 12, "height": 1}, "definition": {"type": "note",
   "content": "Alerts first, then traffic and errors by service. Escalate in #platform-oncall; incidents are declared from the monitor."}},
  {"id": 2, "layout": {"x": 0, "y": 1, "width": 6, "height": 4}, "definition": {"type": "manage_status", "title": "Alerting and warning monitors", "summary_type": "monitors",
   "display_format": "countsAndList", "color_preference": "text", "hide_zero_counts": true, "query": "status:alert status:warn status:no_data", "sort": "status,asc"}},
  {"id": 3, "layout": {"x": 6, "y": 1, "width": 3, "height": 2}, "definition": {"type": "query_value", "title": "Error logs", "autoscale": true, "precision": 0,
   "requests": [{"response_format": "scalar", "queries": [{"data_source": "logs", "name": "l", "indexes": ["*"], "search": {"query": "status:error $env"}, "compute": {"aggregation": "count"}}], "formulas": [{"formula": "l"}],
    "conditional_formats": [{"comparator": ">", "value": 1000, "palette": "white_on_red"}, {"comparator": "<=", "value": 1000, "palette": "white_on_green"}]}],
   "timeseries_background": {"type": "bars"}}},
  {"id": 4, "layout": {"x": 9, "y": 1, "width": 3, "height": 2}, "definition": {"type": "query_value", "title": "Requests / s", "autoscale": true, "precision": 0,
   "requests": [{"response_format": "scalar", "queries": [{"data_source": "metrics", "name": "h", "query": "sum:trace.http.request.hits{$env}.as_rate()", "aggregator": "avg"}], "formulas": [{"formula": "h"}]}],
   "timeseries_background": {"type": "area"}}},
  {"id": 5, "layout": {"x": 6, "y": 3, "width": 6, "height": 2}, "definition": {"type": "toplist", "title": "Error logs by service",
   "requests": [{"response_format": "scalar", "queries": [{"data_source": "logs", "name": "l", "indexes": ["*"], "search": {"query": "status:error $env"}, "compute": {"aggregation": "count"},
     "group_by": [{"facet": "service", "limit": 10, "sort": {"aggregation": "count", "order": "desc"}}]}], "formulas": [{"formula": "l"}]}]}},
  {"id": 6, "layout": {"x": 0, "y": 5, "width": 6, "height": 3}, "definition": {"type": "timeseries", "title": "Requests by service",
   "requests": [{"display_type": "area", "queries": [{"data_source": "metrics", "name": "q", "query": "sum:trace.http.request.hits{$env} by {service}.as_rate()"}], "formulas": [{"formula": "q"}], "style": {"palette": "cool"}}]}},
  {"id": 7, "layout": {"x": 6, "y": 5, "width": 6, "height": 3}, "definition": {"type": "timeseries", "title": "p95 latency by service",
   "requests": [{"display_type": "line", "queries": [{"data_source": "metrics", "name": "q", "query": "p95:trace.http.request.duration{$env} by {service}"}], "formulas": [{"formula": "q"}]}],
   "markers": [{"value": "y = 0.8", "display_type": "error dashed"}]}},
  {"id": 8, "layout": {"x": 0, "y": 8, "width": 12, "height": 3}, "definition": {"type": "timeseries", "title": "Errors by service",
   "requests": [{"display_type": "bars", "queries": [{"data_source": "metrics", "name": "e", "query": "sum:trace.http.request.errors{$env} by {service}.as_count()"}], "formulas": [{"formula": "e"}], "style": {"palette": "warm"}}]}}
 ]
}`

// simpleDashboard builds a dashboard from a few charts about its topic.
func simpleDashboard(d dashInfo) map[string]any {
	type chart struct{ kind, title, query, display string }
	charts := map[string][]chart{
		"k8s-c1u-5tr": {
			{"query_value", "Pods ready", "sum:kubernetes_state.pod.ready{env:prod}", ""},
			{"query_value", "Restarts (1h)", "sum:kubernetes_state.container.restarts{env:prod}", ""},
			{"timeseries", "CPU by deployment", "avg:kubernetes.cpu.usage.total{env:prod} by {kube_deployment}", "line"},
			{"timeseries", "Memory by deployment", "avg:kubernetes.memory.usage{env:prod} by {kube_deployment}", "area"},
			{"toplist", "Restarts by deployment", "sum:kubernetes_state.container.restarts{env:prod} by {kube_deployment}", ""},
			{"timeseries", "Network in", "sum:system.net.bytes_rcvd{env:prod} by {host}", "line"},
		},
		"pay-pr0-v1d": {
			{"query_value", "Authorizations / s", "sum:payments.provider.requests{env:prod}.as_rate()", ""},
			{"query_value", "Provider p95", "p95:payments.authorize.duration{env:prod}", ""},
			{"timeseries", "Provider errors", "sum:payments.provider.errors{env:prod}.as_count()", "bars"},
			{"timeseries", "Authorize latency p50 · p95", "p95:trace.http.request.duration{service:payments,env:prod}", "line"},
			{"toplist", "Errors by endpoint", "sum:trace.http.request.errors{service:payments,env:prod} by {resource_name}.as_count()", ""},
			{"timeseries", "Requests by endpoint", "sum:trace.http.request.hits{service:payments,env:prod} by {resource_name}.as_rate()", "area"},
		},
		"srh-lat-q2x": {
			{"query_value", "Searches / s", "sum:trace.http.request.hits{service:search,env:prod}.as_rate()", ""},
			{"query_value", "Index lag", "avg:search.index.lag{env:prod}", ""},
			{"timeseries", "p95 by endpoint", "p95:trace.http.request.duration{service:search,env:prod} by {resource_name}", "line"},
			{"timeseries", "Index lag", "avg:search.index.lag{env:prod}", "area"},
		},
		"web-v1t-4ls": {
			{"query_value", "Page views / s", "sum:trace.http.request.hits{service:web-store,env:prod}.as_rate()", ""},
			{"query_value", "JS errors (1h)", "sum:trace.http.request.errors{service:web-store,env:prod}.as_count()", ""},
			{"timeseries", "Requests by page", "sum:trace.http.request.hits{service:web-store,env:prod} by {resource_name}.as_rate()", "area"},
			{"timeseries", "Server time p95", "p95:trace.http.request.duration{service:web-store,env:prod}", "line"},
		},
		"db-pg0-rd5": {
			{"query_value", "Postgres connections", "avg:postgresql.connections{role:db}", ""},
			{"query_value", "Redis clients", "avg:redis.net.clients{env:prod}", ""},
			{"timeseries", "Connections by host", "avg:postgresql.connections{role:db} by {host}", "line"},
			{"timeseries", "Disk in use", "avg:system.disk.in_use{role:db} by {host}", "line"},
			{"timeseries", "Redis memory", "avg:redis.mem.used{env:prod}", "area"},
			{"timeseries", "Load", "avg:system.load.1{role:db} by {host}", "line"},
		},
		"biz-0rd-rv3": {
			{"query_value", "Orders / s", "sum:checkout.orders.placed{env:prod}.as_rate()", ""},
			{"query_value", "Average basket", "avg:checkout.orders.value{env:prod}", ""},
			{"timeseries", "Orders", "sum:checkout.orders.placed{env:prod}.as_count()", "bars"},
			{"timeseries", "Basket size", "avg:checkout.orders.value{env:prod}", "line"},
		},
	}[d.id]
	widgets := []any{map[string]any{"id": 1.0, "layout": map[string]any{"x": 0.0, "y": 0.0, "width": 12.0, "height": 1.0},
		"definition": map[string]any{"type": "note", "content": d.desc}}}
	x, y := 0, 1
	for i, c := range charts {
		w, h := 6, 3
		if c.kind == "query_value" {
			w, h = 3, 2
		}
		if c.kind == "toplist" {
			w = 6
		}
		if x+w > 12 {
			x, y = 0, y+h
		}
		req := map[string]any{"queries": []any{map[string]any{"data_source": "metrics", "name": "q", "query": c.query, "aggregator": "avg"}},
			"formulas": []any{map[string]any{"formula": "q"}}}
		if c.kind != "timeseries" {
			req["response_format"] = "scalar"
		} else {
			req["display_type"] = c.display
		}
		def := map[string]any{"type": c.kind, "title": c.title, "requests": []any{req}}
		if c.kind == "query_value" {
			def["autoscale"] = true
			def["precision"] = 1.0
			def["timeseries_background"] = map[string]any{"type": "area"}
		}
		widgets = append(widgets, map[string]any{"id": float64(i + 2),
			"layout":     map[string]any{"x": float64(x), "y": float64(y), "width": float64(w), "height": float64(h)},
			"definition": def})
		x += w
	}
	return map[string]any{"id": d.id, "title": d.title, "description": d.desc, "layout_type": "ordered", "reflow_type": "fixed", "widgets": widgets}
}
