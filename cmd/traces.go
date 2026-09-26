package cmd

import (
	"fmt"
	"strings"
	"time"

	"github.com/ngavilan-dogfy/datadog-cli/datadog"
	"github.com/ngavilan-dogfy/datadog-cli/ui"

	"github.com/spf13/cobra"
)

var (
	tracesJSON       bool
	tracesPlain      bool
	tracesLimit      int
	tracesMinutes    int
	tracesFrom       string
	tracesTo         string
	tracesErrorsOnly bool
)

var tracesCmd = &cobra.Command{
	Use:   "traces <query>",
	Short: "Search APM spans/traces",
	Long: `Search Datadog APM spans with a query (v2 endpoint).

By default shows spans from the last 15 minutes.

Examples:
  datadog traces "service:api"                       # all api spans
  datadog traces "service:api status:error"          # error spans only
  datadog traces "service:api -resource:GET /health" # exclude noise
  datadog traces "@duration:>1s service:api"         # slow spans
  datadog traces "*" --errors-only --minutes 60      # last 1h error spans
  datadog traces "service:checkout" --json | jq '.[].trace_id'`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		query := args[0]
		if tracesErrorsOnly {
			query = "(" + query + ") status:error"
		}

		from := tracesFrom
		to := tracesTo
		if from == "" {
			from = time.Now().Add(-time.Duration(tracesMinutes) * time.Minute).Format(time.RFC3339)
		}
		if to == "" {
			to = time.Now().Format(time.RFC3339)
		}

		result, err := client.SearchSpans(query, from, to, tracesLimit)
		if err != nil {
			return err
		}

		if tracesJSON {
			return printJSON(spansToJSON(result.Data))
		}
		if len(result.Data) == 0 {
			if isTTY() && !tracesPlain {
				fmt.Println(ui.Dimmed.Render("  No spans found."))
			}
			return nil
		}
		if !isTTY() || tracesPlain {
			return printSpansTSV(result.Data)
		}
		return printSpansTable(result.Data, query)
	},
}

type spanJSONOut struct {
	Timestamp  string  `json:"timestamp"`
	TraceID    string  `json:"trace_id"`
	SpanID     string  `json:"span_id"`
	Service    string  `json:"service"`
	Resource   string  `json:"resource"`
	Operation  string  `json:"operation"`
	DurationMS float64 `json:"duration_ms"`
	Status     string  `json:"status"`
	Env        string  `json:"env"`
	Host       string  `json:"host"`
}

func spansToJSON(spans []datadog.SpanData) []spanJSONOut {
	out := make([]spanJSONOut, len(spans))
	for i, s := range spans {
		out[i] = spanJSONOut{
			Timestamp:  s.Attributes.Timestamp(),
			TraceID:    s.Attributes.TraceID(),
			SpanID:     s.Attributes.SpanID,
			Service:    s.Attributes.Service,
			Resource:   s.Attributes.ResourceName,
			Operation:  s.Attributes.OperationName,
			DurationMS: float64(s.Attributes.DurationNS()) / 1e6,
			Status:     s.Attributes.Status,
			Env:        s.Attributes.Env,
			Host:       s.Attributes.Host,
		}
	}
	return out
}

func printSpansTSV(spans []datadog.SpanData) error {
	fmt.Println("TIMESTAMP\tSERVICE\tRESOURCE\tDURATION_MS\tSTATUS\tTRACE_ID")
	for _, s := range spans {
		ms := float64(s.Attributes.DurationNS()) / 1e6
		fmt.Printf("%s\t%s\t%s\t%.2f\t%s\t%s\n",
			s.Attributes.Timestamp(),
			s.Attributes.Service,
			strings.ReplaceAll(s.Attributes.ResourceName, "\t", " "),
			ms,
			s.Attributes.Status,
			s.Attributes.TraceID())
	}
	return nil
}

func printSpansTable(spans []datadog.SpanData, query string) error {
	fmt.Println(ui.Title.Render(fmt.Sprintf(" APM spans · %s · %d hits", query, len(spans))))
	for _, s := range spans {
		ms := float64(s.Attributes.DurationNS()) / 1e6
		stateBadge := "OK"
		if strings.EqualFold(s.Attributes.Status, "error") {
			stateBadge = "Alert"
		}
		fmt.Printf("  %s  %s  %s\n      %s  %s\n",
			ui.MonitorStateBadge(stateBadge),
			ui.Subtitle.Render(s.Attributes.Service),
			s.Attributes.ResourceName,
			ui.Dimmed.Render(fmt.Sprintf("%.1fms  %s", ms, s.Attributes.Timestamp())),
			ui.Dimmed.Render("trace="+s.Attributes.TraceID()))
	}
	return nil
}

func init() {
	tracesCmd.Flags().BoolVar(&tracesJSON, "json", false, "Output as JSON")
	tracesCmd.Flags().BoolVar(&tracesPlain, "plain", false, "Force TSV output")
	tracesCmd.Flags().IntVarP(&tracesLimit, "limit", "n", 25, "Maximum number of spans")
	tracesCmd.Flags().IntVarP(&tracesMinutes, "minutes", "m", 15, "Look-back window (minutes)")
	tracesCmd.Flags().StringVar(&tracesFrom, "from", "", "Custom start (RFC3339)")
	tracesCmd.Flags().StringVar(&tracesTo, "to", "", "Custom end (RFC3339)")
	tracesCmd.Flags().BoolVar(&tracesErrorsOnly, "errors-only", false, "Only spans with status:error")
	rootCmd.AddCommand(tracesCmd)
}
