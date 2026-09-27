package cmd

import (
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/ngavilan-dogfy/datadog-cli/tui"
	"github.com/ngavilan-dogfy/datadog-cli/ui"

	"github.com/spf13/cobra"
)

var (
	dreadWin      windowFlags
	dreadFile     string
	dreadVars     []string
	dreadJSON     bool
	dreadMD       bool
	dreadTop      int
	dreadProblems bool
)

var dashboardsReadCmd = &cobra.Command{
	Use:   "read [<dashboard-id | link>]",
	Short: "Read a dashboard as text: what every widget shows now, and what's wrong with it",
	Long: `Fetch every widget of a dashboard and say what it shows: each chart line
described (its level, changes, peaks, gaps), the numbers of query values and
top lists, monitors in alert, log patterns, notes. And what's wrong: widgets
that fail, show no data (with the likely reason: a tag value that doesn't
exist…), are always 0 or unreadable, plus the static checks of 'dashboards
lint'. Each widget carries its path in the JSON (widgets[2].definition.widgets[0]).

Made for agents, who can't look at charts: read a dashboard to understand a
system, or check a dashboard JSON you're writing (--file) against real data
before creating it — then preview it with 'datadog ui --file'.

A dashboard link keeps its template variables and time window.

Output:
  Terminal: widget by widget · --md: markdown · --json: everything, with numbers

Examples:
  datadog dashboards read abc-def-ghi
  datadog dashboards read "https://app.datadoghq.eu/dashboard/abc-def-ghi/api?tpl_var_env=production"
  datadog dashboards read abc-def-ghi --var env=staging --since 1d --md
  datadog dashboards read --file new-dashboard.json --problems   # check before 'dashboards create'`,
	Args: cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		var raw map[string]interface{}
		opts := tui.ReadOptions{Vars: map[string]string{}, Top: dreadTop}
		from, to, err := dreadWin.resolve()
		if err != nil {
			return err
		}
		opts.Span, opts.End = to.Sub(from), to
		if !cmd.Flags().Changed("to") && !cmd.Flags().Changed("from") {
			opts.End = time.Time{}
		}
		switch {
		case dreadFile != "":
			if raw, err = readDashboardFile(dreadFile); err != nil {
				return err
			}
		default:
			id := ""
			if len(args) > 0 {
				id = args[0]
				if l, ok := parseDDLink(id); ok {
					if l.Kind != "dashboard" {
						return fmt.Errorf("that's a %s link, not a dashboard: try 'datadog read' with it", l.Kind)
					}
					id = l.ID
					for k, v := range l.Vars {
						opts.Vars[k] = v
					}
					if span, end, ok := l.window(); ok && !windowChanged(cmd) {
						opts.Span, opts.End = span, end
					}
				}
			}
			if id, err = pickDashboardID([]string{id}); err != nil {
				return err
			}
			if raw, err = client.GetDashboard(id); err != nil {
				return err
			}
		}
		for _, kv := range dreadVars {
			k, v, ok := strings.Cut(kv, "=")
			if !ok {
				return fmt.Errorf("--var %q: use name=value", kv)
			}
			opts.Vars[strings.TrimPrefix(k, "$")] = v
		}
		rep := readDashboard(raw, opts)
		if dreadProblems {
			rep.Widgets = withProblems(rep.Widgets)
		}
		switch {
		case dreadJSON:
			return printJSON(rep)
		case dreadMD:
			fmt.Print(dashReportMarkdown(rep))
			return nil
		}
		fmt.Print(dashReportText(rep, isTTY()))
		return nil
	},
}

func windowChanged(cmd *cobra.Command) bool {
	for _, f := range []string{"since", "from", "to", "around", "window"} {
		if cmd.Flags().Changed(f) {
			return true
		}
	}
	return false
}

