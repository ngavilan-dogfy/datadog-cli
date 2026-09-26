package cmd

import (
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/ngavilan-dogfy/datadog-cli/datadog"
	"github.com/ngavilan-dogfy/datadog-cli/internal/uiprefs"
	"github.com/ngavilan-dogfy/datadog-cli/ui"
	"github.com/ngavilan-dogfy/datadog-cli/viz"
	"golang.org/x/term"

	"github.com/charmbracelet/lipgloss"
	"github.com/spf13/cobra"
)

var (
	metricsJSON    bool
	metricsPlain   bool
	metricsMinutes int
	metricsSince   string
	metricsFrom    string
	metricsTo      string
	metricsChart   bool
)

var metricsCmd = &cobra.Command{
	Use:   "metrics",
	Short: "Search and query metrics",
}

var metricsSearchCmd = &cobra.Command{
	Use:   "search <query>",
	Short: "Search metric names",
	Long: `Search for Datadog metric names matching a query.

Examples:
  datadog metrics search "cpu"
  datadog metrics search "system.disk"
  datadog metrics search "aws.ec2" --json`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		query := args[0]

		metrics, err := client.SearchMetrics(query)
		if err != nil {
			return err
		}

		if metricsJSON {
			return printJSON(metrics)
		}

		if len(metrics) == 0 {
			if isTTY() && !metricsPlain {
				fmt.Println(ui.Dimmed.Render(fmt.Sprintf("  No metrics matching %q.", query)))
			}
			return nil
		}

		if !isTTY() || metricsPlain {
			for _, m := range metrics {
				fmt.Println(m)
			}
			return nil
		}

		fmt.Println(ui.Title.Render(fmt.Sprintf(" Metrics · %s", query)))
		for _, m := range metrics {
			fmt.Printf("  %s\n", ui.Key.Render(m))
		}
		fmt.Println()
		fmt.Println(ui.Dimmed.Render(fmt.Sprintf("  %d metrics", len(metrics))))
		return nil
	},
}

var metricsQueryCmd = &cobra.Command{
	Use:   "query <query>",
	Short: "Query timeseries metric data",
	Long: `Query Datadog metrics and display timeseries data.

The query uses Datadog's metrics query syntax.

The window is the last hour unless you say otherwise: --since 4h, or
--from/--to with RFC3339 or epoch timestamps (--to defaults to now).

Examples:
  datadog metrics query "avg:system.cpu.user{*}"
  datadog metrics query "avg:system.cpu.user{host:web-01} by {host}" --since 4h
  datadog metrics query "sum:http.requests{service:api}.as_count()" --json
  datadog metrics query "p95:trace.http.request.duration{service:api}" --from 2026-05-19T04:30:00Z --to 2026-05-19T05:30:00Z`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		query := args[0]

		toT := time.Now()
		if metricsTo != "" {
			t, err := parseDate(metricsTo)
			if err != nil {
				return fmt.Errorf("--to: %w", err)
			}
			toT = t
		}
		fromT := toT.Add(-time.Duration(metricsMinutes) * time.Minute)
		switch {
		case metricsFrom != "":
			f, err := parseDate(metricsFrom)
			if err != nil {
				return fmt.Errorf("--from: %w", err)
			}
			fromT = f
		case metricsSince != "":
			d, err := parseDuration(metricsSince)
			if err != nil {
				return fmt.Errorf("--since: %w", err)
			}
			fromT = toT.Add(-d)
		}
		if !fromT.Before(toT) {
			return fmt.Errorf("the window is empty: --from must be before --to")
		}
		from, to := fromT.Unix(), toT.Unix()

		result, err := client.QueryMetrics(query, from, to)
		if err != nil {
			return err
		}

		if metricsJSON {
			return printJSON(result)
		}

		if len(result.Series) == 0 {
			if isTTY() && !metricsPlain {
				fmt.Println(ui.Dimmed.Render("  No data for this query."))
			}
			return nil
		}

		if !isTTY() || metricsPlain {
			return printMetricsTSV(result.Series)
		}

		return printMetricsTerminal(result, query, from*1000, to*1000)
	},
}

