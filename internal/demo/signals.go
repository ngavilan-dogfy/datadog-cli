package demo

import (
	"encoding/binary"
	"hash/fnv"
	"math"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/ngavilan-dogfy/datadog-cli/datadog"
)

// Every number in the demo comes from here: a daily rhythm, smooth noise
// seeded by the series name, and one incident — payments slow down, so
// checkout's latency and errors climb, 25 minutes before the demo started.
// Values depend only on the series and the absolute time, so zooming or
// changing the range never reshuffles them.

// hash01 is a stable pseudo-random number in [0, 1).
func hash01(key string, i int64) float64 {
	h := fnv.New64a()
	h.Write([]byte(key))
	var b [8]byte
	binary.LittleEndian.PutUint64(b[:], uint64(i))
	h.Write(b[:])
	return float64(h.Sum64()>>11) / (1 << 53)
}

// smooth is value noise in [-1, 1] that changes over about period seconds.
func smooth(key string, sec, period float64) float64 {
	x := sec / period
	i := math.Floor(x)
	f := x - i
	a := hash01(key, int64(i))*2 - 1
	b := hash01(key, int64(i)+1)*2 - 1
	u := f * f * (3 - 2*f)
	return a + (b-a)*u
}

// noise mixes slow and fast wobble, in about [-1, 1].
func noise(key string, t time.Time) float64 {
	s := float64(t.Unix())
	return 0.55*smooth(key, s, 120) + 0.3*smooth(key+"~", s, 900) + 0.15*smooth(key+"^", s, 25)
}

// daily is 0 at 4:00 and 1 around 16:00 local time; weekends are quieter.
func daily(t time.Time) float64 {
	t = t.Local()
	h := float64(t.Hour()) + float64(t.Minute())/60
	d := 0.5 - 0.5*math.Cos(2*math.Pi*(h-4)/24)
	if wd := t.Weekday(); wd == time.Saturday || wd == time.Sunday {
		d *= 0.7
	}
	return d
}

// incident is how bad things are at t, 0 to 1: a quick rise 25 minutes
// before start, a plateau, then a slow partial recovery that's still going.
func (a *API) incident(t time.Time) float64 {
	s := a.start.Add(-25 * time.Minute)
	if t.Before(s) {
		return 0
	}
	m := t.Sub(s).Minutes()
	switch {
	case m < 4:
		x := m / 4
		return x * x * (3 - 2*x)
	case m < 17:
		return 1
	case m < 90:
		return 0.55 + 0.45*math.Exp(-(m-17)/5)
	case m < 110:
		return 0.55 * (1 - (m-90)/20)
	}
	return 0
}

// exposure is how much a service suffers from the incident.
func exposure(scope map[string]string) float64 {
	if scope["env"] == "staging" {
		return 0
	}
	switch svc := firstOf(scope, "service", "kube_deployment"); svc {
	case "checkout", "payments":
		return 1
	case "", "*":
		return 0.5 // the whole org: checkout and payments are in there
	case "cart":
		return 0.25
	}
	return 0
}

func firstOf(m map[string]string, keys ...string) string {
	for _, k := range keys {
		if v, ok := m[k]; ok {
			return v
		}
	}
	return ""
}

// ─── services ────────────────────────────────────────────────────

var services = []string{"web-store", "catalog", "search", "cart", "auth", "checkout", "payments"}

// traffic in requests per second at the daily peak.
var peakRPS = map[string]float64{
	"web-store": 1300, "catalog": 900, "search": 610, "cart": 420, "auth": 260, "checkout": 140, "payments": 95,
}

// typical p50 latency, seconds.
var p50 = map[string]float64{
	"web-store": 0.060, "catalog": 0.035, "search": 0.120, "cart": 0.018, "auth": 0.045, "checkout": 0.085, "payments": 0.140,
}