// readDashboard is tui.ReadDashboard plus what the CLI knows on top: why a
// metric shows no data, and the static lint findings.
func readDashboard(raw map[string]interface{}, opts tui.ReadOptions) *tui.DashboardReport {
	rep := tui.ReadDashboard(client, raw, opts)
	for i := range rep.Widgets {
		w := &rep.Widgets[i]
		if !hasProblem(w.Problems, "no data") {
			continue
		}
		for _, q := range w.Queries {
			if h := noDataHint(reQueryName.ReplaceAllString(q, "")); h != "" {
				for _, line := range strings.Split(h, "\n") {
					w.Problems = append(w.Problems, "→ "+line)
				}
			}
		}
	}
	byPath := map[string]*tui.WidgetReport{}
	for i := range rep.Widgets {
		byPath[rep.Widgets[i].Path] = &rep.Widgets[i]
	}
	// Lint warnings are problems; its info findings, hints.
	for _, f := range lintDashboard(raw) {
		w := byPath[f.WidgetPath]
		switch {
		case w != nil && f.Severity == "info":
			w.Hints = append(w.Hints, f.Message)
		case w != nil:
			w.Problems = append(w.Problems, f.Message)
		case f.Severity != "info":
			rep.Problems = append(rep.Problems, f.Message)
		}
	}
	return rep
}

var reQueryName = regexp.MustCompile(`^\w+ = `)

func hasProblem(ps []string, prefix string) bool {
	for _, p := range ps {
		if strings.HasPrefix(p, prefix) {
			return true
		}
	}
	return false
}

func withProblems(ws []tui.WidgetReport) []tui.WidgetReport {
	var out []tui.WidgetReport
	for _, w := range ws {
		if len(w.Problems) > 0 {
			out = append(out, w)
		}
	}
	return out
}

func dashReportHeader(rep *tui.DashboardReport) string {
	s := rep.Title
	if rep.ID != "" {
		s += " (" + rep.ID + ")"
	}
	s += " · " + fmtWindow(rep.From, rep.To)
	var vars []string
	for _, v := range rep.Variables {
		vars = append(vars, "$"+v.Name+"="+v.Value)
	}
	if len(vars) > 0 {
		s += " · " + strings.Join(vars, " ")
	}
	return s
}

// widgetBody is what a widget showed, one line each, without its header.
func widgetBody(w tui.WidgetReport) []string {
	var lines []string
	for _, q := range w.Queries {
		lines = append(lines, "query: "+q)
	}
	for _, s := range w.Series {
		if len(w.Series) == 1 && len(w.Queries) == 1 && s.Name == w.Queries[0] {
			lines = append(lines, s.Text) // named after the query, just above
			continue
		}
		lines = append(lines, s.Name+": "+s.Text)
	}
	if w.MoreSeries > 0 {
		lines = append(lines, fmt.Sprintf("… and %d more lines", w.MoreSeries))
	}
	for _, v := range w.Values {
		line := v.Text
		if v.Label != "" {
			line = v.Label + ": " + v.Text
		}
		if v.Change != "" {
			line += " (" + v.Change + ")"
		}
		if v.State != "" {
			line += " [" + v.State + "]"
		}
		lines = append(lines, line)
	}
	if w.MoreValues > 0 {
		lines = append(lines, fmt.Sprintf("… and %d more rows", w.MoreValues))
	}
	if m := w.Monitors; m != nil {
		var parts []string
		for _, k := range sortedCountKeys(m.Counts) {
			parts = append(parts, fmt.Sprintf("%d %s", m.Counts[k], k))
		}
		lines = append(lines, "monitors: "+strings.Join(parts, ", "))
		for i, a := range m.Alerting {
			if i == 8 {
				lines = append(lines, fmt.Sprintf("… and %d more", len(m.Alerting)-8))
				break
			}
			lines = append(lines, a)
		}
	}
	if w.LogsRead > 0 {
		lines = append(lines, fmt.Sprintf("latest %d logs:", w.LogsRead))
		for _, p := range w.Patterns {
			lines = append(lines, fmt.Sprintf("  ×%d %s %s", p.Count, p.Status, p.Pattern))
		}
	}
	if w.Text != "" {
		lines = append(lines, w.Text)
	}
	if w.Partial != "" {
		lines = append(lines, "partly failed: "+w.Partial)
	}
	if w.Skipped != "" {
		lines = append(lines, "not read: "+w.Skipped)
	}
	return lines
}

func widgetName(w tui.WidgetReport) string {
	title := w.Title
	switch {
	case title == "" && w.Type == "note":
		title = "Note"
	case title == "":
		title = "(untitled)"
	}
	if w.Group != "" {
		title = w.Group + " › " + title
	}
	return title
}