// printMetricsTerminal draws the series as a chart with a legend of
// per-series stats. Point-by-point data is what --plain and --json are for.
func printMetricsTerminal(result *datadog.MetricsQueryResponse, query string, fromMs, toMs int64) error {
	width := 100
	if w, _, err := term.GetSize(int(os.Stdout.Fd())); err == nil && w > 20 {
		width = w - 4
	}
	palette := viz.Palette()
	var series []viz.Series
	var unit viz.Unit
	for i, s := range result.Series {
		pts := make([]viz.Point, 0, len(s.Pointlist))
		for _, p := range s.Pointlist {
			if len(p) == 2 {
				pts = append(pts, viz.Point{T: int64(p[0]), V: p[1]})
			}
		}
		name := s.Scope
		if name == "" || name == "*" {
			name = s.Expression
		}
		series = append(series, viz.Series{Name: name, Color: palette[i%len(palette)], Points: pts})
		if i == 0 && len(s.Unit) > 0 {
			u := s.Unit[0]
			unit = viz.Unit{Family: u.Family, Name: u.Name, Short: u.ShortName, Scale: u.ScaleFactor}
		}
	}
	span := time.Duration(toMs-fromMs) * time.Millisecond
	fmt.Println()
	fmt.Println("  " + lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("5")).Render(query) + ui.Dimmed.Render("  · last "+viz.FormatSpan(span.Round(time.Minute))))
	if metricsChart {
		c := viz.Chart{Width: width, Height: 14, Series: series, Style: uiprefs.VizStyle(), From: fromMs, To: toMs,
			Unit: unit, Cursor: -1, Axis: lipgloss.Color("8")}
		fmt.Println()
		for _, l := range c.Render() {
			fmt.Println("  " + l)
		}
	}
	fmt.Println()
	nameW := 0
	for _, s := range series {
		nameW = max(nameW, len([]rune(s.Name)))
	}
	nameW = min(nameW, max(20, width-60))
	shown := series
	if len(shown) > 12 {
		shown = shown[:12]
	}
	for _, s := range shown {
		lo, hi, sum, n, last := 0.0, 0.0, 0.0, 0, 0.0
		for _, p := range s.Points {
			if n == 0 || p.V < lo {
				lo = p.V
			}
			if n == 0 || p.V > hi {
				hi = p.V
			}
			sum, last = sum+p.V, p.V
			n++
		}
		stats := ui.Dimmed.Render("no data")
		if n > 0 {
			f := func(label string, v float64) string {
				return ui.Dimmed.Render(label+" ") + fmt.Sprintf("%-7s", viz.Format(v, unit))
			}
			stats = f("min", lo) + f(" avg", sum/float64(n)) + f(" max", hi) + f(" last", last)
		}
		name := truncRunes(s.Name, nameW)
		fmt.Printf("  %s %s  %s\n", lipgloss.NewStyle().Foreground(s.Color).Render("■"),
			name+strings.Repeat(" ", nameW-len([]rune(name))), stats)
	}
	if len(series) > len(shown) {
		fmt.Println(ui.Dimmed.Render(fmt.Sprintf("  … and %d more series (see --plain or --json)", len(series)-len(shown))))
	}
	fmt.Println()
	return nil
}

func printMetricsTSV(series []datadog.MetricsSeries) error {
	for _, s := range series {
		scope := s.Scope
		if scope == "" {
			scope = s.Expression
		}
		fmt.Printf("# %s [%s]\n", s.Metric, scope)
		for _, pt := range s.Pointlist {
			if len(pt) >= 2 {
				ts := time.Unix(int64(pt[0])/1000, 0).Format("2006-01-02 15:04:05")
				fmt.Printf("%s\t%.4f\n", ts, pt[1])
			}
		}
	}
	return nil
}

func init() {
	metricsSearchCmd.Flags().BoolVar(&metricsJSON, "json", false, "Output as JSON array")
	metricsSearchCmd.Flags().BoolVar(&metricsPlain, "plain", false, "Force plain output")

	metricsQueryCmd.Flags().BoolVar(&metricsJSON, "json", false, "Output as JSON")
	metricsQueryCmd.Flags().BoolVar(&metricsPlain, "plain", false, "Force plain TSV output")
	metricsQueryCmd.Flags().IntVar(&metricsMinutes, "minutes", 60, "Minutes of history to query")
	metricsQueryCmd.Flags().StringVar(&metricsSince, "since", "", "Lookback window as duration (30m, 4h, 2d) — overrides --minutes")
	metricsQueryCmd.Flags().StringVar(&metricsFrom, "from", "", "Start time (RFC3339 or epoch)")
	metricsQueryCmd.Flags().StringVar(&metricsTo, "to", "", "End time (RFC3339 or epoch; default now)")
	metricsQueryCmd.Flags().BoolVar(&metricsChart, "chart", true, "Draw a chart above the table (terminal only)")

	metricsCmd.AddCommand(metricsSearchCmd)
	metricsCmd.AddCommand(metricsQueryCmd)
	rootCmd.AddCommand(metricsCmd)
}
