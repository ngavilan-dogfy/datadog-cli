package tui

import (
	"regexp"
	"strconv"
	"strings"
)

// A monitor's query says what it watches; the chart shows that over the
// time range, with the thresholds as lines. Metric monitors map to a v1
// metric expression (arithmetic between queries works there); log, trace,
// RUM and audit monitors to an events query.

type monitorChart struct {
	metric            string         // v1 metric expression, or
	events            map[string]any // v2 events query (logs, spans…)
	window            string         // evaluation window, e.g. "last_5m"
	critical, warning *float64
}

var (
	reMetricMonitor = regexp.MustCompile(`^\s*\w+\(([^)]*)\)\s*:\s*(.+?)\s*(>=|<=|>|<|==|!=)\s*(-?[\d.]+)\s*$`)
	reEventsSearch  = regexp.MustCompile(`^(logs|trace-analytics|rum|audits|ci-pipelines|events)\(\s*"((?:[^"\\]|\\.)*)"\s*\)`)
	reRollup        = regexp.MustCompile(`\.rollup\(\s*"(\w+)"(?:\s*,\s*"([^"]*)")?`)
	reBy            = regexp.MustCompile(`\.by\(\s*"([^"]*)"`)
	reLast          = regexp.MustCompile(`\.last\(\s*"([^"]*)"`)
	reTail          = regexp.MustCompile(`(>=|<=|>|<|==|!=)\s*(-?[\d.]+)\s*$`)
)

var eventsSource = map[string]string{
	"logs": "logs", "trace-analytics": "spans", "rum": "rum", "audits": "audit", "ci-pipelines": "ci_pipelines", "events": "events",
}

// parseMonitorQuery turns a monitor query into something chartable, or
// nil (service checks, composites, SLO alerts…).
func parseMonitorQuery(query string, thresholds map[string]any) *monitorChart {
	mc := &monitorChart{}
	if v, ok := thresholds["critical"]; ok {
		f := num(v)
		mc.critical = &f
	}
	if v, ok := thresholds["warning"]; ok && v != nil {
		f := num(v)
		mc.warning = &f
	}
	q := strings.TrimSpace(query)
	if m := reEventsSearch.FindStringSubmatch(q); m != nil {
		search := strings.ReplaceAll(m[2], `\"`, `"`)
		compute := map[string]any{"aggregation": "count"}
		if r := reRollup.FindStringSubmatch(q); r != nil {
			compute["aggregation"] = r[1]
			if r[2] != "" {
				compute["metric"] = r[2]
			}
		}
		ev := map[string]any{
			"data_source": eventsSource[m[1]], "name": "a", "search": map[string]any{"query": search},
			"compute": compute, "indexes": []any{"*"},
		}
		if b := reBy.FindStringSubmatch(q); b != nil && b[1] != "" {
			var groups []any
			for _, f := range strings.Split(b[1], ",") {
				groups = append(groups, map[string]any{"facet": strings.TrimSpace(f), "limit": 10,
					"sort": map[string]any{"aggregation": compute["aggregation"], "order": "desc"}})
			}
			ev["group_by"] = groups
		}
		if m[1] == "logs" {
			ev["indexes"] = []any{"*"}
		} else {
			delete(ev, "indexes")
		}
		mc.events = ev
		if l := reLast.FindStringSubmatch(q); l != nil {
			mc.window = "last_" + l[1]
		}
		if mc.critical == nil {
			if t := reTail.FindStringSubmatch(q); t != nil {
				if f, err := strconv.ParseFloat(t[2], 64); err == nil {
					mc.critical = &f
				}
			}
		}
		return mc
	}
	if m := reMetricMonitor.FindStringSubmatch(q); m != nil {
		mc.window, mc.metric = m[1], unwrapFunctions(m[2])
		if mc.critical == nil {
			if f, err := strconv.ParseFloat(m[4], 64); err == nil {
				mc.critical = &f
			}
		}
		return mc
	}
	return nil
}

// unwrapFunctions charts what forecast(), anomalies() and outliers() look
// at: their first argument (the API can't return the prediction itself).
func unwrapFunctions(expr string) string {
	for _, fn := range []string{"forecast(", "anomalies(", "outliers("} {
		if !strings.HasPrefix(expr, fn) {
			continue
		}
		depth := 0
		for i := len(fn) - 1; i < len(expr); i++ {
			switch expr[i] {
			case '(':
				depth++
			case ')':
				depth--
			case ',':
				if depth == 1 {
					return strings.TrimSpace(expr[len(fn):i])
				}
			}
		}
	}
	return expr
}
