package cmd

import (
	"fmt"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/ngavilan-dogfy/datadog-cli/datadog"
	"github.com/ngavilan-dogfy/datadog-cli/internal/series"

	"github.com/spf13/cobra"
)

// investigate answers "what happened to X, and why?" in one call. It
// gathers facts (this file: the probes, which only read), then analyzes
// them (investigate_analysis.go: pure functions over the facts, tested with
// made-up ones) and writes a report with references
// (investigate_report.go).

var (
	invWin       windowFlags
	invCompare   string
	invEnv       string
	invResource  string
	invQuestion  string
	invJSON      bool
	invMD        bool
	invDumpFacts bool
)

var investigateCmd = &cobra.Command{
	Use:   "investigate [service | words…]",
	Short: "Investigate a service: what changed, when it started, and the likely why — with references",
	Long: `Answer "what happened, and why?" for a service (or an endpoint of one) over a
window, in one call. It compares the window with the same window earlier
(--compare, 1d by default) and correlates everything it can read:

  golden signals   requests, errors, error rate and p95 latency — and when
                   each one changed (the onset)
  endpoints        which resources the errors and the slowness come from
  dependencies     what the service calls (databases, HTTP, queues): their
                   errors and latency
  changes          new instances or versions (deploys, restarts), change
                   events, Datadog configuration changes, CI pipelines
  logs             error log patterns: the new ones, and the ones that grew
  process, hosts   event loop, GC, CPU and memory where it runs
  alerting         monitor transitions, open incidents, SLOs

The report leads with a summary, then a timeline, ranked leads (hypotheses
with the evidence for and against), every finding, what was checked and
looked normal, what couldn't be checked, suggested improvements (a monitor
that would have caught it, one that fired late) and the next commands to
dig. Every claim cites a reference: a Datadog link to the exact view, which
'datadog read' reads back.

The argument is a service name, or words ("checkout", "orders") resolved
with 'datadog find'. Times can be spoken: --around "yesterday 18:00".

Output:
  Terminal: the report · --md: markdown to paste in a ticket or a chat ·
  --json: facts, findings, leads and references for agents

Examples:
  datadog investigate api --since 2h
  datadog investigate checkout --around "today 09:40" --window 1h
  datadog investigate api --resource "POST /v1/orders" --since 6h --md
  datadog investigate api --since 1d --compare 7d --json
  datadog investigate api --question "why were orders failing this morning?" --since "today 07:00" --md`,
	Args: cobra.ArbitraryArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		from, to, err := invWin.resolve()
		if err != nil {
			return err
		}
		compare, err := compareOffset(invCompare, to.Sub(from))
		if err != nil {
			return err
		}
		target := strings.TrimSpace(strings.Join(args, " "))
		if target == "" && invResource == "" {
			return fmt.Errorf("say what to investigate: a service (datadog investigate api) or words (datadog investigate checkout) — 'datadog find' lists what exists")
		}
		var facts *invFacts
		work := func() (err error) {
			facts, err = gatherInvestigation(target, invResource, invEnv, invQuestion, from, to, compare)
			return err
		}
		if isTTY() && !invJSON && !invMD && !invDumpFacts {
			err = withSpinner("Investigating: signals, endpoints, dependencies, changes, logs, alerts", work)
		} else {
			err = work()
		}
		if err != nil {
			return err
		}
		if invDumpFacts {
			return printJSON(facts)
		}
		rep := analyzeInvestigation(facts)
		switch {
		case invJSON:
			return printJSON(rep)
		case invMD:
			fmt.Print(rep.markdown())
			return nil
		}
		fmt.Print(rep.text(isTTY()))
		return nil
	},
}

func init() {
	invWin.register(investigateCmd, time.Hour)
	investigateCmd.Flags().StringVar(&invCompare, "compare", "", "Baseline: the same window this long before (1d, 7d; default 1d, or 7d for windows over a day)")
	investigateCmd.Flags().StringVar(&invEnv, "env", "", "Environment (default: the production one)")
	investigateCmd.Flags().StringVar(&invResource, "resource", "", "Focus on one endpoint (\"POST /v1/orders\")")
	investigateCmd.Flags().StringVar(&invQuestion, "question", "", "The question being answered, quoted at the top of the report")
	investigateCmd.Flags().BoolVar(&invJSON, "json", false, "Output as JSON")
	investigateCmd.Flags().BoolVar(&invMD, "md", false, "Output as markdown")
	investigateCmd.Flags().BoolVar(&invDumpFacts, "facts", false, "Output the raw facts gathered, before analysis (JSON)")
	_ = investigateCmd.Flags().MarkHidden("facts")
	rootCmd.AddCommand(investigateCmd)
}

// compareOffset is how far back the baseline window is.
func compareOffset(flag string, window time.Duration) (time.Duration, error) {
	if flag != "" {
		d, err := parseDuration(flag)
		if err != nil {
			return 0, fmt.Errorf("--compare: %w", err)
		}
		if d < window {
			return 0, fmt.Errorf("--compare %s is shorter than the window (%s): the two would overlap", flag, fmtDuration(window))
		}
		return d, nil
	}
	if window > 24*time.Hour {
		return 7 * 24 * time.Hour, nil
	}
	return 24 * time.Hour, nil
}

