package cmd

import (
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// ddLink is what a Datadog web link points at, as far as the CLI can follow
// it: the kind of page, its id, and the context the page had (time window,
// template variables, search query).
type ddLink struct {
	Kind  string // dashboard, monitor, trace, logs, traces, apm-service, incident, slo, notebook, metric, events, audit, host
	ID    string
	Query string            // logs/traces search, metric query
	Vars  map[string]string // dashboard template variables
	From  time.Time         // zero when the link doesn't fix one
	To    time.Time
	Live  bool // the window moves with now: only its length matters
	Env   string
}

var (
	reDashPath     = regexp.MustCompile(`^/dashboard/([a-z0-9]{3}-[a-z0-9]{3}-[a-z0-9]{3})`)
	reMonitorPath  = regexp.MustCompile(`^/monitors/(\d+)`)
	reTracePath    = regexp.MustCompile(`^/apm/trace/([0-9a-fA-Fx]+)`)
	reServicePath  = regexp.MustCompile(`^/apm/services/([^/?#]+)`)
	reIncidentPath = regexp.MustCompile(`^/incidents/(\d+)`)
	reNotebookPath = regexp.MustCompile(`^/notebook/(\d+)`)
	reTplVar       = regexp.MustCompile(`^tpl_var_([A-Za-z0-9_\-.]+?)(\[\d+\])?$`)
)

// parseDDLink reads a Datadog web link. ok is false when it isn't one.
func parseDDLink(raw string) (ddLink, bool) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Host == "" || !strings.Contains(u.Host, "datadog") {
		return ddLink{}, false
	}
	q := u.Query()
	l := ddLink{Vars: map[string]string{}}
	for k, vs := range q {
		if m := reTplVar.FindStringSubmatch(k); m != nil && len(vs) > 0 {
			l.Vars[m[1]] = vs[0]
		}
	}
	ms := func(k string) time.Time {
		if n, err := strconv.ParseInt(q.Get(k), 10, 64); err == nil && n > 0 {
			return time.UnixMilli(n)
		}
		return time.Time{}
	}
	l.From, l.To = ms("from_ts"), ms("to_ts")
	if l.From.IsZero() {
		l.From, l.To = ms("start"), ms("end")
	}
	l.Live = q.Get("live") == "true"
	l.Env = q.Get("env")
	path := u.Path
	switch {
	case reDashPath.MatchString(path):
		l.Kind, l.ID = "dashboard", reDashPath.FindStringSubmatch(path)[1]
	case reMonitorPath.MatchString(path):
		l.Kind, l.ID = "monitor", reMonitorPath.FindStringSubmatch(path)[1]
	case reTracePath.MatchString(path):
		l.Kind, l.ID = "trace", reTracePath.FindStringSubmatch(path)[1]
	case q.Get("traceID") != "" || q.Get("trace_id") != "":
		l.Kind, l.ID = "trace", q.Get("traceID")
		if l.ID == "" {
			l.ID = q.Get("trace_id")
		}
	case reServicePath.MatchString(path):
		l.Kind = "apm-service"
		l.ID, _ = url.PathUnescape(reServicePath.FindStringSubmatch(path)[1])
	case reIncidentPath.MatchString(path):
		l.Kind, l.ID = "incident", reIncidentPath.FindStringSubmatch(path)[1]
	case reNotebookPath.MatchString(path):
		l.Kind, l.ID = "notebook", reNotebookPath.FindStringSubmatch(path)[1]
	case strings.HasPrefix(path, "/logs"):
		l.Kind, l.Query = "logs", q.Get("query")
	case strings.HasPrefix(path, "/apm/traces"):
		l.Kind, l.Query = "traces", q.Get("query")
	case strings.HasPrefix(path, "/slo"):
		l.Kind, l.ID = "slo", q.Get("slo_id")
		if l.ID == "" {
			l.ID = q.Get("sloId")
		}
	case strings.HasPrefix(path, "/metric/explorer") || strings.HasPrefix(path, "/metric/summary"):
		l.Kind = "metric"
		l.Query = metricQueryFromLink(q)
	case strings.HasPrefix(path, "/event/explorer") || strings.HasPrefix(path, "/event/stream"):
		l.Kind, l.Query = "events", q.Get("query")
	case strings.HasPrefix(path, "/audit-trail"):
		l.Kind, l.Query = "audit", q.Get("query")
	case strings.HasPrefix(path, "/infrastructure") && q.Get("host") != "":
		l.Kind, l.ID = "host", q.Get("host")
	default:
		return l, false
	}
	return l, true
}

// window is the link's time window as a span ending at an instant (zero end
// = now), or false when the link doesn't set one.
func (l ddLink) window() (time.Duration, time.Time, bool) {
	if l.From.IsZero() || !l.To.After(l.From) {
		return 0, time.Time{}, false
	}
	if l.Live {
		return l.To.Sub(l.From), time.Time{}, true
	}
	return l.To.Sub(l.From), l.To, true
}

// metricQueryFromLink rebuilds a metric query from a Metrics Explorer link:
// the exact query when the link carries one (the CLI's own links do), else
// what the explorer's fields say (agg:metric{scope} by {group}).
func metricQueryFromLink(q url.Values) string {
	if exact := strings.TrimSpace(q.Get("q")); exact != "" {
		return exact
	}
	metric := q.Get("exp_metric")
	if metric == "" {
		return q.Get("metric")
	}
	if q.Get("exp_agg") == "" && q.Get("exp_scope") == "" && q.Get("exp_group") == "" {
		return metric
	}
	agg, scope := q.Get("exp_agg"), q.Get("exp_scope")
	if agg == "" {
		agg = "avg"
	}
	if scope == "" {
		scope = "*"
	}
	out := agg + ":" + metric + "{" + scope + "}"
	if g := q.Get("exp_group"); g != "" {
		out += " by {" + g + "}"
	}
	return out
}
