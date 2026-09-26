package demo

import (
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/ngavilan-dogfy/datadog-cli/datadog"
)

// Logs: counts per minute follow the same story as the metrics (errors on
// checkout and payments climb with the incident), and each minute's lines
// are generated from templates, stable for that minute.

// logRate is log lines per minute for a service and status. Info volume is
// sampled: a real store logs far more, but a demo only shows a page.
func (a *API) logRate(svc, status string, t time.Time) float64 {
	inc := a.incident(t)
	d := daily(t)
	switch status {
	case "error":
		base := map[string]float64{"checkout": 5, "payments": 1.5, "auth": 0.7, "cart": 0.4, "search": 0.3, "web-store": 1.1, "catalog": 0.2}[svc]
		switch svc {
		case "checkout":
			base += 40 * inc
		case "payments":
			base += 40 * inc
		case "cart":
			base += 3 * inc
		}
		return base * (0.85 + 0.15*d)
	case "warn":
		base := map[string]float64{"checkout": 3, "payments": 2, "auth": 2, "cart": 1.5, "search": 4, "web-store": 5, "catalog": 1}[svc]
		if svc == "payments" {
			base += 25 * inc // retries
		}
		return base * (0.5 + 0.5*d)
	}
	return peakRPS[svc] / 25 * (0.3 + 0.7*d)
}

var statuses = []string{"error", "warn", "info"}

// logFilter is a parsed log search: facets must match, free text must
// appear in the message.
type logFilter struct {
	facets map[string][]string // key → accepted values (any)
	not    map[string][]string
	text   []string
}

func parseLogQuery(q string) logFilter {
	f := logFilter{facets: map[string][]string{}, not: map[string][]string{}}
	for _, tok := range splitQuery(q) {
		neg := strings.HasPrefix(tok, "-")
		tok = strings.TrimPrefix(tok, "-")
		switch strings.ToUpper(tok) {
		case "", "*", "AND", "OR", "NOT":
			continue
		}
		tok = strings.Trim(tok, "()")
		if k, v, ok := strings.Cut(tok, ":"); ok && k != "" && !strings.Contains(k, " ") {
			v = strings.Trim(v, `"`)
			if neg {
				f.not[k] = append(f.not[k], strings.ToLower(v))
			} else {
				f.facets[k] = append(f.facets[k], strings.ToLower(v))
			}
			continue
		}
		if !neg {
			f.text = append(f.text, strings.ToLower(strings.Trim(tok, `"`)))
		}
	}
	return f
}

// splitQuery splits on spaces outside quotes.
func splitQuery(q string) []string {
	var out []string
	var b strings.Builder
	quoted := false
	for _, r := range q {
		switch {
		case r == '"':
			quoted = !quoted
			b.WriteRune(r)
		case r == ' ' && !quoted:
			if b.Len() > 0 {
				out = append(out, b.String())
				b.Reset()
			}
		default:
			b.WriteRune(r)
		}
	}
	if b.Len() > 0 {
		out = append(out, b.String())
	}
	return out
}

func globMatch(pattern, v string) bool {
	switch {
	case pattern == "*":
		return true
	case strings.HasPrefix(pattern, "*") && strings.HasSuffix(pattern, "*"):
		return strings.Contains(v, strings.Trim(pattern, "*"))
	case strings.HasSuffix(pattern, "*"):
		return strings.HasPrefix(v, strings.TrimSuffix(pattern, "*"))
	case strings.HasPrefix(pattern, "*"):
		return strings.HasSuffix(v, strings.TrimPrefix(pattern, "*"))
	case strings.HasPrefix(pattern, ">="), strings.HasPrefix(pattern, "<="), strings.HasPrefix(pattern, ">"), strings.HasPrefix(pattern, "<"):
		op := strings.TrimRight(pattern[:2], "0123456789.")
		n, err1 := strconv.ParseFloat(strings.TrimPrefix(pattern, op), 64)
		x, err2 := strconv.ParseFloat(v, 64)
		if err1 != nil || err2 != nil {
			return false
		}
		switch op {
		case ">=":
			return x >= n
		case "<=":
			return x <= n
		case ">":
			return x > n
		}
		return x < n
	}
	return pattern == v
}