// ─── facts ───────────────────────────────────────────────────────

// invSeries is one signal over the window, and over the baseline window.
type invSeries struct {
	Query  string         `json:"query"`
	Points []series.Point `json:"points"`
	Base   []series.Point `json:"base"`
}

// invGroup compares one group (an endpoint, an operation, a peer, a host)
// between the window and the baseline.
type invGroup struct {
	Name       string        `json:"name"`
	Count      float64       `json:"count"`
	Errors     float64       `json:"errors"`
	P95        time.Duration `json:"p95,omitempty"`
	BaseCount  float64       `json:"base_count"`
	BaseErrors float64       `json:"base_errors"`
	BaseP95    time.Duration `json:"base_p95,omitempty"`
	FirstSeen  time.Time     `json:"first_seen,omitempty"` // for new hosts and versions
	// P95Series is its p95 over the window (seconds), to see whether it
	// spiked when the service did.
	P95Series []series.Point `json:"p95_series,omitempty"`
}

// invVital is one vital sign of a service's process or host.
type invVital struct {
	Name   string         `json:"name"` // "CPU aef-default-1-8hw2", "event loop delay"
	Query  string         `json:"query"`
	Unit   string         `json:"unit,omitempty"` // percent, fraction, nanosecond…
	Points []series.Point `json:"points"`
	Base   []series.Point `json:"base,omitempty"`
}

type invServiceFacts struct {
	Name     string `json:"name"`
	Env      string `json:"env,omitempty"`
	Entry    string `json:"entry_operation,omitempty"`
	Resource string `json:"resource,omitempty"` // the endpoint in focus

	Hits   *invSeries `json:"hits,omitempty"`
	Errors *invSeries `json:"errors,omitempty"`
	P95    *invSeries `json:"p95,omitempty"`
	// Before are the same three signals over the hours before the window
	// (with it): to date a trouble that had started before it.
	Before map[string][]series.Point `json:"before,omitempty"`
	// Vitals are the process and its hosts: CPU, memory, runtime metrics
	// (event loop, garbage collection) — over the window and the baseline.
	Vitals []invVital `json:"vitals,omitempty"`
	// Unit of P95's values: Datadog's trace metrics are in seconds.
	LatencyUnit string `json:"latency_unit,omitempty"`

	Resources  []invGroup `json:"resources,omitempty"`
	Operations []invGroup `json:"operations,omitempty"` // what it calls, by peer: "mongodb.query → orders-db"
	Hosts      []invGroup `json:"hosts,omitempty"`
	Versions   []invGroup `json:"versions,omitempty"`

	LogQuery  string          `json:"log_query,omitempty"`
	ErrorLogs *patternsReport `json:"error_logs,omitempty"`

	Monitors []datadog.MonitorSearchHit `json:"monitors,omitempty"` // their state now
	// WatchedBy are the monitors that watch the service, with their query:
	// naming it, or alerting per service.
	WatchedBy []datadog.Monitor            `json:"watched_by,omitempty"`
	SLOs      []datadog.SLO                `json:"slos,omitempty"`
	Catalog   *datadog.ServiceCatalogEntry `json:"catalog,omitempty"`
	// ExampleTrace is a failing request of the endpoint whose errors grew
	// the most; SlowTrace a slow one of the endpoint that slowed the most.
	ExampleTrace         string        `json:"example_trace,omitempty"`
	ExampleTraceResource string        `json:"example_trace_resource,omitempty"`
	SlowTrace            string        `json:"slow_trace,omitempty"`
	SlowTraceResource    string        `json:"slow_trace_resource,omitempty"`
	SlowTraceDuration    time.Duration `json:"slow_trace_duration,omitempty"`
}

type invFacts struct {
	Now       time.Time              `json:"now"` // when the facts were read
	Question  string                 `json:"question,omitempty"`
	Target    string                 `json:"target"`
	From      time.Time              `json:"from"`
	To        time.Time              `json:"to"`
	Compare   time.Duration          `json:"compare"`
	Lookback  time.Duration          `json:"lookback"` // how far before the window changes are read
	Services  []*invServiceFacts     `json:"services"`
	Alerts    []datadog.EventV2      `json:"alerts,omitempty"`
	Changes   []datadog.EventV2      `json:"changes,omitempty"`
	Events    []datadog.EventV2      `json:"events,omitempty"`
	Audit     []datadog.AuditEvent   `json:"audit,omitempty"`
	Incidents []datadog.IncidentData `json:"incidents,omitempty"`
	Failed    map[string]string      `json:"failed,omitempty"`
	Notes     []string               `json:"notes,omitempty"` // how the target was resolved
}

func (f *invFacts) baseFrom() time.Time { return f.From.Add(-f.Compare) }
func (f *invFacts) baseTo() time.Time   { return f.To.Add(-f.Compare) }

// ─── gathering ───────────────────────────────────────────────────

// Datadog allows only five spans searches or aggregations a minute, so the
// spans API is asked twice — what the service does, and one failing
// request — and everything that's compared with the baseline comes from
// the trace metrics, in two batched queries: the window and the baseline.

