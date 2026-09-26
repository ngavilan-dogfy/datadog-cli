package cmd

import (
	"strings"
	"testing"
	"time"

	"github.com/ngavilan-dogfy/datadog-cli/datadog"
)

var t0 = time.Date(2026, 5, 19, 10, 0, 0, 0, time.UTC)

// span makes a span starting at ms after t0 and lasting dur ms.
func span(id, parent, service, op, resource string, at, dur float64, err bool) datadog.SpanData {
	start := t0.Add(time.Duration(at * float64(time.Millisecond)))
	status := "ok"
	if err {
		status = "error"
	}
	return datadog.SpanData{Attributes: datadog.SpanAttributes{
		SpanID: id, ParentID: parent, Service: service, OperationName: op, ResourceName: resource, Status: status,
		StartTimestamp: start.Format(time.RFC3339Nano),
		EndTimestamp:   start.Add(time.Duration(dur * float64(time.Millisecond))).Format(time.RFC3339Nano),
		Custom:         map[string]interface{}{"duration": dur * 1e6},
	}}
}

func byName(rep *traceReport) map[string]*traceSpan {
	m := map[string]*traceSpan{}
	for _, s := range rep.Spans {
		m[s.SpanID] = s
	}
	return m
}

func TestTraceTreeSelfTimeAndCriticalPath(t *testing.T) {
	// web (0–100) calls auth (5–25), then db (30–60) and cache (30–40) in
	// parallel, then fires an async email (90–150) it doesn't wait for.
	rep := assembleTrace("1", []datadog.SpanData{
		span("db", "web", "api", "mongodb.query", "find orders", 30, 30, false),
		span("web", "0", "api", "fastify.request", "GET /orders", 0, 100, false),
		span("auth", "web", "auth", "http.request", "POST /verify", 5, 20, false),
		span("cache", "web", "api", "redis.command", "GET", 30, 10, false),
		span("email", "web", "api", "http.request", "POST", 90, 60, true),
	})
	s := byName(rep)
	if rep.Partial || len(rep.roots) != 1 || rep.roots[0].SpanID != "web" {
		t.Fatalf("want one root web, got partial=%v roots=%d", rep.Partial, len(rep.roots))
	}
	if rep.Duration != 150 {
		t.Errorf("duration = %v, want 150 (async email outlasts the request)", rep.Duration)
	}
	// web waits on auth 5–25, db/cache 30–60, email 90–100 → busy 60 → own 40.
	if got := s["web"].Self; got != 40 {
		t.Errorf("web self = %v, want 40", got)
	}
	for id, want := range map[string]bool{"web": true, "auth": true, "db": true, "cache": false, "email": false} {
		if s[id].Critical != want {
			t.Errorf("%s critical = %v, want %v", id, s[id].Critical, want)
		}
	}
	if s["db"].Depth != 1 || rep.Spans[0].SpanID != "web" {
		t.Errorf("depth-first order broken: %v first, db depth %d", rep.Spans[0].SpanID, s["db"].Depth)
	}
	if rep.Errors != 1 {
		t.Errorf("errors = %d, want 1", rep.Errors)
	}
}

func TestTracePartialAndRepeats(t *testing.T) {
	var raw []datadog.SpanData
	// The request's root wasn't indexed: the handler's parent is missing.
	raw = append(raw, span("h", "missing", "api", "fastify.request", "POST /leads", 0, 50, false))
	for i := 0; i < 6; i++ {
		raw = append(raw, span(string(rune('a'+i)), "h", "api", "mongodb.query", "find customers", float64(1+i*5), 4, false))
	}
	rep := assembleTrace("1", raw)
	if !rep.Partial || !rep.roots[0].Orphan {
		t.Errorf("a missing parent must make the trace partial")
	}
	if len(rep.Repeats) != 1 || rep.Repeats[0].Count != 6 || rep.Repeats[0].TotalMS != 24 {
		t.Fatalf("repeats = %+v, want find customers ×6 = 24ms", rep.Repeats)
	}
	rows := rep.rows()
	if len(rows) != 2 || rows[1].n != 6 {
		t.Fatalf("want the 6 identical leaves folded into one row, got %d rows", len(rows))
	}
	out := rep.markdown(0)
	for _, want := range []string{"partial", "×6", "Repeated calls", "find customers ×6 = 24ms"} {
		if !strings.Contains(out, want) {
			t.Errorf("markdown lacks %q:\n%s", want, out)
		}
	}
}

func TestTraceErrorsDeepestFirst(t *testing.T) {
	rep := assembleTrace("1", []datadog.SpanData{
		span("web", "0", "api", "fastify.request", "GET /x", 0, 30, true),
		span("call", "web", "billing", "http.request", "GET /invoice", 5, 20, true),
		span("db", "call", "billing", "postgres.query", "SELECT invoices", 6, 10, true),
	})
	out := rep.markdown(0)
	i := strings.Index(out, "Errors")
	if i < 0 || !strings.HasPrefix(out[i:][strings.Index(out[i:], "\n")+1:], "  billing SELECT invoices") {
		t.Errorf("the deepest error should come first:\n%s", out[i:])
	}
}

func TestTraceIDFromArg(t *testing.T) {
	for in, want := range map[string]string{
		"8202096045573558039":              "8202096045573558039",
		"68F2A1C40000000071D3B2E59A0C4F17": "68f2a1c40000000071d3b2e59a0c4f17",
		"71d3b2e59a0c4f17":                 "8202096045573558039",
		"0x71d3b2e59a0c4f17":               "8202096045573558039",
		"https://app.datadoghq.eu/apm/trace/8202096045573558039?spanID=1":      "8202096045573558039",
		"https://app.datadoghq.eu/apm/traces?query=x&traceID=71d3b2e59a0c4f17": "8202096045573558039",
	} {
		got, err := traceIDFromArg(in)
		if err != nil || got != want {
			t.Errorf("traceIDFromArg(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	for _, bad := range []string{"", "trace", "12ab-34"} {
		if _, err := traceIDFromArg(bad); err == nil {
			t.Errorf("traceIDFromArg(%q) should fail", bad)
		}
	}
}
