package cmd

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/ngavilan-dogfy/datadog-cli/tui"
	"github.com/ngavilan-dogfy/datadog-cli/ui"

	"github.com/spf13/cobra"
)

var (
	readWin  windowFlags
	readJSON bool
	readMD   bool
)

var readCmd = &cobra.Command{
	Use:   "read <datadog link>",
	Short: "Read any Datadog link as text: dashboard, monitor, trace, logs, service…",
	Long: `Paste a Datadog link and get what it shows, as text, with the context the
link carries (time window, template variables, search query):

  dashboard   → every widget described          (dashboards read)
  monitor     → state, thresholds, what its data did (monitors explain)
  trace       → span tree, where time went, logs (trace)
  logs search → the patterns of those logs       (logs patterns)
  APM service → its traffic, errors, latency and coverage gaps
  metric      → the metric described             (metrics describe)
  events      → monitor transitions, deploys and changes in the window
  audit trail → who changed what
  incident, SLO, host → their details

Made for agents: someone pastes a link in a chat, the agent reads it.

Examples:
  datadog read "https://app.datadoghq.eu/dashboard/abc-def-ghi/api?tpl_var_env=production"
  datadog read https://app.datadoghq.eu/monitors/12345
  datadog read "https://app.datadoghq.eu/logs?query=service%3Aapi%20status%3Aerror&from_ts=…&to_ts=…" --md
  datadog read https://app.datadoghq.eu/apm/services/checkout --since 1d`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		l, ok := parseDDLink(args[0])
		if !ok {
			return fmt.Errorf("that isn't a Datadog link I can read (dashboards, monitors, traces, logs, APM services, metrics, incidents, SLOs)")
		}
		// The link's window, unless the flags say otherwise.
		from, to, err := readWin.resolve()
		if err != nil {
			return err
		}
		live := !cmd.Flags().Changed("to") && !cmd.Flags().Changed("from") && !cmd.Flags().Changed("around")
		span, end, linked := l.window()
		if linked && !windowChanged(cmd) {
			live = end.IsZero()
			if live {
				end = time.Now()
			}
			from, to = end.Add(-span), end
		}
		out := func(v interface{}, md func() string, text func() string) error {
			switch {
			case readJSON:
				return printJSON(v)
			case readMD:
				fmt.Print(md())
			default:
				fmt.Print(text())
			}
			return nil
		}
		switch l.Kind {
		case "dashboard":
			raw, err := client.GetDashboard(l.ID)
			if err != nil {
				return err
			}
			opts := tui.ReadOptions{Vars: l.Vars, Span: to.Sub(from), End: to}
			if live {
				opts.End = time.Time{}
			}
			rep := readDashboard(raw, opts)
			return out(rep, func() string { return dashReportMarkdown(rep) }, func() string { return dashReportText(rep, isTTY()) })
		case "monitor":
			id, _ := strconv.ParseInt(l.ID, 10, 64)
			ex, err := explainMonitor(id, from, to)
			if err != nil {
				return err
			}
			return out(ex, ex.markdown, func() string { return ex.text(isTTY()) })
		case "trace":
			id, err := traceIDFromArg(l.ID)
			if err != nil {
				return err
			}
			if !linked {
				from = to.Add(-15 * 24 * time.Hour)
			}
			rep, err := buildTrace(id, from, to, 5000, 50)
			if err == errNoSpans {
				return fmt.Errorf("no indexed spans for trace %s since %s (sampling, or older than retention)", id, from.Local().Format("Jan 2 15:04"))
			}
			if err != nil {
				return err
			}
			return out(rep, func() string { return rep.markdown(60) }, func() string { return rep.text(60, isTTY()) })
		case "logs":
			q := l.Query
			if q == "" {
				q = "*"
			}
			rep, err := logPatterns(q, from, to, 2000, "")
			if err != nil {
				return err
			}
			if len(rep.Patterns) > 20 {
				rep.More, rep.Patterns = len(rep.Patterns)-20, rep.Patterns[:20]
			}
			return out(rep, rep.markdown, func() string { return rep.text(isTTY(), termWidth(120)) })
		case "apm-service":
			sr, err := readService(l.ID, l.Env, from, to)
			if err != nil {
				return err
			}
			return out(sr, sr.markdown, func() string { return sr.text(isTTY()) })
		case "metric":
			q := l.Query
			if q == "" {
				return fmt.Errorf("the link doesn't say which metric")
			}
			if !strings.Contains(q, "{") {
				q = "avg:" + q + "{*}"
			}
			res, err := describeMetric(q, from, to, "", 10)
			if err != nil {
				return err
			}
			return out(res, res.markdown, res.text)
		case "incident":
			if readJSON {
				incidentShowJSON = true
			}
			return incidentsShowCmd.RunE(incidentsShowCmd, []string{l.ID})
		case "slo":
			if l.ID == "" {
				return fmt.Errorf("the link doesn't say which SLO")
			}
			if readJSON {
				sloShowJSON = true
			}
			return slosShowCmd.RunE(slosShowCmd, []string{l.ID})
		case "traces":
			rep, err := spanPatterns(l.Query, from, to)
			if err != nil {
				return err
			}
			return out(rep, rep.markdown, func() string { return rep.text(isTTY()) })
		case "events":
			q := l.Query
			if q == "" {
				q = "*"
			}
			evs, err := client.SearchEvents(q, from.UTC().Format(time.RFC3339), to.UTC().Format(time.RFC3339), 100)
			if err != nil {
				return err
			}
			rep := eventList{Query: q, From: from, To: to, Events: evs}
			return out(rep, rep.markdown, rep.text)
		case "audit":
			if readJSON {
				auditJSON = true
			}
			auditQuery, auditFrom, auditTo = l.Query, from.Format(time.RFC3339), to.Format(time.RFC3339)
			return auditCmd.RunE(auditCmd, nil)
		case "host":
			rep := readHost(l.ID, from, to)
			return out(rep, rep.markdown, func() string { return rep.text(isTTY()) })
		}
		return fmt.Errorf("%s links aren't read yet: open it with 'datadog open'", l.Kind)
	},
}