// gatherInvestigation resolves the target and reads every source,
// concurrently. A source that fails is recorded, never fatal.
func gatherInvestigation(target, resource, env, question string, from, to time.Time, compare time.Duration) (*invFacts, error) {
	f := &invFacts{Now: time.Now(), Question: question, Target: target, From: from, To: to, Compare: compare, Lookback: 2 * time.Hour, Failed: map[string]string{}}
	name, probe, resource, notes, err := resolveTarget(target, resource, env, from, to)
	if err != nil {
		return nil, err
	}
	f.Notes = notes
	g := newGatherer(f)
	s := &invServiceFacts{Name: name, Resource: resource}
	f.Services = append(f.Services, s)
	g.service(s, env, probe)
	g.org()
	g.wait()
	if len(f.Failed) == 0 {
		f.Failed = nil
	}
	return f, nil
}

// svcProbe is what a service's spans say it does: in which environments,
// which operations, and which of those serve requests.
type svcProbe struct {
	envs   map[string]int
	ops    map[string]map[string]int // env → operation → spans
	server map[string]map[string]int // env → operation → server spans
}

func (p *svcProbe) empty() bool { return p == nil || len(p.envs) == 0 }

// probeService reads it in one spans aggregation.
func probeService(name string, from, to time.Time) (*svcProbe, error) {
	buckets, err := client.AggregateSpans("service:"+name, rfc(from), rfc(to), []string{"env", "@span.kind", "operation_name"}, 30)
	if err != nil {
		return nil, err
	}
	p := &svcProbe{envs: map[string]int{}, ops: map[string]map[string]int{}, server: map[string]map[string]int{}}
	for _, b := range buckets {
		env, op := b.By["env"], b.By["operation_name"]
		if env == "N/A" {
			env = ""
		}
		p.envs[env] += b.Count
		if p.ops[env] == nil {
			p.ops[env], p.server[env] = map[string]int{}, map[string]int{}
		}
		p.ops[env][op] += b.Count
		if b.By["@span.kind"] == "server" {
			p.server[env][op] += b.Count
		}
	}
	return p, nil
}

// resolveTarget turns the target into a service (and maybe an endpoint):
// a service by that name, or the best service, endpoint or operation
// 'find' sees in the words.
func resolveTarget(target, resource, env string, from, to time.Time) (string, *svcProbe, string, []string, error) {
	if target == "" {
		return "", nil, "", nil, fmt.Errorf("--resource needs the service it belongs to: datadog investigate <service> --resource %q", resource)
	}
	// A day before the window too: a service that stopped serving during
	// the window still shows what it does.
	lookFrom := from.Add(-24 * time.Hour)
	if !strings.ContainsAny(target, " \t\"") {
		if probe, err := probeService(target, lookFrom, to); err == nil && !probe.empty() {
			return target, probe, resource, nil, nil
		}
		if res, err := client.AggregateLogs("service:"+target, rfc(lookFrom), rfc(to), []string{"service"}, 1); err == nil && len(res.Data.Buckets) > 0 && bucketCount(res.Data.Buckets[0]) > 0 {
			return target, nil, resource, []string{fmt.Sprintf("%s sends logs but no APM spans", target)}, nil
		}
	}
	rep := runFind(target, env, lookFrom, to)
	best := rep.Best
	if best == nil || best.Score < findThreshold || (best.Kind != "service" && best.Kind != "endpoint" && best.Kind != "operation") {
		var seen []string
		for _, m := range rep.Matches {
			if m.Kind == "service" {
				seen = append(seen, m.Name)
			}
		}
		msg := fmt.Sprintf("no service matches %q", target)
		if len(seen) > 0 {
			msg += " — close ones: " + strings.Join(seen, ", ")
		}
		return "", nil, "", nil, fmt.Errorf("%s (datadog find %q shows everything that matches)", msg, target)
	}
	name, note := best.Name, fmt.Sprintf("%q is service %s", target, best.Name)
	if best.Kind != "service" {
		name, note = best.Service, fmt.Sprintf("%q is the %s %q of service %s", target, best.Kind, best.Name, best.Service)
		if best.Kind == "endpoint" && resource == "" {
			resource = best.Name
		}
	}
	probe, _ := probeService(name, lookFrom, to)
	return name, probe, resource, []string{note}, nil
}

// gatherer runs the probes with a bounded number of requests in flight.
type gatherer struct {
	f   *invFacts
	mu  sync.Mutex
	wg  sync.WaitGroup
	sem chan struct{}
}

func newGatherer(f *invFacts) *gatherer { return &gatherer{f: f, sem: make(chan struct{}, 6)} }

// runIn starts a probe counted in wave (and in the whole gathering).
func (g *gatherer) runIn(wave *sync.WaitGroup, source string, fn func() error) {
	wave.Add(1)
	g.wg.Add(1)
	go func() {
		defer g.wg.Done()
		defer wave.Done()
		g.sem <- struct{}{}
		defer func() { <-g.sem }()
		start := time.Now()
		err := fn()
		if os.Getenv("DATADOG_DEBUG_TIMING") != "" {
			fmt.Fprintf(os.Stderr, "%6.1fs  %s\n", time.Since(start).Seconds(), source)
		}
		if err != nil {
			g.mu.Lock()
			g.f.Failed[source] = err.Error()
			g.mu.Unlock()
		}
	}()
}

