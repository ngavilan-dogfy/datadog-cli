package cmd

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/ngavilan-dogfy/datadog-cli/datadog"
	"github.com/ngavilan-dogfy/datadog-cli/ui"

	"github.com/spf13/cobra"
)

var (
	covWin     windowFlags
	covEnv     string
	covService string
	covJSON    bool
	covMD      bool
)

// coverageService is how well one service is watched.
type coverageService struct {
	Service    string              `json:"service"`
	Entry      string              `json:"entry_operation,omitempty"` // fastify.request, web.request…
	Spans      int                 `json:"spans"`                     // indexed APM spans in the window
	ErrorSpans int                 `json:"error_spans"`
	Logs       int                 `json:"logs"`
	ErrorLogs  int                 `json:"error_logs"`
	Monitors   map[string][]string `json:"monitors"` // by kind: errors, latency, traffic, logs, other
	Silent     []string            `json:"monitors_notifying_no_one,omitempty"`
	SLOs       []string            `json:"slos,omitempty"`
	InCatalog  bool                `json:"in_catalog"`
	Team       string              `json:"team,omitempty"`
	Gaps       []coverageGap       `json:"gaps,omitempty"`
}

// coverageGap is something missing, and how to add it.
type coverageGap struct {
	What string `json:"what"`
	Fix  string `json:"fix,omitempty"`
}

type coverageReport struct {
	From     time.Time         `json:"from"`
	To       time.Time         `json:"to"`
	Env      string            `json:"env,omitempty"`
	Services []coverageService `json:"services"`
	Failed   map[string]string `json:"failed,omitempty"` // sources that couldn't be read
}

var coverageCmd = &cobra.Command{
	Use:   "coverage",
	Short: "Which services are watched, and what's missing: monitors, SLOs, owners",
	Long: `Cross every service that sends APM spans or logs with the monitors, SLOs
and Service Catalog entries that watch it, and list the gaps: no error-rate
or latency monitor on its requests, error logs nobody alerts on, monitors
that notify no one, no SLO, no owner. Each gap comes with the monitor query
that would close it, built from the service's own metrics.

It reads the production environment when there is one (env:production,
prod…); --env to choose, --env all for every environment.

Output:
  Terminal: a table and the gaps · --md: markdown · --json: everything

Examples:
  datadog coverage
  datadog coverage --since 7d --md
  datadog coverage --service checkout
  datadog coverage --env staging --json | jq '.services[] | select(.gaps)'`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		from, to, err := covWin.resolve()
		if err != nil {
			return err
		}
		rep, err := buildCoverage(from, to, covEnv, covService)
		if err != nil {
			return err
		}
		switch {
		case covJSON:
			return printJSON(rep)
		case covMD:
			fmt.Print(rep.markdown())
			return nil
		}
		fmt.Print(rep.text(isTTY()))
		return nil
	},
}

// productionEnv picks the production-looking env among those with spans.
func productionEnv(envs map[string]int) string {
	for _, want := range []string{"production", "prod", "prd", "live", "pro"} {
		if envs[want] > 0 {
			return want
		}
	}
	return ""
}

