package tui

import "testing"

func TestParseMonitorQuery(t *testing.T) {
	thr := func(c float64) map[string]any { return map[string]any{"critical": c} }
	mc := parseMonitorQuery("avg(last_5m):avg:system.io.await{*} by {device} > 500", thr(500))
	if mc == nil || mc.metric != "avg:system.io.await{*} by {device}" || mc.window != "last_5m" || *mc.critical != 500 {
		t.Fatalf("metric: %+v", mc)
	}
	mc = parseMonitorQuery(`max(next_3d):forecast((1 - avg:system.disk.free{*} by {host,device} / avg:system.disk.total{*} by {host,device}) * 100, "seasonal", 1, interval="60m", seasonality="weekly") >= 99`, thr(99))
	if mc == nil || mc.metric != "(1 - avg:system.disk.free{*} by {host,device} / avg:system.disk.total{*} by {host,device}) * 100" {
		t.Fatalf("forecast: %+v", mc)
	}
	mc = parseMonitorQuery(`sum(last_30m):sum:trace.fastify.request.errors{env:production,service:api}.as_count() / sum:trace.fastify.request.hits{env:production,service:api}.as_count() * 100 > 7.5`, map[string]any{"critical": 7.5, "warning": 3.0})
	if mc == nil || *mc.warning != 3 || mc.metric == "" {
		t.Fatalf("ratio: %+v", mc)
	}
	mc = parseMonitorQuery(`logs("service:api container_name:gaeapp -source:datadog-agent status:error env:production").index("*").rollup("count").last("30m") > 2500`, thr(2500))
	if mc == nil || mc.events == nil || mc.window != "last_30m" {
		t.Fatalf("logs: %+v", mc)
	}
	if s := obj(mc.events["search"]); str(s["query"]) != "service:api container_name:gaeapp -source:datadog-agent status:error env:production" || str(mc.events["data_source"]) != "logs" {
		t.Errorf("logs query: %+v", mc.events)
	}
	mc = parseMonitorQuery(`trace-analytics("@peer.hostname:*impackta.com status:error env:production").index("trace-search", "djm-search").rollup("count").last("1h") > 100`, thr(100))
	if mc == nil || str(mc.events["data_source"]) != "spans" || mc.events["indexes"] != nil {
		t.Fatalf("spans: %+v", mc)
	}
	mc = parseMonitorQuery(`events("source:watchdog (story_category:apm)").rollup("count").by("story_key").last("30m") > 0`, thr(0))
	if mc == nil || len(list(mc.events["group_by"])) != 1 {
		t.Fatalf("events by: %+v", mc)
	}
	if parseMonitorQuery(`"datadog.agent.up".over("*").by("host").last(2).count_by_status()`, nil) != nil {
		t.Error("service checks have nothing to chart")
	}
}