func (g *gatherer) run(source string, fn func() error) {
	var wave sync.WaitGroup
	g.runIn(&wave, source, fn)
}

func (g *gatherer) wait() { g.wg.Wait() }

func (g *gatherer) lock(fn func()) {
	g.mu.Lock()
	defer g.mu.Unlock()
	fn()
}

func rfc(t time.Time) string { return t.UTC().Format(time.RFC3339) }

// maxOperations is how many of what a service does (besides serving
// requests) are compared: its busiest calls.
const maxOperations = 6

// contextSpan is how far before the window a trouble already on is dated.
const contextSpan = 6 * time.Hour

// service reads one service: the trace metrics batches and the rest at
// once, then a failing request of the worst endpoint.
func (g *gatherer) service(s *invServiceFacts, env string, probe *svcProbe) {
	if env == "" && !probe.empty() {
		env = productionEnv(probe.envs)
		if env == "" {
			best := 0
			for e, n := range probe.envs {
				if n > best {
					best, env = n, e
				}
			}
		}
	}
	s.Env = env
	var ops []string
	if !probe.empty() {
		best := 0
		for op, n := range probe.server[env] {
			if n > best {
				best, s.Entry = n, op
			}
		}
		for op := range probe.ops[env] {
			if op != s.Entry && op != "" {
				ops = append(ops, op)
			}
		}
		sort.Slice(ops, func(i, j int) bool { return probe.ops[env][ops[i]] > probe.ops[env][ops[j]] })
		if len(ops) > maxOperations {
			ops = ops[:maxOperations]
		}
	}
	g.wg.Add(1)
	go func() {
		defer g.wg.Done()
		var wave sync.WaitGroup
		batch := g.serviceProbes(s, ops, &wave)
		wave.Wait()
		batch.assemble(s)
		var wave2 sync.WaitGroup
		g.followUps(s, &wave2)
		wave2.Wait()
	}()
}

// metricBatch is one service's trace-metric queries, asked together for
// the window and for the baseline.
type metricBatch struct {
	queries   []string
	keys      []string // what each query is: "res.hits", "op.p95 <op>"…
	now, base [][]datadog.MetricsSeries
	tagScope  string
}

func (m *metricBatch) add(key, query string) {
	m.keys = append(m.keys, key)
	m.queries = append(m.queries, query)
}

// series returns the answers to one query key, window then baseline.
func (m *metricBatch) series(key string) (now, base []datadog.MetricsSeries) {
	for i, k := range m.keys {
		if k == key {
			if i < len(m.now) {
				now = m.now[i]
			}
			if i < len(m.base) {
				base = m.base[i]
			}
			return
		}
	}
	return nil, nil
}