func dashReportText(rep *tui.DashboardReport, tty bool) string {
	var b strings.Builder
	paint := func(st func(...string) string, s string) string {
		if tty {
			return st(s)
		}
		return s
	}
	b.WriteString(paint(ui.Title.Render, dashReportHeader(rep)) + "\n")
	if rep.URL != "" {
		b.WriteString(paint(ui.Dimmed.Render, rep.URL) + "\n")
	}
	if rep.Description != "" {
		b.WriteString(oneLine(rep.Description, 300) + "\n")
	}
	if len(rep.Problems) > 0 {
		b.WriteString("\n" + paint(ui.ErrorStyle.Render, "Problems:") + "\n")
		for _, p := range rep.Problems {
			b.WriteString("  • " + p + "\n")
		}
	}
	for _, w := range rep.Widgets {
		if w.Type == "group" {
			b.WriteString("\n" + paint(ui.SectionHeader.Render, "▌ "+w.Title) + "\n")
			continue
		}
		b.WriteString("\n" + paint(ui.Subtitle.Render, widgetName(w)) + paint(ui.Dimmed.Render, "  "+w.Type+" · "+w.Path) + "\n")
		for _, l := range widgetBody(w) {
			if strings.HasPrefix(l, "query: ") {
				b.WriteString("  " + paint(ui.Dimmed.Render, l) + "\n")
				continue
			}
			b.WriteString("  " + l + "\n")
		}
		for _, p := range w.Problems {
			b.WriteString("  " + paint(ui.ErrorStyle.Render, "✗ "+p) + "\n")
		}
		for _, h := range w.Hints {
			b.WriteString("  " + paint(ui.Dimmed.Render, "· "+h) + "\n")
		}
	}
	return b.String()
}

func dashReportMarkdown(rep *tui.DashboardReport) string {
	var b strings.Builder
	b.WriteString("## " + dashReportHeader(rep) + "\n\n")
	if rep.URL != "" {
		b.WriteString(rep.URL + "\n\n")
	}
	if rep.Description != "" {
		b.WriteString(oneLine(rep.Description, 500) + "\n\n")
	}
	if len(rep.Problems) > 0 {
		b.WriteString("**Problems**\n\n")
		for _, p := range rep.Problems {
			b.WriteString("- " + p + "\n")
		}
		b.WriteString("\n")
	}
	for _, w := range rep.Widgets {
		if w.Type == "group" {
			b.WriteString("### " + w.Title + "\n\n")
			continue
		}
		fmt.Fprintf(&b, "**%s** (%s, `%s`)\n", widgetName(w), w.Type, w.Path)
		for _, l := range widgetBody(w) {
			if strings.HasPrefix(l, "query: ") {
				b.WriteString("- query: `" + strings.TrimPrefix(l, "query: ") + "`\n")
				continue
			}
			b.WriteString("- " + l + "\n")
		}
		for _, p := range w.Problems {
			b.WriteString("- ✗ " + p + "\n")
		}
		for _, h := range w.Hints {
			b.WriteString("- hint: " + h + "\n")
		}
		b.WriteString("\n")
	}
	return b.String()
}

func init() {
	dreadWin.register(dashboardsReadCmd, 4*time.Hour)
	dashboardsReadCmd.Flags().StringVarP(&dreadFile, "file", "f", "", "Read a dashboard JSON/YAML file instead (- for stdin)")
	dashboardsReadCmd.Flags().StringArrayVar(&dreadVars, "var", nil, "Template variable value, name=value (repeatable)")
	dashboardsReadCmd.Flags().BoolVar(&dreadJSON, "json", false, "Output as JSON")
	dashboardsReadCmd.Flags().BoolVar(&dreadMD, "md", false, "Output as markdown")
	dashboardsReadCmd.Flags().IntVar(&dreadTop, "top", 8, "Lines / rows kept per widget")
	dashboardsReadCmd.Flags().BoolVar(&dreadProblems, "problems", false, "Only widgets with problems")
	dashboardsCmd.AddCommand(dashboardsReadCmd)
}