// field reads a facet from a log: reserved attributes, tags or @attributes.
func field(l *datadog.LogData, key string) string {
	at := l.Attributes
	switch key {
	case "service":
		return at.Service
	case "status":
		return at.Status
	case "host":
		return at.Host
	}
	if strings.HasPrefix(key, "@") {
		var cur any = at.Attributes
		for _, part := range strings.Split(key[1:], ".") {
			m, ok := cur.(map[string]any)
			if !ok {
				return ""
			}
			cur = m[part]
		}
		switch v := cur.(type) {
		case string:
			return strings.ToLower(v)
		case float64:
			return strconv.FormatFloat(v, 'f', -1, 64)
		}
		return ""
	}
	for _, t := range at.Tags {
		if k, v, ok := strings.Cut(t, ":"); ok && k == key {
			return strings.ToLower(v)
		}
	}
	return ""
}

func (f logFilter) match(l *datadog.LogData) bool {
	for k, vals := range f.facets {
		got := field(l, k)
		ok := false
		for _, v := range vals {
			if globMatch(v, got) || (k == "status" && v == "warning" && got == "warn") {
				ok = true
			}
		}
		if !ok {
			return false
		}
	}
	for k, vals := range f.not {
		got := field(l, k)
		for _, v := range vals {
			if globMatch(v, got) {
				return false
			}
		}
	}
	msg := strings.ToLower(l.Attributes.Message)
	for _, t := range f.text {
		if !strings.Contains(msg, strings.Trim(t, "*")) {
			return false
		}
	}
	return true
}

// allows reports whether the filter lets a service/status through, for
// counting without generating every line.
func (f logFilter) allows(svc, status string) (bool, float64) {
	probe := &datadog.LogData{Attributes: datadog.LogAttributes{Service: svc, Status: status, Tags: []string{"env:prod"}}}
	for k, vals := range f.facets {
		switch k {
		case "service", "status", "env":
			got := field(probe, k)
			ok := false
			for _, v := range vals {
				if globMatch(v, got) || (k == "status" && v == "warning" && got == "warn") {
					ok = true
				}
			}
			if !ok {
				return false, 0
			}
		}
	}
	for k, vals := range f.not {
		switch k {
		case "service", "status", "env":
			for _, v := range vals {
				if globMatch(v, field(probe, k)) {
					return false, 0
				}
			}
		}
	}
	// Other facets and free text narrow things down; guess how much.
	frac := 1.0
	for k := range f.facets {
		if k != "service" && k != "status" && k != "env" {
			frac *= 0.3
		}
	}
	for range f.text {
		frac *= 0.15
	}
	return true, frac
}

// ─── lines ───────────────────────────────────────────────────────

type logLine struct {
	msg   string
	attrs func(r float64) map[string]any
}

func httpAttrs(method, path string, code int, ms float64) map[string]any {
	return map[string]any{
		"http":     map[string]any{"method": method, "status_code": float64(code), "url_details": map[string]any{"path": path}},
		"duration": math.Round(ms * 1e6), // nanoseconds, like the tracers send
		"network":  map[string]any{"client": map[string]any{"ip": "203.0.113.24"}},
	}
}

func withError(m map[string]any, kind, message, stack string) map[string]any {
	e := map[string]any{"kind": kind, "message": message}
	if stack != "" {
		e["stack"] = stack
	}
	m["error"] = e
	return m
}