func (g *gatherer) serviceProbes(s *invServiceFacts, ops []string, wave *sync.WaitGroup) *metricBatch {
	f := g.f
	tagScope := "service:" + s.Name
	if s.Env != "" {
		tagScope = "env:" + s.Env + "," + tagScope
	}
	batch := &metricBatch{tagScope: tagScope}
	if op := s.Entry; op != "" {
		batch.add("res.hits", fmt.Sprintf("sum:trace.%s.hits{%s} by {resource_name}.as_count()", op, tagScope))
		batch.add("res.errors", fmt.Sprintf("sum:trace.%s.errors{%s} by {resource_name}.as_count()", op, tagScope))
		batch.add("res.p95", fmt.Sprintf("p95:trace.%s{%s} by {resource_name}", op, tagScope))
		batch.add("p95", fmt.Sprintf("p95:trace.%s{%s}", op, tagScope))
		batch.add("hosts", fmt.Sprintf("sum:trace.%s.hits{%s} by {host}.as_count()", op, tagScope))
		batch.add("versions", fmt.Sprintf("sum:trace.%s.hits{%s} by {version}.as_count()", op, tagScope))
	}
	for _, op := range ops {
		batch.add("op.hits "+op, fmt.Sprintf("sum:trace.%s.hits{%s} by {peer.service}.as_count()", op, tagScope))
		batch.add("op.errors "+op, fmt.Sprintf("sum:trace.%s.errors{%s} by {peer.service}.as_count()", op, tagScope))
		batch.add("op.p95 "+op, fmt.Sprintf("p95:trace.%s{%s} by {peer.service}", op, tagScope))
	}
	if op := s.Entry; op != "" {
		// The hours before the window, for the service's (or endpoint's)
		// three signals: when did it start, if it was already on?
		scope := tagScope
		if s.Resource != "" {
			scope += ",resource_name:" + normalizeTag(s.Resource)
		}
		qs := []string{
			fmt.Sprintf("sum:trace.%s.hits{%s}.as_count()", op, scope),
			fmt.Sprintf("sum:trace.%s.errors{%s}.as_count()", op, scope),
			fmt.Sprintf("p95:trace.%s{%s}", op, scope),
		}
		g.runIn(wave, s.Name+" before the window", func() error {
			resp, err := client.QueryMetrics(strings.Join(qs, ","), f.From.Add(-contextSpan).Unix(), f.To.Unix())
			if err != nil {
				return err
			}
			before := map[string][]series.Point{}
			for _, sr := range resp.Series {
				key := []string{"hits", "errors", "p95"}[min(max(sr.QueryIndex, 0), 2)]
				before[key] = append(before[key], points(sr)...)
			}
			g.lock(func() { s.Before = before })
			return nil
		})
	}
	if len(batch.queries) > 0 {
		for i, win := range [][2]time.Time{{f.From, f.To}, {f.baseFrom(), f.baseTo()}} {
			i, win := i, win
			g.runIn(wave, s.Name+" trace metrics", func() error {
				resp, err := client.QueryMetrics(strings.Join(batch.queries, ","), win[0].Unix(), win[1].Unix())
				if err != nil {
					return err
				}
				byIndex := make([][]datadog.MetricsSeries, len(batch.queries))
				for _, sr := range resp.Series {
					if sr.QueryIndex >= 0 && sr.QueryIndex < len(byIndex) {
						byIndex[sr.QueryIndex] = append(byIndex[sr.QueryIndex], sr)
					}
				}
				g.lock(func() {
					if i == 0 {
						batch.now = byIndex
					} else {
						batch.base = byIndex
					}
				})
				return nil
			})
		}
	}

	// Error logs: patterns, and which are new against the baseline.
	g.runIn(wave, s.Name+" error logs", func() error {
		q := "service:" + s.Name + " status:error"
		if s.Env != "" {
			if res, err := client.AggregateLogs("service:"+s.Name, rfc(f.From), rfc(f.To), []string{"env"}, 10); err == nil {
				for _, bk := range res.Data.Buckets {
					if bk.By["env"] == s.Env {
						q += " env:" + s.Env
						break
					}
				}
			}
		}
		rep, err := logPatterns(q, f.From, f.To, 1000, fmtDuration(f.Compare))
		if err != nil {
			return err
		}
		g.lock(func() { s.LogQuery, s.ErrorLogs = q, rep })
		return nil
	})
	g.runIn(wave, s.Name+" monitors", func() error {
		res, err := client.SearchMonitorsRich(fmt.Sprintf("tag:%q", "service:"+s.Name), 100)
		if err != nil {
			return err
		}
		g.lock(func() { s.Monitors = res.Monitors })
		return nil
	})
	g.runIn(wave, s.Name+" monitor queries", func() error {
		all, err := client.ListMonitors("", 0)
		if err != nil {
			return err
		}
		g.lock(func() {
			for _, m := range all {
				names := monitorServices(m)
				if names[s.Name] || (len(names) == 0 && rePerService.MatchString(m.Query)) {
					s.WatchedBy = append(s.WatchedBy, m)
				}
			}
		})
		return nil
	})
	g.runIn(wave, s.Name+" slos", func() error {
		slos, err := client.ListSLOs("")
		if err != nil {
			return err
		}
		g.lock(func() {
			for _, slo := range slos {
				if hasTag(slo.Tags, "service:"+s.Name) {
					s.SLOs = append(s.SLOs, slo)
				}
			}
		})
		return nil
	})
	g.runIn(wave, s.Name+" catalog", func() error {
		c, err := client.GetService(s.Name)
		if err != nil {
			return nil // not in the catalog: the analysis says so
		}
		g.lock(func() { s.Catalog = c })
		return nil
	})
	return batch
}

// followUps need the first answers: a failing request of the endpoint
// whose errors grew the most, and a slow one of the endpoint that slowed
// the most — each with its real resource name.
func (g *gatherer) followUps(s *invServiceFacts, wave *sync.WaitGroup) {
	f := g.f
	if s.Entry == "" {
		return
	}
	scope := "service:" + s.Name
	if s.Env != "" {
		scope += " env:" + s.Env
	}
	pick := func(data []datadog.SpanData, want string) *datadog.SpanData {
		for i := range data {
			if normalizeTag(data[i].Attributes.ResourceName) == normalizeTag(want) {
				return &data[i]
			}
		}
		if len(data) > 0 {
			return &data[0]
		}
		return nil
	}
	if names, queries, units := vitalQueries(s); len(queries) > 0 {
		vitals := make([]invVital, len(queries))
		for i := range queries {
			vitals[i] = invVital{Name: names[i], Query: queries[i], Unit: units[i]}
		}
		var got [2][][]series.Point
		var vwave sync.WaitGroup
		for i, win := range [][2]time.Time{{f.From, f.To}, {f.baseFrom(), f.baseTo()}} {
			i, win := i, win
			g.runIn(&vwave, s.Name+" vitals", func() error {
				resp, err := client.QueryMetrics(strings.Join(queries, ","), win[0].Unix(), win[1].Unix())
				if err != nil {
					return err
				}
				byIndex := make([][]series.Point, len(queries))
				for _, sr := range resp.Series {
					if sr.QueryIndex >= 0 && sr.QueryIndex < len(byIndex) {
						byIndex[sr.QueryIndex] = append(byIndex[sr.QueryIndex], points(sr)...)
					}
				}
				g.lock(func() { got[i] = byIndex })
				return nil
			})
		}
		wave.Add(1)
		go func() {
			defer wave.Done()
			vwave.Wait()
			g.lock(func() {
				for i := range vitals {
					if i < len(got[0]) {
						vitals[i].Points = got[0][i]
					}
					if i < len(got[1]) {
						vitals[i].Base = got[1][i]
					}
					if len(vitals[i].Points) > 0 {
						s.Vitals = append(s.Vitals, vitals[i])
					}
				}
			})
		}()
	}
	if want := topErrorResource(s); want != "" {
		g.runIn(wave, s.Name+" failing request", func() error {
			sp, err := client.SearchSpans(scope+" status:error @span.kind:server"+resourceHint(want), rfc(f.From), rfc(f.To), 50)
			if err != nil {
				return err
			}
			if d := pick(sp.Data, want); d != nil {
				g.lock(func() { s.ExampleTrace, s.ExampleTraceResource = d.Attributes.TraceID(), d.Attributes.ResourceName })
			}
			return nil
		})
	}
	if slow := topSlowedResource(s); slow != nil {
		g.runIn(wave, s.Name+" slow request", func() error {
			q := fmt.Sprintf("%s @span.kind:server @duration:>=%d%s", scope, int64(slow.P95), resourceHint(slow.Name))
			sp, err := client.SearchSpans(q, rfc(f.From), rfc(f.To), 50)
			if err != nil {
				return err
			}
			if d := pick(sp.Data, slow.Name); d != nil {
				dur := datadog.ParseTime(d.Attributes.EndTimestamp).Sub(datadog.ParseTime(d.Attributes.StartTimestamp))
				g.lock(func() {
					s.SlowTrace, s.SlowTraceResource, s.SlowTraceDuration = d.Attributes.TraceID(), d.Attributes.ResourceName, dur
				})
			}
			return nil
		})
	}
}