// serviceRead is an APM service at a glance: its request rate, errors and
// latency described, and how well it's watched.
type serviceRead struct {
	Service  string            `json:"service"`
	Env      string            `json:"env,omitempty"`
	Entry    string            `json:"entry_operation,omitempty"`
	Metrics  []*describeResult `json:"metrics,omitempty"`
	Coverage *coverageService  `json:"coverage,omitempty"`
	From     time.Time         `json:"from"`
	To       time.Time         `json:"to"`
}

func readService(name, env string, from, to time.Time) (*serviceRead, error) {
	cov, err := buildCoverage(from, to, env, name)
	if err != nil {
		return nil, err
	}
	sr := &serviceRead{Service: name, Env: cov.Env, From: from, To: to}
	if len(cov.Services) > 0 {
		sr.Coverage = &cov.Services[0]
		sr.Entry = sr.Coverage.Entry
	}
	if sr.Entry == "" {
		return sr, nil
	}
	scope := "service:" + name
	if sr.Env != "" {
		scope = "env:" + sr.Env + "," + scope
	}
	op := sr.Entry
	for _, q := range []string{
		fmt.Sprintf("sum:trace.%s.hits{%s}.as_count()", op, scope),
		fmt.Sprintf("sum:trace.%s.errors{%s}.as_count()", op, scope),
		fmt.Sprintf("p95:trace.%s{%s}", op, scope),
		fmt.Sprintf("sum:trace.%s.errors{%s} by {resource_name}.as_count()", op, scope),
	} {
		if res, err := describeMetric(q, from, to, "", 5); err == nil {
			sr.Metrics = append(sr.Metrics, res)
		}
	}
	return sr, nil
}

func (sr *serviceRead) header() string {
	s := "Service " + sr.Service
	if sr.Env != "" {
		s += " · env:" + sr.Env
	}
	return s + " · " + fmtWindow(sr.From, sr.To)
}

func (sr *serviceRead) text(tty bool) string {
	var b strings.Builder
	title := sr.header()
	if tty {
		title = ui.Title.Render(title)
	}
	b.WriteString(title + "\n")
	for _, m := range sr.Metrics {
		b.WriteString("\n" + m.text())
	}
	if c := sr.Coverage; c != nil {
		fmt.Fprintf(&b, "\nMonitors: %s · SLOs: %d · owner: %s\n", c.monitorCounts(), len(c.SLOs), orDash(c.Team))
		for _, g := range c.Gaps {
			b.WriteString("  ✗ " + g.What + "\n")
			if g.Fix != "" {
				b.WriteString("    " + g.Fix + "\n")
			}
		}
	}
	return b.String()
}

func (sr *serviceRead) markdown() string {
	var b strings.Builder
	b.WriteString("## " + sr.header() + "\n\n")
	for _, m := range sr.Metrics {
		b.WriteString(m.markdown() + "\n")
	}
	if c := sr.Coverage; c != nil {
		fmt.Fprintf(&b, "Monitors: %s · SLOs: %d · owner: %s\n\n", c.monitorCounts(), len(c.SLOs), orDash(c.Team))
		for _, g := range c.Gaps {
			b.WriteString("- " + g.What + "\n")
			if g.Fix != "" {
				b.WriteString("  ```\n  " + g.Fix + "\n  ```\n")
			}
		}
	}
	return b.String()
}

// hostRead is a host's vital signs over the window.
type hostRead struct {
	Host    string            `json:"host"`
	From    time.Time         `json:"from"`
	To      time.Time         `json:"to"`
	Metrics []*describeResult `json:"metrics"`
}

// hostMetrics are the vitals read for a host: the ones every Datadog Agent
// sends.
var hostMetrics = []string{"avg:system.cpu.user{%s}", "avg:system.mem.pct_usable{%s}", "avg:system.load.norm.1{%s}"}