var endpoints = map[string][]string{
	"checkout":  {"POST /api/checkout", "GET /api/checkout/summary", "POST /api/checkout/coupon", "GET /api/checkout/shipping"},
	"payments":  {"POST /api/payments/authorize", "POST /api/payments/capture", "POST /api/payments/refund"},
	"cart":      {"GET /api/cart", "POST /api/cart/items", "DELETE /api/cart/items/:id"},
	"catalog":   {"GET /api/products/:id", "GET /api/products", "GET /api/categories"},
	"search":    {"GET /api/search", "GET /api/search/suggest"},
	"auth":      {"POST /api/login", "POST /api/token/refresh", "POST /api/logout"},
	"web-store": {"GET /", "GET /product/:id", "GET /cart", "GET /checkout"},
}

// ─── queries ─────────────────────────────────────────────────────

// metricQuery is one "aggr:metric{scope} by {tags}.fn()" term.
type metricQuery struct {
	aggr, metric string
	scope        map[string]string
	by           []string
	asCount      bool
	asRate       bool
}

var reMetricTerm = regexp.MustCompile(`(?:(\w+):)?([a-zA-Z_][\w.]*)\{([^}]*)\}(?:\s*by\s*\{([^}]*)\})?((?:\.\w+\([^)]*\))*)`)

func parseTerm(m []string) metricQuery {
	q := metricQuery{aggr: m[1], metric: m[2], scope: parseScope(m[3])}
	if q.aggr == "" {
		q.aggr = "avg"
	}
	for _, t := range strings.Split(m[4], ",") {
		if t = strings.TrimSpace(t); t != "" {
			q.by = append(q.by, t)
		}
	}
	q.asCount = strings.Contains(m[5], "as_count")
	q.asRate = strings.Contains(m[5], "as_rate")
	return q
}

func parseScope(s string) map[string]string {
	out := map[string]string{}
	for _, part := range strings.Split(s, ",") {
		part = strings.TrimSpace(part)
		if part == "" || part == "*" {
			continue
		}
		k, v, ok := strings.Cut(part, ":")
		if !ok {
			out[part] = ""
			continue
		}
		out[strings.TrimPrefix(k, "!")] = v
	}
	return out
}

// groupsFor lists the group tag sets a "by {…}" produces, respecting the
// scope ({service:checkout} by {service} is just checkout).
func groupsFor(by []string, scope map[string]string) [][]string {
	if len(by) == 0 {
		return [][]string{nil}
	}
	out := [][]string{nil}
	for _, tag := range by {
		values := tagValues(tag, scope)
		var next [][]string
		for _, g := range out {
			for _, v := range values {
				next = append(next, append(append([]string(nil), g...), tag+":"+v))
			}
		}
		out = next
		if len(out) > 12 {
			out = out[:12]
		}
	}
	return out
}

func tagValues(tag string, scope map[string]string) []string {
	if v, ok := scope[tag]; ok && v != "" && !strings.Contains(v, "*") {
		return []string{v}
	}
	svc := firstOf(scope, "service", "kube_deployment")
	switch tag {
	case "service", "kube_deployment", "kube_service":
		return services
	case "host":
		if scope["role"] == "db" {
			return []string{"db-primary-1", "db-replica-1", "db-replica-2"}
		}
		return []string{"ip-10-0-1-23", "ip-10-0-1-57", "ip-10-0-2-14", "ip-10-0-2-91", "ip-10-0-3-8", "ip-10-0-3-66"}
	case "pod_name", "kube_pod_name", "pod":
		if svc == "" || svc == "*" {
			svc = "checkout"
		}
		pods := []string{svc + "-7d9f4-x2k9p", svc + "-7d9f4-m4q8z", svc + "-7d9f4-h7t2c", svc + "-7d9f4-b8v3n"}
		if svc == "checkout" {
			pods = append(pods, "checkout-7d9f4-q1w5e", "checkout-7d9f4-r6y2u")
		}
		return pods
	case "resource_name", "endpoint", "http.url_details.path", "@http.url_details.path", "http.route":
		if eps, ok := endpoints[svc]; ok {
			return eps
		}
		return []string{"POST /api/checkout", "GET /api/products/:id", "GET /api/search", "GET /api/cart", "POST /api/payments/authorize"}
	case "status_code", "http.status_code", "@http.status_code":
		return []string{"200", "201", "304", "404", "429", "500", "503"}
	case "availability-zone", "availability_zone", "zone":
		return []string{"eu-west-1a", "eu-west-1b", "eu-west-1c"}
	case "region":
		return []string{"eu-west-1", "us-east-1"}
	case "env":
		return []string{"prod", "staging"}
	case "version":
		return []string{"v2.31.0", "v2.30.4"}
	case "status":
		return []string{"info", "warn", "error"}
	case "team":
		return []string{"checkout", "discovery", "platform", "payments"}
	}
	return []string{"a", "b", "c"}
}