// resourceHint narrows a spans search to an endpoint by its most specific
// plain path segment (" resource_name:*geocontext*"): the metric only has
// the endpoint's normalized name, not the exact one to search for.
func resourceHint(resource string) string {
	segs := strings.FieldsFunc(normalizeTag(resource), func(r rune) bool { return r == '/' || r == '_' })
	for i := len(segs) - 1; i >= 0; i-- {
		seg := segs[i]
		if len(seg) >= 4 && !strings.ContainsAny(seg, ":.") && !httpMethods[seg] {
			return " resource_name:*" + seg + "*"
		}
	}
	return ""
}

// vitalQueries are the process's and hosts' vital signs: every Agent sends
// the system ones; the runtime ones need runtime metrics on the service.
func vitalQueries(s *invServiceFacts) (names, queries, units []string) {
	scope := "service:" + s.Name
	if s.Env != "" {
		scope = "env:" + s.Env + "," + scope
	}
	for _, v := range []struct{ name, query, unit string }{
		{"event loop delay (max)", "max:runtime.node.event_loop.delay.max{%s}", "nanosecond"},
		{"GC pause (max)", "max:runtime.node.gc.pause.max{%s}", "nanosecond"},
		{"JVM GC time", "avg:jvm.gc.major_collection_time{%s}", "nanosecond"},
		{"Python GC", "avg:runtime.python.gc.count.gen2{%s}", ""},
	} {
		names, queries, units = append(names, v.name), append(queries, fmt.Sprintf(v.query, scope)), append(units, v.unit)
	}
	for i, h := range s.Hosts {
		if i >= 6 || h.Count == 0 {
			break
		}
		names = append(names, "CPU "+h.Name, "memory used "+h.Name)
		queries = append(queries, fmt.Sprintf("avg:system.cpu.user{host:%s}", h.Name), fmt.Sprintf("avg:system.mem.pct_usable{host:%s}", h.Name))
		units = append(units, "percent", "fraction usable")
	}
	return
}

// topSlowedResource is the endpoint whose p95 grew the most, weighted by
// its requests (nil when none slowed down).
func topSlowedResource(s *invServiceFacts) *invGroup {
	var best *invGroup
	score := 0.0
	for i := range s.Resources {
		r := &s.Resources[i]
		if r.Count < 20 || r.BaseP95 == 0 || float64(r.P95) < float64(r.BaseP95)*latencyFactor || r.P95-r.BaseP95 < latencyMinDelta {
			continue
		}
		if sc := float64(r.P95-r.BaseP95) * r.Count; sc > score {
			best, score = r, sc
		}
	}
	return best
}

// org reads what isn't about one service: alerts, changes, the audit
// trail and incidents, from a little before the window (a deploy an hour
// before an outage matters).
func (g *gatherer) org() {
	f := g.f
	lf, wt := rfc(f.From.Add(-f.Lookback)), rfc(f.To)
	g.run("monitor alerts", func() error {
		evs, err := client.SearchEvents("source:alert", lf, wt, 1000)
		if err != nil {
			return err
		}
		g.lock(func() { f.Alerts = evs })
		return nil
	})
	g.run("change events", func() error {
		evs, err := client.SearchEvents("@evt.category:(change OR deployment) OR source:(deploy OR deployment OR github OR gitlab OR kubernetes OR argocd)", lf, wt, 300)
		if err != nil {
			return err
		}
		g.lock(func() { f.Changes = evs })
		return nil
	})
	for _, s := range f.Services {
		name := s.Name
		g.run(name+" events", func() error {
			evs, err := client.SearchEvents("service:"+name+" -source:alert", lf, wt, 100)
			if err != nil {
				return err
			}
			g.lock(func() { f.Events = append(f.Events, evs...) })
			return nil
		})
	}
	g.run("audit trail", func() error {
		evs, err := client.SearchAuditEvents("-@asset.type:datadog_agent_configuration", lf, wt, 100)
		if err != nil {
			return err
		}
		g.lock(func() { f.Audit = evs })
		return nil
	})
	g.run("incidents", func() error {
		incs, err := client.ListIncidents()
		if err != nil {
			return err
		}
		for _, inc := range incs {
			if incidentOverlaps(inc, f.From, f.To) {
				g.lock(func() { f.Incidents = append(f.Incidents, inc) })
			}
		}
		return nil
	})
}