func buildCoverage(from, to time.Time, env, only string) (*coverageReport, error) {
	f, t := from.UTC().Format(time.RFC3339), to.UTC().Format(time.RFC3339)
	rep := &coverageReport{From: from, To: to, Failed: map[string]string{}}
	if env == "" {
		envs := map[string]int{}
		buckets, err := client.AggregateSpans("*", f, t, []string{"env"}, 20)
		if err != nil {
			return nil, err
		}
		for _, b := range buckets {
			envs[b.By["env"]] += b.Count
		}
		env = productionEnv(envs)
	}
	if env == "all" {
		env = ""
	}
	rep.Env = env
	scope := "*"
	if env != "" {
		scope = "env:" + env
	}

	svcs := map[string]*coverageService{}
	get := func(name string) *coverageService {
		if name == "" || name == "N/A" {
			return nil
		}
		s := svcs[name]
		if s == nil {
			s = &coverageService{Service: name, Monitors: map[string][]string{}}
			svcs[name] = s
		}
		return s
	}
	var (
		mu       sync.Mutex
		wg       sync.WaitGroup
		entries  []datadog.SpanCount
		monitors []datadog.Monitor
		slos     []datadog.SLO
		catalog  []datadog.ServiceCatalogEntry
		spans    []datadog.SpanCount
		logs     *datadog.LogsAggregateResponse
	)
	run := func(name string, fn func() error) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := fn(); err != nil {
				mu.Lock()
				rep.Failed[name] = err.Error()
				mu.Unlock()
			}
		}()
	}
	run("apm", func() (err error) {
		spans, err = client.AggregateSpans(scope, f, t, []string{"service", "status"}, 100)
		return
	})
	run("apm entry spans", func() (err error) {
		entries, err = client.AggregateSpans(scope+" @span.kind:server", f, t, []string{"service", "operation_name"}, 100)
		return
	})
	run("logs", func() (err error) {
		logs, err = client.AggregateLogs(scope, f, t, []string{"service", "status"}, 100)
		return
	})
	run("monitors", func() (err error) {
		monitors, err = client.ListMonitors("", 0)
		return
	})
	run("slos", func() (err error) {
		slos, err = client.ListSLOs("")
		return
	})
	run("service catalog", func() (err error) {
		catalog, err = client.ListServices()
		return
	})
	wg.Wait()

	for _, b := range spans {
		if s := get(b.By["service"]); s != nil {
			s.Spans += b.Count
			if b.By["status"] == "error" {
				s.ErrorSpans += b.Count
			}
		}
	}
	best := map[string]int{}
	for _, b := range entries {
		if s := svcs[b.By["service"]]; s != nil && b.Count > best[s.Service] {
			best[s.Service], s.Entry = b.Count, b.By["operation_name"]
		}
	}
	if logs != nil {
		for _, b := range logs.Data.Buckets {
			if s := get(b.By["service"]); s != nil {
				n := bucketCount(b)
				s.Logs += n
				if b.By["status"] == "error" || b.By["status"] == "critical" || b.By["status"] == "emergency" || b.By["status"] == "alert" {
					s.ErrorLogs += n
				}
			}
		}
	}
	for _, c := range catalog {
		if s := svcs[c.Attributes.Schema.DDService]; s != nil {
			s.InCatalog, s.Team = true, c.Attributes.Schema.Team
		}
	}
	// Monitors: the services they name, or every service when they alert
	// per service (by {service}).
	byID := map[int64][]string{}
	for _, m := range monitors {
		names := monitorServices(m)
		perService := rePerService.MatchString(m.Query)
		kind := monitorKind(m)
		silent := !strings.Contains(m.Message, "@")
		label := m.Name
		if perService {
			label += " (per service)"
		}
		for _, s := range svcs {
			if !names[s.Service] && !(perService && len(names) == 0 && s.Spans > 0) {
				continue
			}
			s.Monitors[kind] = append(s.Monitors[kind], label)
			if silent {
				s.Silent = append(s.Silent, m.Name)
			}
			byID[m.ID] = append(byID[m.ID], s.Service)
		}
	}
	for _, slo := range slos {
		for _, s := range svcs {
			if sloCovers(slo, s.Service, byID) {
				s.SLOs = append(s.SLOs, slo.Name)
			}
		}
	}
	for _, s := range svcs {
		if only != "" && s.Service != only {
			continue
		}
		s.Gaps = coverageGaps(s, env)
		rep.Services = append(rep.Services, *s)
	}
	sort.Slice(rep.Services, func(i, j int) bool {
		a, b := rep.Services[i], rep.Services[j]
		if a.Spans != b.Spans {
			return a.Spans > b.Spans
		}
		return a.Logs > b.Logs
	})
	if len(rep.Failed) == 0 {
		rep.Failed = nil
	}
	return rep, nil
}

var (
	reServiceTag = regexp.MustCompile(`(?:^|[{,\s("'])service:([\w.\-/]+)`)
	rePerService = regexp.MustCompile(`by \{[^}]*\bservice\b[^}]*\}|group_by\([^)]*service|"facet":\s*"service"`)
	rePercentile = regexp.MustCompile(`\bp(50|75|90|95|99|999)[:(]`)
	reErrorQuery = regexp.MustCompile(`error|5xx|(?:code|status|class)[\w.]*:5`)
)

func monitorServices(m datadog.Monitor) map[string]bool {
	out := map[string]bool{}
	for _, t := range m.Tags {
		if v, ok := strings.CutPrefix(t, "service:"); ok {
			out[v] = true
		}
	}
	for _, x := range reServiceTag.FindAllStringSubmatch(m.Query, -1) {
		if !strings.HasPrefix(x[1], "$") {
			out[x[1]] = true
		}
	}
	return out
}