// share is the fraction of a total that one group gets. Services and env
// come through the scope (value reads them); along any other tag the first
// values carry most of the traffic, and the shares add up to 1.
func share(group []string) float64 {
	s := 1.0
	for _, t := range group {
		k, v, _ := strings.Cut(t, ":")
		switch k {
		case "service", "kube_deployment", "kube_service", "env":
			continue
		case "status_code", "http.status_code", "@http.status_code":
			s *= statusShare[v]
			continue
		}
		list := listOf(k, v)
		norm, idx := 0.0, 0
		for j, x := range list {
			norm += 1 / math.Pow(float64(j+1), 0.9)
			if x == v {
				idx = j
			}
		}
		s *= 1 / math.Pow(float64(idx+1), 0.9) / norm
	}
	return s
}

var statusShare = map[string]float64{"200": 0.86, "201": 0.05, "304": 0.05, "404": 0.02, "429": 0.008, "500": 0.008, "503": 0.004}

// listOf finds the list of values v belongs to for a tag.
func listOf(tag, v string) []string {
	lists := [][]string{tagValues(tag, nil)}
	for _, eps := range endpoints {
		lists = append(lists, eps)
	}
	for _, l := range lists {
		for _, x := range l {
			if x == v {
				return l
			}
		}
	}
	return []string{v}
}

// ─── series ──────────────────────────────────────────────────────

// family classifies a metric name.
var metricSet = func() map[string]bool {
	m := map[string]bool{}
	for _, n := range metricNames {
		m[n] = true
	}
	return m
}()

func knownMetric(name string) bool { return metricSet[strings.ToLower(name)] }

func family(metric string) string {
	m := strings.ToLower(metric)
	switch {
	case strings.Contains(m, "apdex"):
		return "apdex"
	case strings.Contains(m, "queue.depth"):
		return "nodata"
	case strings.Contains(m, "duration"), strings.Contains(m, "latency"), strings.Contains(m, "largest_contentful"), strings.Contains(m, "index.lag"):
		return "latency"
	case strings.Contains(m, "error"), strings.Contains(m, "5xx"), strings.Contains(m, "declined"):
		return "errors"
	case strings.Contains(m, "hits"), strings.Contains(m, "request"), strings.Contains(m, "orders.placed"):
		return "traffic"
	case strings.Contains(m, "cpu"):
		return "cpu"
	case strings.Contains(m, "mem"):
		return "memory"
	case strings.Contains(m, "disk.in_use"):
		return "disk"
	case strings.Contains(m, "bytes"):
		return "network"
	case strings.Contains(m, "restarts"):
		return "restarts"
	case strings.Contains(m, "pod.ready"), strings.Contains(m, "replicas"):
		return "pods"
	case strings.Contains(m, "connections"), strings.Contains(m, "clients"):
		return "connections"
	case strings.Contains(m, "orders.value"):
		return "money"
	case strings.Contains(m, "load"):
		return "load"
	}
	return "generic"
}