func incidentOverlaps(inc datadog.IncidentData, from, to time.Time) bool {
	created := datadog.ParseTime(inc.Attributes.Created)
	if created.IsZero() || created.After(to) {
		return false
	}
	if r := inc.Attributes.Resolved; r != nil && *r != "" {
		if resolved := datadog.ParseTime(*r); !resolved.IsZero() && resolved.Before(from) {
			return false
		}
	}
	return true
}

// ─── series helpers ──────────────────────────────────────────────

// points are a series' points; for counts, the intervals Datadog leaves
// out are zeros.
func points(sr datadog.MetricsSeries) []series.Point {
	return seriesPoints(sr, strings.Contains(sr.Expression, "as_count") || strings.HasSuffix(sr.Metric, ".hits") || strings.HasSuffix(sr.Metric, ".errors"))
}

// sumSeries adds series point by point (same timestamps).
func sumSeries(all []datadog.MetricsSeries) []series.Point {
	acc := map[int64]float64{}
	for _, sr := range all {
		for _, p := range points(sr) {
			if p.V == p.V {
				acc[p.T] += p.V
			}
		}
	}
	out := make([]series.Point, 0, len(acc))
	for t, v := range acc {
		out = append(out, series.Point{T: t, V: v})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].T < out[j].T })
	return out
}

func total(pts []series.Point) float64 {
	t := 0.0
	for _, p := range pts {
		if p.V == p.V {
			t += p.V
		}
	}
	return t
}

func meanOf(pts []series.Point) float64 {
	t, n := 0.0, 0
	for _, p := range pts {
		if p.V == p.V {
			t += p.V
			n++
		}
	}
	if n == 0 {
		return 0
	}
	return t / float64(n)
}

// tagValue is a tag's value in a series' scope ("host:web-1,env:prod").
func tagValue(scope, key string) string {
	for _, part := range strings.Split(scope, ",") {
		if v, ok := strings.CutPrefix(part, key+":"); ok {
			return v
		}
	}
	return ""
}

// normalizeTag approximates how Datadog normalizes a tag value
// ("GET /v1/orders" → "get_/v1/orders"), to match an endpoint's series.
func normalizeTag(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	var b strings.Builder
	under := false
	for _, r := range s {
		ok := r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || strings.ContainsRune("_-:./", r)
		if !ok {
			if !under {
				b.WriteRune('_')
				under = true
			}
			continue
		}
		b.WriteRune(r)
		under = r == '_'
	}
	return strings.Trim(b.String(), "_")
}

var httpMethods = map[string]bool{"get": true, "post": true, "put": true, "patch": true, "delete": true, "head": true, "options": true}

// prettyResource undoes the tag normalization where it can: the endpoint
// "get_/v1/orders" reads as "GET /v1/orders".
func prettyResource(tag string) string {
	if httpMethods[tag] {
		return strings.ToUpper(tag)
	}
	if method, rest, ok := strings.Cut(tag, "_"); ok && httpMethods[method] {
		return strings.ToUpper(method) + " " + rest
	}
	return tag
}