var lines = map[string][]logLine{
	"checkout/error": {
		{"Payment authorization timed out after 5000ms", func(r float64) map[string]any {
			return withError(httpAttrs("POST", "/api/checkout", 504, 5000+300*r), "TimeoutError", "payments.authorize timed out after 5000ms",
				"TimeoutError: payments.authorize timed out after 5000ms\n    at PaymentsClient.authorize (src/payments/client.ts:88:11)\n    at CheckoutService.placeOrder (src/checkout/service.ts:142:25)\n    at processTicksAndRejections (node:internal/process/task_queues:95:5)")
		}},
		{"Could not place order: upstream payments returned 503", func(r float64) map[string]any {
			return withError(httpAttrs("POST", "/api/checkout", 502, 820+400*r), "UpstreamError", "payments responded 503 Service Unavailable",
				"UpstreamError: payments responded 503 Service Unavailable\n    at PaymentsClient.request (src/payments/client.ts:51:13)\n    at CheckoutService.placeOrder (src/checkout/service.ts:142:25)")
		}},
	},
	"checkout/warn": {
		{"Slow checkout: 2.4s to price the cart", func(r float64) map[string]any { return httpAttrs("GET", "/api/checkout/summary", 200, 2400+500*r) }},
		{"Coupon SPRING10 expired, ignoring it", func(r float64) map[string]any { return httpAttrs("POST", "/api/checkout/coupon", 422, 40+20*r) }},
	},
	"checkout/info": {
		{"Order placed", func(r float64) map[string]any {
			m := httpAttrs("POST", "/api/checkout", 201, 180+120*r)
			m["order"] = map[string]any{"id": fmt.Sprintf("ORD-%05d", int(r*99999)), "items": float64(1 + int(r*5)), "total": math.Round((20+r*140)*100) / 100}
			return m
		}},
		{"Shipping options computed", func(r float64) map[string]any { return httpAttrs("GET", "/api/checkout/shipping", 200, 60+40*r) }},
	},
	"payments/error": {
		{"Provider responded 503 Service Unavailable", func(r float64) map[string]any {
			return withError(httpAttrs("POST", "/api/payments/authorize", 503, 3000+2000*r), "ProviderUnavailable", "acquirer returned 503",
				"ProviderUnavailable: acquirer returned 503\n    at Acquirer.authorize (src/acquirer.go:212)\n    at Authorize (src/handlers/authorize.go:64)")
		}},
		{"Authorization declined: issuer unavailable", func(r float64) map[string]any {
			return withError(httpAttrs("POST", "/api/payments/authorize", 402, 900+300*r), "CardDeclined", "issuer_unavailable", "")
		}},
	},
	"payments/warn": {
		{"Retrying authorization (attempt 2 of 3)", func(r float64) map[string]any { return httpAttrs("POST", "/api/payments/authorize", 200, 2100+900*r) }},
	},
	"payments/info": {
		{"Payment captured", func(r float64) map[string]any { return httpAttrs("POST", "/api/payments/capture", 200, 120+80*r) }},
		{"Payment authorized", func(r float64) map[string]any { return httpAttrs("POST", "/api/payments/authorize", 200, 380+200*r) }},
	},
	"cart/error": {
		{"Redis command timed out", func(r float64) map[string]any {
			return withError(httpAttrs("POST", "/api/cart/items", 500, 1000+100*r), "RedisTimeoutError", "Command timed out after 1000ms",
				"RedisTimeoutError: Command timed out after 1000ms\n    at CartStore.save (src/store.ts:37:9)")
		}},
	},
	"cart/warn": {
		{"Cart has items that are out of stock", func(r float64) map[string]any { return httpAttrs("GET", "/api/cart", 200, 25+15*r) }},
	},
	"cart/info": {
		{"Item added to cart", func(r float64) map[string]any { return httpAttrs("POST", "/api/cart/items", 201, 18+12*r) }},
		{"Cart fetched", func(r float64) map[string]any { return httpAttrs("GET", "/api/cart", 200, 9+8*r) }},
	},
	"auth/error": {
		{"Could not verify refresh token", func(r float64) map[string]any {
			m := withError(httpAttrs("POST", "/api/token/refresh", 401, 12+8*r), "TokenExpiredError", "jwt expired",
				"TokenExpiredError: jwt expired\n    at verify (node_modules/jsonwebtoken/verify.js:190:21)\n    at refresh (src/tokens.ts:58:14)")
			m["evt"] = map[string]any{"outcome": "failure"}
			return m
		}},
	},
	"auth/warn": {
		{"Login failed: wrong password", func(r float64) map[string]any {
			m := httpAttrs("POST", "/api/login", 401, 90+40*r)
			m["evt"] = map[string]any{"outcome": "failure"}
			return m
		}},
	},
	"auth/info": {
		{"Login succeeded", func(r float64) map[string]any {
			m := httpAttrs("POST", "/api/login", 200, 110+60*r)
			m["evt"] = map[string]any{"outcome": "success"}
			return m
		}},
	},
	"search/error": {
		{"Search cluster rejected the query: too many clauses", func(r float64) map[string]any {
			return withError(httpAttrs("GET", "/api/search", 500, 40+20*r), "SearchError", "maxClauseCount is set to 1024", "")
		}},
	},
	"search/warn": {
		{"Slow query: \"running shoes\" took 640ms", func(r float64) map[string]any { return httpAttrs("GET", "/api/search", 200, 600+200*r) }},
	},
	"search/info": {
		{"Search served", func(r float64) map[string]any { return httpAttrs("GET", "/api/search", 200, 90+60*r) }},
	},
	"catalog/error": {
		{"Product 88213 has no price in EUR", func(r float64) map[string]any {
			return withError(httpAttrs("GET", "/api/products/88213", 500, 30+10*r), "PricingError", "no price for currency EUR", "")
		}},
	},
	"catalog/warn": {
		{"Image missing for product 51042", func(r float64) map[string]any { return httpAttrs("GET", "/api/products/51042", 200, 20+10*r) }},
	},
	"catalog/info": {
		{"Product page served", func(r float64) map[string]any { return httpAttrs("GET", "/api/products/:id", 200, 22+15*r) }},
	},
	"web-store/error": {
		{"Uncaught TypeError: Cannot read properties of undefined (reading 'price')", func(r float64) map[string]any {
			return withError(map[string]any{"view": map[string]any{"url": "https://shop.example.com/product/51042"}}, "TypeError",
				"Cannot read properties of undefined (reading 'price')",
				"TypeError: Cannot read properties of undefined (reading 'price')\n    at ProductCard (webpack:///src/components/ProductCard.tsx:41:23)")
		}},
	},
	"web-store/warn": {
		{"Largest contentful paint took 3.1s on /product/:id", func(r float64) map[string]any {
			return map[string]any{"view": map[string]any{"url": "https://shop.example.com/product/51042", "largest_contentful_paint": (2.8 + r) * 1e9}}
		}},
	},
	"web-store/info": {
		{"Page served", func(r float64) map[string]any { return httpAttrs("GET", "/product/:id", 200, 40+30*r) }},
	},
}