var (
	unitSecond  = &datadog.QueryUnit{Family: "time", Name: "second", ShortName: "s", ScaleFactor: 1}
	unitPercent = &datadog.QueryUnit{Family: "percentage", Name: "percent", ShortName: "%"}
	unitFrac    = &datadog.QueryUnit{Family: "percentage", Name: "fraction"}
	unitBytes   = &datadog.QueryUnit{Family: "bytes", Name: "byte", ShortName: "B", ScaleFactor: 1}
	unitBps     = &datadog.QueryUnit{Family: "bytes", Name: "byte", ShortName: "B/s", ScaleFactor: 1}
	unitRate    = &datadog.QueryUnit{Family: "general", Name: "request", ShortName: "/s"}
	unitEuro    = &datadog.QueryUnit{Family: "money", Name: "euro", ShortName: "€"}
)

func unitOf(q metricQuery) *datadog.QueryUnit {
	switch family(q.metric) {
	case "latency":
		return unitSecond
	case "cpu":
		return unitPercent
	case "disk":
		return unitFrac
	case "memory":
		return unitBytes
	case "network":
		return unitBps
	case "traffic", "errors":
		if q.asCount {
			return nil
		}
		return unitRate
	case "money":
		return unitEuro
	}
	return nil
}

// value is one metric at one moment for one group; NaN means no data.
func (a *API) value(q metricQuery, group []string, t time.Time, step time.Duration) float64 {
	scope := map[string]string{}
	for k, v := range q.scope {
		scope[k] = v
	}
	for _, g := range group {
		k, v, _ := strings.Cut(g, ":")
		scope[k] = v
	}
	svc := firstOf(scope, "service", "kube_deployment")
	if svc == "" || svc == "*" { // payments.provider.errors belongs to payments
		if prefix, _, _ := strings.Cut(q.metric, "."); peakRPS[prefix] > 0 {
			svc = prefix
			scope["service"] = prefix
		}
	}
	key := q.metric + "|" + strings.Join(group, ",") + "|" + scope["service"] + scope["env"]
	inc := a.incident(t) * exposure(scope)
	n := noise(key, t)
	d := daily(t)
	env := 1.0
	if scope["env"] == "staging" {
		env = 0.04
	}
	perStep := step.Seconds()
	if perStep <= 0 {
		perStep = 60
	}

	switch family(q.metric) {
	case "nodata":
		return math.NaN()
	case "traffic":
		rps := 0.0
		if svc == "" || svc == "*" {
			for _, s := range services {
				rps += peakRPS[s]
			}
		} else {
			rps = peakRPS[svc]
			if rps == 0 {
				rps = 200
			}
		}
		if strings.Contains(q.metric, "orders") {
			rps = 2.4
		}
		v := rps * env * (0.3 + 0.7*d) * (1 + 0.07*n) * (1 - 0.15*inc) * share(group)
		if q.asCount {
			v *= perStep
		}
		return v
	case "errors":
		rps := peakRPS[svc]
		if rps == 0 {
			rps = 3000
		}
		rate := 0.002 + 0.001*(n+1) + 0.11*inc
		if strings.Contains(q.metric, "elb") { // the load balancer's own 5xx: rare
			rate = 0.00005 + 0.0005*inc
		}
		v := rps * env * (0.3 + 0.7*d) * rate * share(group)
		for _, g := range group {
			if strings.HasPrefix(g, "status_code:") || strings.HasPrefix(g, "http.status_code:") {
				if !strings.HasSuffix(g, ":500") && !strings.HasSuffix(g, ":503") && !strings.HasSuffix(g, ":502") {
					return 0
				}
			}
		}
		if q.asCount {
			v *= perStep
		}
		return v
	case "latency":
		base := p50[svc]
		if base == 0 {
			base = 0.07
		}
		if strings.Contains(q.metric, "largest_contentful") {
			base = 1.6
		}
		if strings.Contains(q.metric, "index.lag") {
			base = 3
		}
		mult := map[string]float64{"p50": 1, "p75": 1.4, "p90": 2, "p95": 2.6, "p99": 4.5, "max": 7, "min": 0.3}[q.aggr]
		if mult == 0 {
			mult = 1.15
		}
		for _, g := range group { // some endpoints are just slower
			if strings.Contains(g, "authorize") || strings.Contains(g, "POST /api/checkout") {
				mult *= 1.4
			}
		}
		return base * mult * (1 + 0.12*n) * (1 + 0.25*d) * (1 + 6.5*inc)
	case "apdex":
		return math.Min(1, 0.975+0.01*n-0.19*inc)
	case "cpu":
		v := 22 + 30*d + 6*n + 28*inc
		if strings.Contains(q.metric, "idle") {
			v = 100 - v
		}
		return math.Max(1, math.Min(99, v))
	case "memory":
		saw := math.Mod(float64(t.Unix())/10800+hash01(key, 0), 1)
		return (1.1 + 0.45*saw + 0.05*n + 0.2*inc) * (1 << 30)
	case "disk":
		return 0.42 + 0.25*hash01(key, 1) + 0.01*n + 0.02*float64(t.Unix()%86400)/86400
	case "network":
		return (4 + 30*d + 3*n) * 1e6 * (0.5 + hash01(key, 2))
	case "restarts":
		if svc == "cart" || strings.Contains(strings.Join(group, ","), "cart") {
			return math.Floor(4 * math.Min(1, math.Max(0, float64(t.Sub(a.start.Add(-4*time.Hour)))/float64(3*time.Hour))))
		}
		return 0
	case "pods":
		pods := 6.0
		if svc == "checkout" && a.incident(t) > 0 && t.After(a.start.Add(-22*time.Minute)) {
			pods = 9
		}
		return pods
	case "connections":
		return 80 + 35*d + 6*n + 30*inc
	case "money":
		return 71 + 6*n
	case "load":
		return 0.6 + 1.6*d + 0.3*n + inc
	}
	return 50 + 25*d + 10*n
}