// monitorKind sorts a monitor into what it watches.
func monitorKind(m datadog.Monitor) string {
	q := strings.ToLower(m.Query)
	switch {
	case m.Type == "log alert":
		return "logs"
	case reErrorQuery.MatchString(q):
		return "errors"
	case strings.Contains(q, "duration") || strings.Contains(q, "latency") || strings.Contains(q, "apdex") || rePercentile.MatchString(q):
		return "latency"
	case (strings.Contains(q, "hits") || strings.Contains(q, "request")) && strings.Contains(q, "<"):
		return "traffic"
	}
	return "other"
}

func sloCovers(slo datadog.SLO, service string, monitorsOf map[int64][]string) bool {
	for _, t := range slo.Tags {
		if t == "service:"+service {
			return true
		}
	}
	if q := slo.Query; q != nil {
		for _, x := range reServiceTag.FindAllStringSubmatch(q.Numerator+" "+q.Denominator, -1) {
			if x[1] == service {
				return true
			}
		}
	}
	for _, id := range slo.MonitorIDs {
		for _, s := range monitorsOf[id] {
			if s == service {
				return true
			}
		}
	}
	return false
}

// coverageGaps lists what a service lacks, most important first, with the
// monitor that would close each gap.
func coverageGaps(s *coverageService, env string) []coverageGap {
	var gaps []coverageGap
	total := 0
	for _, ms := range s.Monitors {
		total += len(ms)
	}
	scope := "service:" + s.Service
	if env != "" {
		scope = "env:" + env + "," + scope
	}
	create := func(name, typ, query string) string {
		return fmt.Sprintf("datadog monitors create %q --type %q --query %q --message \"… @<who-to-notify>\"", name, typ, query)
	}
	op := s.Entry
	if s.Spans > 0 && total == 0 {
		gaps = append(gaps, coverageGap{What: "no monitors at all"})
	}
	if s.Spans > 0 && op != "" {
		if len(s.Monitors["errors"]) == 0 {
			gaps = append(gaps, coverageGap{What: "no error-rate monitor on its requests",
				Fix: create(s.Service+" error rate", "query alert", fmt.Sprintf("sum(last_10m):sum:trace.%s.errors{%s}.as_count() / sum:trace.%s.hits{%s}.as_count() > 0.05", op, scope, op, scope))})
		}
		if len(s.Monitors["latency"]) == 0 {
			gaps = append(gaps, coverageGap{What: "no latency monitor",
				Fix: create(s.Service+" p95 latency", "query alert", fmt.Sprintf("percentile(last_10m):p95:trace.%s{%s} > 1", op, scope)) +
					fmt.Sprintf("   # pick the threshold: datadog metrics describe \"p95:trace.%s{%s}\" --since 7d", op, scope)})
		}
		if len(s.Monitors["traffic"]) == 0 {
			gaps = append(gaps, coverageGap{What: "no traffic-drop monitor: would anyone notice it going silent?",
				Fix: create(s.Service+" traffic drop", "query alert", fmt.Sprintf("sum(last_30m):sum:trace.%s.hits{%s}.as_count() < 10", op, scope)) +
					fmt.Sprintf("   # pick the floor: datadog metrics describe \"sum:trace.%s.hits{%s}.as_count().rollup(sum, 1800)\" --since 7d", op, scope)})
		}
	} else if s.Spans > 0 && len(s.Monitors["errors"]) == 0 {
		gaps = append(gaps, coverageGap{What: "no error monitor on its spans"})
	}
	if s.ErrorLogs > 0 && len(s.Monitors["logs"]) == 0 {
		search := "service:" + s.Service + " status:error"
		if env != "" {
			search += " env:" + env
		}
		what := fmtCount(s.ErrorLogs) + " error logs, and no log monitor"
		if s.ErrorLogs == 1 {
			what = "1 error log, and no log monitor"
		}
		gaps = append(gaps, coverageGap{What: what,
			Fix: create(s.Service+" error logs", "log alert", fmt.Sprintf("logs(%q).index(\"*\").rollup(\"count\").last(\"10m\") > 50", search)) +
				fmt.Sprintf("   # what they say: datadog logs patterns %q", search)})
	}
	if total > 0 && len(s.Silent) == total {
		gaps = append(gaps, coverageGap{What: "its monitors notify no one (no @ in their messages)"})
	}
	if s.Spans > 0 && len(s.SLOs) == 0 {
		gaps = append(gaps, coverageGap{What: "no SLO"})
	}
	switch {
	case !s.InCatalog:
		gaps = append(gaps, coverageGap{What: "not in the Service Catalog: no owner on record"})
	case s.Team == "":
		gaps = append(gaps, coverageGap{What: "no team in the Service Catalog"})
	}
	return gaps
}

