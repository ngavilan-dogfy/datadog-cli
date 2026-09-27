package cmd

import (
	"fmt"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// References are the links a report cites: each one opens the exact view
// behind a finding (same query, same window), and each one can be fed back
// to 'datadog read' — an agent can dig into any reference it's given.

// ref is one cited link.
type ref struct {
	N     int    `json:"n"`
	Title string `json:"title"`
	URL   string `json:"url"`
	Query string `json:"query,omitempty"` // the exact query, when the page can't hold all of it
}

// refList numbers references in the order they're cited, once each.
type refList struct {
	list  []ref
	byURL map[string]int
}

// add cites a link and returns its number (0 when there's no link).
func (r *refList) add(title, link, query string) int {
	if link == "" {
		return 0
	}
	if r.byURL == nil {
		r.byURL = map[string]int{}
	}
	if n, ok := r.byURL[link]; ok {
		return n
	}
	n := len(r.list) + 1
	r.list = append(r.list, ref{N: n, Title: title, URL: link, Query: query})
	r.byURL[link] = n
	return n
}

// webBase is the site's web address (https://app.datadoghq.eu); empty when
// there's no client, so reports degrade to text.
func webBase() string {
	if client == nil {
		return ""
	}
	return strings.TrimSuffix(client.BrowseURL("/"), "/")
}

func epochMS(t time.Time) string { return strconv.FormatInt(t.UnixMilli(), 10) }

func webLink(path string, q url.Values) string {
	base := webBase()
	if base == "" {
		return ""
	}
	if len(q) == 0 {
		return base + path
	}
	return base + path + "?" + q.Encode()
}

func fixedWindow(q url.Values, from, to time.Time, fromKey, toKey string) url.Values {
	if !from.IsZero() && !to.IsZero() {
		q.Set(fromKey, epochMS(from))
		q.Set(toKey, epochMS(to))
		q.Set("live", "false")
	}
	return q
}

// logsLink opens the Log Explorer on a query and window.
func logsLink(query string, from, to time.Time) string {
	return webLink("/logs", fixedWindow(url.Values{"query": {query}}, from, to, "from_ts", "to_ts"))
}

// spansLink opens the Trace Explorer on a spans query and window.
func spansLink(query string, from, to time.Time) string {
	return webLink("/apm/traces", fixedWindow(url.Values{"query": {query}}, from, to, "start", "end"))
}

// serviceLink opens an APM service's page (its operation's resources).
func serviceLink(service, env, operation string, from, to time.Time) string {
	path := "/apm/services/" + url.PathEscape(service)
	if operation != "" {
		path += "/operations/" + url.PathEscape(operation) + "/resources"
	}
	q := url.Values{}
	if env != "" {
		q.Set("env", env)
	}
	return webLink(path, fixedWindow(q, from, to, "start", "end"))
}

// traceLink opens one trace.
func traceLink(id string) string { return webLink("/apm/trace/"+id, nil) }

// monitorLink opens a monitor, on a window when one is given.
func monitorLink(id int64, from, to time.Time) string {
	return webLink(fmt.Sprintf("/monitors/%d", id), fixedWindow(url.Values{}, from, to, "from_ts", "to_ts"))
}

func dashboardLink(id string, from, to time.Time) string {
	return webLink("/dashboard/"+id, fixedWindow(url.Values{}, from, to, "from_ts", "to_ts"))
}

func eventsLink(query string, from, to time.Time) string {
	return webLink("/event/explorer", fixedWindow(url.Values{"query": {query}}, from, to, "from_ts", "to_ts"))
}

func auditLink(query string, from, to time.Time) string {
	return webLink("/audit-trail", fixedWindow(url.Values{"query": {query}}, from, to, "from_ts", "to_ts"))
}

func hostLink(host string) string { return webLink("/infrastructure", url.Values{"host": {host}}) }

func incidentLink(id string) string { return webLink("/incidents/"+id, nil) }

func sloLink(id string) string { return webLink("/slo", url.Values{"slo_id": {id}}) }

// reMetricQuery splits a simple metric query: agg:metric{scope} by {group}.
var reMetricQuery = regexp.MustCompile(`^\s*(?:(\w+):)?([\w.]+)\{([^}]*)\}(?:\s+by\s+\{([^}]*)\})?`)

// metricLink opens the Metrics Explorer on a query and window. The explorer
// holds the metric, scope, aggregation and grouping; q keeps the exact
// query (rollups, as_count, arithmetic) for 'datadog read'.
func metricLink(query string, from, to time.Time) string {
	q := url.Values{"q": {query}, "exp_row_type": {"metric"}}
	if m := reMetricQuery.FindStringSubmatch(query); m != nil {
		q.Set("exp_metric", m[2])
		if m[1] != "" {
			q.Set("exp_agg", m[1])
		}
		if m[3] != "" && m[3] != "*" {
			q.Set("exp_scope", m[3])
		}
		if m[4] != "" {
			q.Set("exp_group", m[4])
		}
	}
	return webLink("/metric/explorer", fixedWindow(q, from, to, "from_ts", "to_ts"))
}

// cite formats reference numbers for text: " [1][3]".
func cite(ns ...int) string {
	var b strings.Builder
	for _, n := range ns {
		if n > 0 {
			fmt.Fprintf(&b, "[%d]", n)
		}
	}
	if b.Len() == 0 {
		return ""
	}
	return " " + b.String()
}

// citeMD formats reference numbers as markdown links to their pages.
func citeMD(refs []ref, ns ...int) string {
	var b strings.Builder
	for _, n := range ns {
		if n > 0 && n <= len(refs) {
			fmt.Fprintf(&b, "[[%d]](%s)", n, refs[n-1].URL)
		}
	}
	if b.Len() == 0 {
		return ""
	}
	return " " + b.String()
}
