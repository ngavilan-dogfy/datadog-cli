package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/ngavilan-dogfy/datadog-cli/datadog"
	"github.com/ngavilan-dogfy/datadog-cli/ui"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/lipgloss/table"
	"github.com/spf13/cobra"
)

var (
	logsJSON     bool
	logsJSONL    bool
	logsPlain    bool
	logsAll      bool
	logsLimit    int
	logsMinutes  int
	logsSince    string
	logsFrom     string
	logsTo       string
	tailInterval int
	tailJSON     bool
)

// logsAllCap bounds --all pagination so a broad query can't run forever.
const logsAllCap = 5000

var logsCmd = &cobra.Command{
	Use:   "logs <query>",
	Short: "Search logs",
	Long: `Search Datadog logs with a query.

By default shows logs from the last 15 minutes.

Output adapts automatically:
  • Terminal  → colored table
  • Piped     → tab-separated plain text (TSV)
  • --json    → structured JSON array

Examples:
  datadog logs "service:api"                       # search logs
  datadog logs "service:api status:error"          # errors only
  datadog logs "service:api" --since 2h            # last 2 hours
  datadog logs "@http.status_code:500" --json      # JSON output (includes tags + attributes)
  datadog logs "env:prod" --limit 100              # more results
  datadog logs "*" --from "2024-01-01T00:00:00Z"   # custom range`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		query := args[0]

		from := logsFrom
		to := logsTo
		if from == "" {
			lookback := time.Duration(logsMinutes) * time.Minute
			if logsSince != "" {
				d, err := parseDuration(logsSince)
				if err != nil {
					return fmt.Errorf("--since: %w", err)
				}
				lookback = d
				logsMinutes = int(d.Minutes())
			}
			from = time.Now().Add(-lookback).Format(time.RFC3339)
		}
		if to == "" {
			to = time.Now().Format(time.RFC3339)
		}

		var logs []datadog.LogData
		if logsAll {
			cursor := ""
			for {
				res, err := client.SearchLogsCursor(query, from, to, 200, cursor)
				if err != nil {
					return err
				}
				logs = append(logs, res.Data...)
				cursor = res.Meta.Page.After
				if cursor == "" || len(logs) >= logsAllCap {
					break
				}
			}
			if len(logs) >= logsAllCap {
				fmt.Fprintf(os.Stderr, "note: output capped at %d logs — narrow the query or window for the rest\n", logsAllCap)
			}
		} else {
			result, err := client.SearchLogs(query, from, to, logsLimit)
			if err != nil {
				return err
			}
			logs = result.Data
		}

		if logsJSONL {
			enc := json.NewEncoder(os.Stdout)
			for _, l := range logsToJSON(logs) {
				if err := enc.Encode(l); err != nil {
					return err
				}
			}
			return nil
		}

		if logsJSON {
			return printJSON(logsToJSON(logs))
		}

		if len(logs) == 0 {
			if isTTY() && !logsPlain {
				fmt.Println(ui.Dimmed.Render("  No logs found."))
			}
			return nil
		}

		if !isTTY() || logsPlain {
			return printLogsTSV(logs)
		}

		return printLogsTable(logs, query)
	},
}

// --- helpers ---

type logJSONOut struct {
	ID         string                 `json:"id"`
	Timestamp  string                 `json:"timestamp"`
	Status     string                 `json:"status"`
	Host       string                 `json:"host"`
	Service    string                 `json:"service"`
	Message    string                 `json:"message"`
	Tags       []string               `json:"tags,omitempty"`
	Attributes map[string]interface{} `json:"attributes,omitempty"`
}

func logsToJSON(logs []datadog.LogData) []logJSONOut {
	out := make([]logJSONOut, len(logs))
	for i, l := range logs {
		out[i] = logJSONOut{
			ID:         l.ID,
			Timestamp:  l.Attributes.Timestamp,
			Status:     l.Attributes.Status,
			Host:       l.Attributes.Host,
			Service:    l.Attributes.Service,
			Message:    l.Attributes.Message,
			Tags:       l.Attributes.Tags,
			Attributes: l.Attributes.Attributes,
		}
	}
	return out
}

func printLogsTSV(logs []datadog.LogData) error {
	headers := []string{"TIMESTAMP", "STATUS", "HOST", "SERVICE", "MESSAGE"}
	var rows [][]string
	for _, l := range logs {
		msg := l.Attributes.Message
		if len(msg) > 120 {
			msg = msg[:117] + "..."
		}
		msg = strings.ReplaceAll(msg, "\n", " ")
		rows = append(rows, []string{
			l.Attributes.Timestamp,
			l.Attributes.Status,
			l.Attributes.Host,
			l.Attributes.Service,
			msg,
		})
	}
	printTSV(headers, rows)
	return nil
}

