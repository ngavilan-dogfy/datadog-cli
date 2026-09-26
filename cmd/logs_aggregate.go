package cmd

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/ngavilan-dogfy/datadog-cli/datadog"
	"github.com/ngavilan-dogfy/datadog-cli/ui"

	"github.com/spf13/cobra"
)

var (
	logsAggJSON    bool
	logsAggPlain   bool
	logsAggMinutes int
	logsAggFrom    string
	logsAggTo      string
	logsAggGroup   []string
	logsAggLimit   int
)

var logsAggregateCmd = &cobra.Command{
	Use:   "aggregate <query>",
	Short: "Aggregate (count + groupBy) over logs",
	Long: `Run a count() aggregation on logs, optionally grouped by one or more facets.
Useful for answering "how many 5xx by service?" in one call.

Examples:
  datadog logs aggregate "@http.status_code:>=500"                          # total count
  datadog logs aggregate "*" --groupby service                              # logs per service
  datadog logs aggregate "@http.status_code:>=500" --groupby service        # 5xx per service
  datadog logs aggregate "service:api" --groupby @http.status_code,@env     # multi-facet
  datadog logs aggregate "service:api" --groupby service --minutes 60       # last hour
  datadog logs aggregate "service:api" --json | jq '.[] | {by, count}'`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		query := args[0]
		from := logsAggFrom
		to := logsAggTo
		if from == "" {
			from = time.Now().Add(-time.Duration(logsAggMinutes) * time.Minute).Format(time.RFC3339)
		}
		if to == "" {
			to = time.Now().Format(time.RFC3339)
		}

		// Accept either comma-separated or repeated --groupby
		var groups []string
		for _, g := range logsAggGroup {
			for _, p := range strings.Split(g, ",") {
				p = strings.TrimSpace(p)
				if p != "" {
					groups = append(groups, p)
				}
			}
		}

		res, err := client.AggregateLogs(query, from, to, groups, logsAggLimit)
		if err != nil {
			return err
		}

		rows := flattenAggregateBuckets(res, groups)

		// Sort by count desc (already done by API for ordered groupby, but safe)
		sort.SliceStable(rows, func(i, j int) bool { return rows[i].Count > rows[j].Count })

		if logsAggJSON {
			return printJSON(rows)
		}
		if !isTTY() || logsAggPlain {
			headers := append([]string{}, groups...)
			headers = append(headers, "COUNT")
			tsv := make([][]string, 0, len(rows))
			for _, r := range rows {
				row := make([]string, 0, len(groups)+1)
				for _, g := range groups {
					row = append(row, r.By[g])
				}
				row = append(row, fmt.Sprintf("%d", r.Count))
				tsv = append(tsv, row)
			}
			if len(groups) == 0 {
				headers = []string{"COUNT"}
			}
			printTSV(headers, tsv)
			return nil
		}

		// Rich table
		fmt.Println(ui.Title.Render(fmt.Sprintf(" logs aggregate · %s (%s → %s)", query,
			compactTime(from), compactTime(to))))
		if len(rows) == 0 {
			fmt.Println(ui.Dimmed.Render("  No matches."))
			return nil
		}
		for _, r := range rows {
			label := "(total)"
			if len(groups) > 0 {
				parts := make([]string, 0, len(groups))
				for _, g := range groups {
					v := r.By[g]
					if v == "" {
						v = "—"
					}
					parts = append(parts, fmt.Sprintf("%s=%s", g, v))
				}
				label = strings.Join(parts, "  ")
			}
			fmt.Printf("  %s  %s\n",
				ui.Subtitle.Render(fmt.Sprintf("%d", r.Count)),
				ui.Dimmed.Render(label))
		}
		return nil
	},
}

type aggregateRow struct {
	By    map[string]string `json:"by"`
	Count int64             `json:"count"`
}

func flattenAggregateBuckets(res *datadog.LogsAggregateResponse, groups []string) []aggregateRow {
	if res == nil || len(res.Data.Buckets) == 0 {
		return nil
	}
	out := make([]aggregateRow, 0, len(res.Data.Buckets))
	for _, b := range res.Data.Buckets {
		row := aggregateRow{By: b.By}
		// "c0" is the alias DD uses for the first compute. Fall back to any key.
		for _, v := range b.Computes {
			if cnt, ok := v.(float64); ok {
				row.Count = int64(cnt)
				break
			}
		}
		out = append(out, row)
	}
	return out
}

func compactTime(s string) string {
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return s
	}
	return t.Format("15:04")
}

func init() {
	logsAggregateCmd.Flags().BoolVar(&logsAggJSON, "json", false, "Output as JSON")
	logsAggregateCmd.Flags().BoolVar(&logsAggPlain, "plain", false, "Force TSV output")
	logsAggregateCmd.Flags().IntVarP(&logsAggMinutes, "minutes", "m", 15, "Look-back window")
	logsAggregateCmd.Flags().StringVar(&logsAggFrom, "from", "", "Custom start (RFC3339)")
	logsAggregateCmd.Flags().StringVar(&logsAggTo, "to", "", "Custom end (RFC3339)")
	logsAggregateCmd.Flags().StringSliceVar(&logsAggGroup, "groupby", nil, "Facet(s) to group by (repeat or comma-separate)")
	logsAggregateCmd.Flags().IntVar(&logsAggLimit, "limit", 50, "Max rows per group")
	logsCmd.AddCommand(logsAggregateCmd)
}