func readHost(host string, from, to time.Time) *hostRead {
	hr := &hostRead{Host: host, From: from, To: to}
	for _, q := range hostMetrics {
		if res, err := describeMetric(fmt.Sprintf(q, "host:"+host), from, to, "", 3); err == nil && len(res.Series) > 0 {
			hr.Metrics = append(hr.Metrics, res)
		}
	}
	return hr
}

func (hr *hostRead) header() string { return "Host " + hr.Host + " · " + fmtWindow(hr.From, hr.To) }

func (hr *hostRead) text(tty bool) string {
	var b strings.Builder
	title := hr.header()
	if tty {
		title = ui.Title.Render(title)
	}
	b.WriteString(title + "\n")
	if len(hr.Metrics) == 0 {
		b.WriteString("  no system metrics for this host in the window\n")
	}
	for _, m := range hr.Metrics {
		b.WriteString("\n" + m.text())
	}
	return b.String()
}

func (hr *hostRead) markdown() string {
	var b strings.Builder
	b.WriteString("## " + hr.header() + "\n\n")
	for _, m := range hr.Metrics {
		b.WriteString(m.markdown() + "\n")
	}
	return b.String()
}

func orDash(s string) string {
	if s == "" {
		return "–"
	}
	return s
}

func init() {
	readWin.register(readCmd, 4*time.Hour)
	readCmd.Flags().BoolVar(&readJSON, "json", false, "Output as JSON")
	readCmd.Flags().BoolVar(&readMD, "md", false, "Output as markdown")
	rootCmd.AddCommand(readCmd)
}

// spanGroupCount is one service + resource in a spans search.
type spanGroupCount struct {
	Service  string `json:"service"`
	Resource string `json:"resource"`
	Count    int    `json:"count"`
	Errors   int    `json:"errors"`
}

type spanGroupsReport struct {
	Query  string           `json:"query"`
	From   time.Time        `json:"from"`
	To     time.Time        `json:"to"`
	Total  int              `json:"total"`
	Errors int              `json:"errors"`
	Groups []spanGroupCount `json:"groups"`
}

// spanPatterns sums up a spans search: which service and resource the
// spans come from, and how many failed.
func spanPatterns(query string, from, to time.Time) (*spanGroupsReport, error) {
	if strings.TrimSpace(query) == "" {
		query = "*"
	}
	buckets, err := client.AggregateSpans(query, from.UTC().Format(time.RFC3339), to.UTC().Format(time.RFC3339), []string{"service", "resource_name", "status"}, 30)
	if err != nil {
		return nil, err
	}
	rep := &spanGroupsReport{Query: query, From: from, To: to}
	idx := map[string]int{}
	for _, b := range buckets {
		k := b.By["service"] + "\x00" + b.By["resource_name"]
		i, ok := idx[k]
		if !ok {
			i = len(rep.Groups)
			idx[k] = i
			rep.Groups = append(rep.Groups, spanGroupCount{Service: b.By["service"], Resource: b.By["resource_name"]})
		}
		rep.Groups[i].Count += b.Count
		rep.Total += b.Count
		if b.By["status"] == "error" {
			rep.Groups[i].Errors += b.Count
			rep.Errors += b.Count
		}
	}
	sort.SliceStable(rep.Groups, func(i, j int) bool { return rep.Groups[i].Count > rep.Groups[j].Count })
	if len(rep.Groups) > 25 {
		rep.Groups = rep.Groups[:25]
	}
	return rep, nil
}

func (r *spanGroupsReport) header() string {
	return fmt.Sprintf("Spans · %s · %s · %s spans, %s errors", r.Query, fmtWindow(r.From, r.To), fmtCount(r.Total), fmtCount(r.Errors))
}

func (r *spanGroupsReport) text(tty bool) string {
	var b strings.Builder
	title := r.header()
	if tty {
		title = ui.Title.Render(title)
	}
	b.WriteString(title + "\n\n")
	fmt.Fprintf(&b, "  %9s %6s  %-20s %s\n", "SPANS", "ERR%", "SERVICE", "RESOURCE")
	for _, g := range r.Groups {
		fmt.Fprintf(&b, "  %9s %5.1f%%  %-20s %s\n", fmtCount(g.Count), float64(g.Errors)/float64(max(1, g.Count))*100, truncRunes(g.Service, 20), truncRunes(g.Resource, 90))
	}
	return b.String()
}

func (r *spanGroupsReport) markdown() string {
	var b strings.Builder
	b.WriteString("**" + r.header() + "**\n\n| spans | err % | service | resource |\n|---:|---:|---|---|\n")
	for _, g := range r.Groups {
		fmt.Fprintf(&b, "| %s | %.1f%% | %s | `%s` |\n", fmtCount(g.Count), float64(g.Errors)/float64(max(1, g.Count))*100, g.Service, strings.ReplaceAll(g.Resource, "|", `\|`))
	}
	return b.String()
}