func printLogsTable(logs []datadog.LogData, query string) error {
	header := fmt.Sprintf(" Logs · %s · last %dm", query, logsMinutes)
	fmt.Println(ui.Title.Render(header))

	var rows [][]string
	for _, l := range logs {
		ts := l.Attributes.Timestamp
		if t := datadog.ParseTime(ts); !t.IsZero() {
			ts = t.Format("15:04:05")
		}
		host := l.Attributes.Host
		if len(host) > 20 {
			host = host[:17] + "..."
		}
		service := l.Attributes.Service
		if len(service) > 15 {
			service = service[:12] + "..."
		}
		msg := l.Attributes.Message
		if len(msg) > 70 {
			msg = msg[:67] + "..."
		}
		msg = strings.ReplaceAll(msg, "\n", " ")

		rows = append(rows, []string{
			ts,
			l.Attributes.Status,
			host,
			service,
			msg,
		})
	}

	t := table.New().
		Headers("TIME", "STATUS", "HOST", "SERVICE", "MESSAGE").
		Rows(rows...).
		Border(lipgloss.RoundedBorder()).
		BorderStyle(lipgloss.NewStyle().Foreground(ui.Subtle)).
		StyleFunc(func(row, col int) lipgloss.Style {
			if row == table.HeaderRow {
				return lipgloss.NewStyle().Bold(true).Foreground(ui.Secondary).Padding(0, 1)
			}
			s := lipgloss.NewStyle().Padding(0, 1)
			switch col {
			case 0: // TIME
				s = s.Foreground(ui.Muted).Width(10)
			case 1: // STATUS
				if row >= 0 && row < len(logs) {
					s = s.Foreground(ui.LogStatusColor(logs[row].Attributes.Status)).Bold(true).Width(8)
				}
			case 2: // HOST
				s = s.Foreground(ui.Muted).Width(22)
			case 3: // SERVICE
				s = s.Foreground(ui.Secondary).Width(17)
			case 4: // MESSAGE
				s = s.Foreground(ui.Text).Width(72)
			}
			return s
		})

	fmt.Println(t)
	fmt.Println(ui.Dimmed.Render(fmt.Sprintf("  %d logs", len(logs))))
	return nil
}

var logsTailCmd = &cobra.Command{
	Use:   "tail <query>",
	Short: "Live tail logs (continuous polling)",
	Long: `Continuously poll for new logs matching a query, similar to tail -f.

Polls every 5 seconds by default. Shows new logs as they arrive.

Examples:
  datadog logs tail "service:api"
  datadog logs tail "service:api status:error"
  datadog logs tail "env:prod" --interval 2
  datadog logs tail "*" --json`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		query := args[0]
		interval := time.Duration(tailInterval) * time.Second
		return tailLogs(query, interval, tailJSON)
	},
}

func init() {
	logsCmd.Flags().BoolVar(&logsJSON, "json", false, "Output as JSON array")
	logsCmd.Flags().BoolVar(&logsJSONL, "jsonl", false, "Output as JSON lines (one object per line, streaming-friendly)")
	logsCmd.Flags().BoolVar(&logsAll, "all", false, fmt.Sprintf("Paginate through all results (capped at %d)", logsAllCap))
	logsCmd.Flags().BoolVar(&logsPlain, "plain", false, "Force plain TSV output")
	logsCmd.Flags().IntVarP(&logsLimit, "limit", "n", 25, "Maximum number of results")
	logsCmd.Flags().IntVar(&logsMinutes, "minutes", 15, "Minutes of history to search (default 15)")
	logsCmd.Flags().StringVar(&logsSince, "since", "", "Lookback window as duration (30m, 2h, 1d) — overrides --minutes")
	logsCmd.Flags().StringVar(&logsFrom, "from", "", "Start time (RFC3339)")
	logsCmd.Flags().StringVar(&logsTo, "to", "", "End time (RFC3339)")

	logsTailCmd.Flags().IntVar(&tailInterval, "interval", 5, "Poll interval in seconds (default 5)")
	logsTailCmd.Flags().BoolVar(&tailJSON, "json", false, "Output as JSON lines")

	logsCmd.AddCommand(logsTailCmd)
	rootCmd.AddCommand(logsCmd)
}
