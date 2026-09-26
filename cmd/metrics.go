package cmd

import (
	"fmt"
	"strings"
	"time"

	"github.com/ngavilan-dogfy/datadog-cli/datadog"
	"github.com/ngavilan-dogfy/datadog-cli/ui"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/lipgloss/table"
	"github.com/spf13/cobra"
)

var (
	metricsJSON    bool
	metricsPlain   bool
	metricsMinutes int
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

Examples:
  datadog metrics query "avg:system.cpu.user{*}"
  datadog metrics query "avg:system.cpu.user{host:web-01}" --minutes 60
  datadog metrics query "sum:http.requests{service:api}.as_count()" --json`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		query := args[0]

		to := time.Now().Unix()
		from := to - int64(metricsMinutes*60)

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

		return printMetricsTable(result, query)
	},
}

// --- helpers ---

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

func printMetricsTable(result *datadog.MetricsQueryResponse, query string) error {
	fmt.Println(ui.Title.Render(fmt.Sprintf(" Metrics · %s · last %dm", query, metricsMinutes)))

	for _, s := range result.Series {
		scope := s.Scope
		if scope == "" {
			scope = s.Expression
		}
		unit := ""
		if len(s.Unit) > 0 && s.Unit[0].Name != "" {
			unit = " " + s.Unit[0].Name
		}

		fmt.Println()
		fmt.Println(ui.Subtitle.Render(fmt.Sprintf("  %s [%s]", s.Metric, scope)))

		// Show summary stats
		if len(s.Pointlist) > 0 {
			var min, max, sum float64
			min = s.Pointlist[0][1]
			max = s.Pointlist[0][1]
			for _, pt := range s.Pointlist {
				if len(pt) >= 2 {
					v := pt[1]
					if v < min {
						min = v
					}
					if v > max {
						max = v
					}
					sum += v
				}
			}
			avg := sum / float64(len(s.Pointlist))

			statsStyle := lipgloss.NewStyle().PaddingLeft(4)
			fmt.Println(statsStyle.Render(fmt.Sprintf("min: %.4f%s  avg: %.4f%s  max: %.4f%s  points: %d",
				min, unit, avg, unit, max, unit, len(s.Pointlist))))
		}

		// Show last N points as table
		points := s.Pointlist
		maxShow := 20
		if len(points) > maxShow {
			points = points[len(points)-maxShow:]
		}

		var rows [][]string
		for _, pt := range points {
			if len(pt) >= 2 {
				ts := time.Unix(int64(pt[0])/1000, 0).Format("15:04:05")
				val := fmt.Sprintf("%.4f%s", pt[1], unit)
				// Simple bar
				bar := renderBar(pt[1], min(points), max(points), 20)
				rows = append(rows, []string{ts, val, bar})
			}
		}

		t := table.New().
			Headers("TIME", "VALUE", "").
			Rows(rows...).
			Border(lipgloss.RoundedBorder()).
			BorderStyle(lipgloss.NewStyle().Foreground(ui.Subtle)).
			StyleFunc(func(row, col int) lipgloss.Style {
				if row == table.HeaderRow {
					return lipgloss.NewStyle().Bold(true).Foreground(ui.Secondary).Padding(0, 1)
				}
				s := lipgloss.NewStyle().Padding(0, 1)
				switch col {
				case 0:
					s = s.Foreground(ui.Muted).Width(10)
				case 1:
					s = s.Foreground(ui.Text).Width(18)
				case 2:
					s = s.Foreground(ui.Success)
				}
				return s
			})

		fmt.Println(t)
	}

	fmt.Println()
	fmt.Println(ui.Dimmed.Render(fmt.Sprintf("  %d series", len(result.Series))))
	return nil
}

func renderBar(val, minVal, maxVal float64, width int) string {
	if maxVal == minVal {
		return strings.Repeat("█", width/2)
	}
	ratio := (val - minVal) / (maxVal - minVal)
	filled := int(ratio * float64(width))
	if filled < 1 {
		filled = 1
	}
	return strings.Repeat("█", filled)
}

func min(points [][]float64) float64 {
	if len(points) == 0 {
		return 0
	}
	m := points[0][1]
	for _, pt := range points {
		if len(pt) >= 2 && pt[1] < m {
			m = pt[1]
		}
	}
	return m
}

func max(points [][]float64) float64 {
	if len(points) == 0 {
		return 0
	}
	m := points[0][1]
	for _, pt := range points {
		if len(pt) >= 2 && pt[1] > m {
			m = pt[1]
		}
	}
	return m
}

func init() {
	metricsSearchCmd.Flags().BoolVar(&metricsJSON, "json", false, "Output as JSON array")
	metricsSearchCmd.Flags().BoolVar(&metricsPlain, "plain", false, "Force plain output")

	metricsQueryCmd.Flags().BoolVar(&metricsJSON, "json", false, "Output as JSON")
	metricsQueryCmd.Flags().BoolVar(&metricsPlain, "plain", false, "Force plain TSV output")
	metricsQueryCmd.Flags().IntVar(&metricsMinutes, "minutes", 60, "Minutes of history to query (default 60)")

	metricsCmd.AddCommand(metricsSearchCmd)
	metricsCmd.AddCommand(metricsQueryCmd)
	rootCmd.AddCommand(metricsCmd)
}