func hostOf(r float64) string {
	hosts := []string{"ip-10-0-1-23", "ip-10-0-1-57", "ip-10-0-2-14", "ip-10-0-2-91", "ip-10-0-3-8", "ip-10-0-3-66"}
	return hosts[int(r*float64(len(hosts)))%len(hosts)]
}

// minuteLogs builds the lines of one minute, newest first.
func (a *API) minuteLogs(minute time.Time) []datadog.LogData {
	var out []datadog.LogData
	for _, svc := range services {
		for _, st := range statuses {
			rate := a.logRate(svc, st, minute)
			key := svc + "/" + st
			n := int(rate)
			if hash01(key, minute.Unix()) < rate-float64(n) {
				n++
			}
			tmpl := lines[key]
			if len(tmpl) == 0 {
				continue
			}
			for i := 0; i < n; i++ {
				r := hash01(key+strconv.Itoa(i), minute.Unix())
				r2 := hash01(key+"~"+strconv.Itoa(i), minute.Unix())
				line := tmpl[int(r*float64(len(tmpl)))%len(tmpl)]
				ts := minute.Add(time.Duration(r2 * float64(time.Minute)))
				pod := svc + "-7d9f4-" + []string{"x2k9p", "m4q8z", "h7t2c", "b8v3n"}[int(r2*4)%4]
				attrs := line.attrs(r2)
				attrs["env"] = "prod"
				attrs["trace_id"] = strconv.FormatUint(uint64(r*1e18), 10)
				if svc != "web-store" {
					attrs["usr"] = map[string]any{"id": fmt.Sprintf("u_%05d", int(r2*99999))}
				}
				out = append(out, datadog.LogData{
					ID:   fmt.Sprintf("AQAAAZ%011d%04d", minute.Unix(), i) + strings.ToUpper(key[:2]),
					Type: "log",
					Attributes: datadog.LogAttributes{
						Timestamp: ts.UTC().Format(time.RFC3339Nano), Service: svc, Status: st,
						Host: hostOf(r2), Message: line.msg,
						Tags:       []string{"env:prod", "service:" + svc, "kube_deployment:" + svc, "pod_name:" + pod, "version:" + versionOf(svc)},
						Attributes: attrs,
					},
				})
			}
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Attributes.Timestamp > out[j].Attributes.Timestamp })
	return out
}

func versionOf(svc string) string {
	switch svc {
	case "checkout":
		return "v2.31.0"
	case "search":
		return "v1.18.2"
	case "web-store":
		return "v5.4.0"
	}
	return "v1.9.3"
}