func (r *coverageReport) header() string {
	env := "every env"
	if r.Env != "" {
		env = "env:" + r.Env
	}
	return fmt.Sprintf("Observability coverage · %s · %s", env, fmtWindow(r.From, r.To))
}

func (s coverageService) monitorCounts() string {
	var parts []string
	for _, k := range []string{"errors", "latency", "traffic", "logs", "other"} {
		if n := len(s.Monitors[k]); n > 0 {
			parts = append(parts, fmt.Sprintf("%d %s", n, k))
		}
	}
	if len(parts) == 0 {
		return "none"
	}
	return strings.Join(parts, ", ")
}

func (s coverageService) errPct() string {
	if s.Spans == 0 {
		return "–"
	}
	return fmt.Sprintf("%.1f%%", float64(s.ErrorSpans)/float64(s.Spans)*100)
}

func (r *coverageReport) text(tty bool) string {
	var b strings.Builder
	paint := func(st func(...string) string, s string) string {
		if tty {
			return st(s)
		}
		return s
	}
	b.WriteString(paint(ui.Title.Render, r.header()) + "\n\n")
	fmt.Fprintf(&b, "  %-24s %9s %6s %9s   %s   %4s  %s\n", "", "", "", "", paint(ui.Dimmed.Render, "── monitors on ──────"), "", "")
	fmt.Fprintf(&b, "  %-24s %9s %6s %9s   %3s %3s %3s %3s %3s   %4s  %s\n", "SERVICE", "SPANS", "ERR%", "ERR LOGS", "ERR", "LAT", "TRF", "LOG", "OTH", "SLOS", "OWNER")
	count := func(s coverageService, k string) string {
		if n := len(s.Monitors[k]); n > 0 {
			return fmt.Sprint(n)
		}
		return "·"
	}
	for _, s := range r.Services {
		owner := s.Team
		if owner == "" {
			owner = "–"
		}
		fmt.Fprintf(&b, "  %-24s %9s %6s %9s   %3s %3s %3s %3s %3s   %4d  %s\n", truncRunes(s.Service, 24), fmtCount(s.Spans), s.errPct(), fmtCount(s.ErrorLogs),
			count(s, "errors"), count(s, "latency"), count(s, "traffic"), count(s, "logs"), count(s, "other"), len(s.SLOs), owner)
	}
	for _, s := range r.Services {
		if len(s.Gaps) == 0 {
			continue
		}
		b.WriteString("\n" + paint(ui.Subtitle.Render, s.Service) + "\n")
		for _, g := range s.Gaps {
			b.WriteString("  " + paint(ui.ErrorStyle.Render, "✗ "+g.What) + "\n")
			if g.Fix != "" {
				b.WriteString("    " + paint(ui.Dimmed.Render, g.Fix) + "\n")
			}
		}
	}
	for src, err := range r.Failed {
		b.WriteString("\n" + paint(ui.Dimmed.Render, "couldn't read "+src+": "+err) + "\n")
	}
	return b.String()
}

func (r *coverageReport) markdown() string {
	var b strings.Builder
	b.WriteString("## " + r.header() + "\n\n")
	b.WriteString("| service | spans | err % | error logs | monitors | SLOs | owner |\n|---|---:|---:|---:|---|---:|---|\n")
	for _, s := range r.Services {
		fmt.Fprintf(&b, "| %s | %s | %s | %s | %s | %d | %s |\n", s.Service, fmtCount(s.Spans), s.errPct(), fmtCount(s.ErrorLogs), s.monitorCounts(), len(s.SLOs), s.Team)
	}
	for _, s := range r.Services {
		if len(s.Gaps) == 0 {
			continue
		}
		b.WriteString("\n### " + s.Service + "\n\n")
		for _, g := range s.Gaps {
			b.WriteString("- " + g.What + "\n")
			if g.Fix != "" {
				b.WriteString("  ```\n  " + g.Fix + "\n  ```\n")
			}
		}
	}
	for src, err := range r.Failed {
		b.WriteString("\n_Couldn't read " + src + ": " + err + "_\n")
	}
	return b.String()
}

func init() {
	covWin.register(coverageCmd, 24*time.Hour)
	coverageCmd.Flags().StringVar(&covEnv, "env", "", "Environment to read (default: the production one; 'all' for every env)")
	coverageCmd.Flags().StringVar(&covService, "service", "", "Only this service")
	coverageCmd.Flags().BoolVar(&covJSON, "json", false, "Output as JSON")
	coverageCmd.Flags().BoolVar(&covMD, "md", false, "Output as markdown")
	rootCmd.AddCommand(coverageCmd)
}