// assemble turns the batch's answers into the service's signals (or the
// focused endpoint's), its endpoints, instances, versions and operations.
func (m *metricBatch) assemble(s *invServiceFacts) {
	op := s.Entry
	if op != "" {
		hits, hitsBase := m.series("res.hits")
		errs, errsBase := m.series("res.errors")
		lat, latBase := m.series("res.p95")
		focus := func(all []datadog.MetricsSeries) []datadog.MetricsSeries {
			if s.Resource == "" {
				return all
			}
			want := normalizeTag(s.Resource)
			var out []datadog.MetricsSeries
			for _, sr := range all {
				if tagValue(sr.Scope, "resource_name") == want {
					out = append(out, sr)
				}
			}
			return out
		}
		scope := m.tagScope
		if s.Resource != "" {
			scope += ",resource_name:" + normalizeTag(s.Resource)
		}
		s.Hits = &invSeries{Query: fmt.Sprintf("sum:trace.%s.hits{%s}.as_count()", op, scope), Points: sumSeries(focus(hits)), Base: sumSeries(focus(hitsBase))}
		s.Errors = &invSeries{Query: fmt.Sprintf("sum:trace.%s.errors{%s}.as_count()", op, scope), Points: sumSeries(focus(errs)), Base: sumSeries(focus(errsBase))}
		if s.Resource != "" {
			s.P95 = &invSeries{Query: fmt.Sprintf("p95:trace.%s{%s}", op, scope), Points: sumSeries(focus(lat)), Base: sumSeries(focus(latBase))}
		} else {
			p, pb := m.series("p95")
			s.P95 = &invSeries{Query: fmt.Sprintf("p95:trace.%s{%s}", op, scope), Points: sumSeries(p), Base: sumSeries(pb)}
		}
		s.LatencyUnit = "second"

		groups := map[string]*invGroup{}
		get := func(sr datadog.MetricsSeries) *invGroup {
			name := prettyResource(tagValue(sr.Scope, "resource_name"))
			if groups[name] == nil {
				groups[name] = &invGroup{Name: name}
			}
			return groups[name]
		}
		for _, sr := range hits {
			get(sr).Count += total(points(sr))
		}
		for _, sr := range hitsBase {
			get(sr).BaseCount += total(points(sr))
		}
		for _, sr := range errs {
			get(sr).Errors += total(points(sr))
		}
		for _, sr := range errsBase {
			get(sr).BaseErrors += total(points(sr))
		}
		for _, sr := range lat {
			g := get(sr)
			g.P95Series = points(sr)
			g.P95 = seconds(series.Describe(g.P95Series).Median)
		}
		for _, sr := range latBase {
			get(sr).BaseP95 = seconds(series.Describe(points(sr)).Median)
		}
		for _, g := range groups {
			s.Resources = append(s.Resources, *g)
		}
		sort.Slice(s.Resources, func(i, j int) bool { return s.Resources[i].Count > s.Resources[j].Count })

		s.Hosts = m.presence("hosts", "host")
		s.Versions = m.presence("versions", "version")
	}

	// Operations: what the service calls, by the peer it calls.
	groups := map[string]*invGroup{}
	var order []string
	get := func(op string, sr datadog.MetricsSeries) *invGroup {
		name := op
		if peer := tagValue(sr.Scope, "peer.service"); peer != "" && peer != "N/A" {
			name = op + " → " + peer
		}
		if groups[name] == nil {
			groups[name] = &invGroup{Name: name}
			order = append(order, name)
		}
		return groups[name]
	}
	for _, key := range m.keys {
		op, ok := strings.CutPrefix(key, "op.hits ")
		if !ok {
			continue
		}
		hits, hitsBase := m.series("op.hits " + op)
		errs, errsBase := m.series("op.errors " + op)
		lat, latBase := m.series("op.p95 " + op)
		for _, sr := range hits {
			get(op, sr).Count += total(points(sr))
		}
		for _, sr := range hitsBase {
			get(op, sr).BaseCount += total(points(sr))
		}
		for _, sr := range errs {
			get(op, sr).Errors += total(points(sr))
		}
		for _, sr := range errsBase {
			get(op, sr).BaseErrors += total(points(sr))
		}
		for _, sr := range lat {
			g := get(op, sr)
			g.P95Series = points(sr)
			g.P95 = seconds(series.Describe(g.P95Series).Median)
		}
		for _, sr := range latBase {
			get(op, sr).BaseP95 = seconds(series.Describe(points(sr)).Median)
		}
	}
	for _, name := range order {
		s.Operations = append(s.Operations, *groups[name])
	}
	sort.SliceStable(s.Operations, func(i, j int) bool { return s.Operations[i].Count > s.Operations[j].Count })
}

// presence is who served requests (hosts, versions): how many now and
// before, and when each first did in the window.
func (m *metricBatch) presence(key, tag string) []invGroup {
	now, base := m.series(key)
	idx := map[string]*invGroup{}
	var out []*invGroup
	get := func(sr datadog.MetricsSeries) *invGroup {
		name := tagValue(sr.Scope, tag)
		if name == "" || name == "N/A" {
			return nil
		}
		if idx[name] == nil {
			idx[name] = &invGroup{Name: name}
			out = append(out, idx[name])
		}
		return idx[name]
	}
	for _, sr := range now {
		if g := get(sr); g != nil {
			pts := points(sr)
			g.Count += total(pts)
			for _, p := range pts {
				if p.V > 0 {
					t := time.UnixMilli(p.T)
					if g.FirstSeen.IsZero() || t.Before(g.FirstSeen) {
						g.FirstSeen = t
					}
					break
				}
			}
		}
	}
	for _, sr := range base {
		if g := get(sr); g != nil {
			g.BaseCount += total(points(sr))
		}
	}
	groups := make([]invGroup, 0, len(out))
	for _, g := range out {
		groups = append(groups, *g)
	}
	sort.Slice(groups, func(i, j int) bool { return groups[i].Count > groups[j].Count })
	return groups
}

// topErrorResource is the endpoint whose errors grew the most.
func topErrorResource(s *invServiceFacts) string {
	best, name := 0.0, ""
	for _, r := range s.Resources {
		if d := r.Errors - r.BaseErrors; d > best && r.Errors > 0 {
			best, name = d, r.Name
		}
	}
	if name == "" {
		for _, r := range s.Resources {
			if r.Errors > best {
				best, name = r.Errors, r.Name
			}
		}
	}
	return name
}