// series evaluates a metric query over [from, to) at one point per step.
func (a *API) series(q metricQuery, from, to time.Time, step time.Duration) (times []int64, out []namedSeries) {
	for t := from; t.Before(to); t = t.Add(step) {
		times = append(times, t.UnixMilli())
	}
	if !knownMetric(q.metric) {
		return times, nil // like Datadog: a metric nobody sends has no series
	}
	for _, g := range groupsFor(q.by, q.scope) {
		vals := make([]float64, len(times))
		for i, ms := range times {
			vals[i] = a.value(q, g, time.UnixMilli(ms), step)
			// Pods that only exist after the autoscale have no data before.
			if len(g) == 1 && (strings.HasSuffix(g[0], "q1w5e") || strings.HasSuffix(g[0], "r6y2u")) &&
				time.UnixMilli(ms).Before(a.start.Add(-22*time.Minute)) {
				vals[i] = math.NaN()
			}
		}
		out = append(out, namedSeries{tags: g, values: vals})
	}
	return times, out
}

type namedSeries struct {
	tags   []string
	values []float64
}

// reduce aggregates a series to one number the way Datadog's scalar
// aggregators do.
func reduce(vals []float64, aggr string) float64 {
	var clean []float64
	for _, v := range vals {
		if !math.IsNaN(v) {
			clean = append(clean, v)
		}
	}
	if len(clean) == 0 {
		return math.NaN()
	}
	switch aggr {
	case "sum":
		s := 0.0
		for _, v := range clean {
			s += v
		}
		return s
	case "last":
		return clean[len(clean)-1]
	case "max":
		sort.Float64s(clean)
		return clean[len(clean)-1]
	case "min":
		sort.Float64s(clean)
		return clean[0]
	}
	s := 0.0
	for _, v := range clean {
		s += v
	}
	return s / float64(len(clean))
}