// parseTime reads the times the log APIs take: epoch milliseconds, RFC 3339
// or "now" / "now-15m".
func parseTime(s string, now time.Time) time.Time {
	s = strings.TrimSpace(s)
	if s == "" || s == "now" {
		return now
	}
	if ms, err := strconv.ParseInt(s, 10, 64); err == nil {
		return time.UnixMilli(ms)
	}
	if strings.HasPrefix(s, "now-") {
		spec := s[4:]
		unit := map[byte]time.Duration{'s': time.Second, 'm': time.Minute, 'h': time.Hour, 'd': 24 * time.Hour, 'w': 7 * 24 * time.Hour}[spec[len(spec)-1]]
		if n, err := strconv.Atoi(spec[:len(spec)-1]); err == nil && unit > 0 {
			return now.Add(-time.Duration(n) * unit)
		}
	}
	if t, err := time.Parse(time.RFC3339Nano, s); err == nil {
		return t
	}
	return now
}

// searchLogs returns up to limit lines matching q in [from, to), newest
// first, starting before the cursor (a timestamp) when there is one.
func (a *API) searchLogs(q string, from, to time.Time, limit int, cursor string) ([]datadog.LogData, string) {
	f := parseLogQuery(q)
	if cursor != "" {
		if ms, err := strconv.ParseInt(cursor, 10, 64); err == nil {
			to = time.UnixMilli(ms)
		}
	}
	// Look back at most two days: plenty for a demo, and it stays fast.
	if from.Before(to.Add(-48 * time.Hour)) {
		from = to.Add(-48 * time.Hour)
	}
	var out []datadog.LogData
	for m := to.Truncate(time.Minute); !m.Before(from.Truncate(time.Minute)) && len(out) < limit; m = m.Add(-time.Minute) {
		for _, l := range a.minuteLogs(m) {
			ts, _ := time.Parse(time.RFC3339Nano, l.Attributes.Timestamp)
			if !ts.Before(to) || ts.Before(from) || !f.match(&l) {
				continue
			}
			out = append(out, l)
			if len(out) == limit {
				break
			}
		}
	}
	next := ""
	if len(out) == limit {
		last, _ := time.Parse(time.RFC3339Nano, out[len(out)-1].Attributes.Timestamp)
		next = strconv.FormatInt(last.UnixMilli(), 10)
	}
	return out, next
}

// logCounts counts matching lines per step, grouped by facets.
func (a *API) logCounts(q string, groupBy []string, times []int64, step time.Duration) set {
	f := parseLogQuery(q)
	out := set{byKey: map[string]namedSeries{}}
	add := func(tags []string, i int, v float64) {
		k := strings.Join(tags, ",")
		s, ok := out.byKey[k]
		if !ok {
			s = namedSeries{tags: tags, values: make([]float64, len(times))}
			out.order = append(out.order, k)
		}
		s.values[i] += v
		out.byKey[k] = s
	}
	perMin := step.Minutes()
	for i, ms := range times {
		t := time.UnixMilli(ms)
		for _, svc := range services {
			for _, st := range statuses {
				ok, frac := f.allows(svc, st)
				if !ok {
					continue
				}
				wobble := 0.15
				if st == "error" {
					wobble = 0.06
				}
				v := math.Round(a.logRate(svc, st, t) * perMin * frac * (1 + wobble*noise(svc+st, t)))
				var tags []string
				for _, g := range groupBy {
					switch strings.TrimPrefix(g, "@") {
					case "service":
						tags = append(tags, "service:"+svc)
					case "status":
						tags = append(tags, "status:"+st)
					case "env":
						tags = append(tags, "env:prod")
					default:
						// Spread over the facet's usual values.
						vals := tagValues(strings.TrimPrefix(g, "@"), map[string]string{"service": svc})
						v /= float64(len(vals))
						tags = append(tags, g+":"+vals[int(hash01(g, t.Unix()/600)*float64(len(vals)))%len(vals)])
					}
				}
				add(tags, i, v)
			}
		}
	}
	if len(out.order) == 0 {
		out = single(nil, make([]float64, len(times)))
	}
	// Biggest groups first, like the API.
	sort.SliceStable(out.order, func(i, j int) bool {
		return sum(out.byKey[out.order[i]].values) > sum(out.byKey[out.order[j]].values)
	})
	return out
}

func sum(vals []float64) float64 {
	s := 0.0
	for _, v := range vals {
		if !math.IsNaN(v) {
			s += v
		}
	}
	return s
}
