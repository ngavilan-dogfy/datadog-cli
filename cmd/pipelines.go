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
	pipelinesJSON    bool
	pipelinesPlain   bool
	pipelinesLimit   int
	pipelinesMinutes int
)

var pipelinesCmd = &cobra.Command{
	Use:     "pipelines",
	Aliases: []string{"ci"},
	Short:   "List CI pipeline events (CI Visibility)",
	Long: `List CI pipeline events from Datadog CI Visibility.

Requires CI Visibility to be enabled in your Datadog organization.

Output adapts automatically:
  • Terminal  → colored table
  • Piped     → tab-separated plain text (TSV)
  • --json    → structured JSON array

Examples:
  datadog pipelines                        # recent pipelines
  datadog ci                               # alias
  datadog ci --minutes 1440                # last 24h
  datadog ci --json | jq '.[].name'        # extract names`,
	Args: cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		query := "*"
		if len(args) > 0 {
			query = args[0]
		}

		from := time.Now().Add(-time.Duration(pipelinesMinutes) * time.Minute).Format(time.RFC3339)
		to := time.Now().Format(time.RFC3339)

		events, err := client.SearchPipelines(query, from, to, pipelinesLimit)
		if err != nil {
			return err
		}

		if pipelinesJSON {
			return printJSON(events)
		}

		if len(events) == 0 {
			if isTTY() && !pipelinesPlain {
				fmt.Println(ui.Dimmed.Render("  No pipeline events found."))
			}
			return nil
		}

		if !isTTY() || pipelinesPlain {
			return printPipelinesTSV(events)
		}

		return printPipelinesTable(events)
	},
}

// --- helpers ---

func pipelineStatusColor(status string) lipgloss.Color {
	switch status {
	case "success":
		return ui.Success
	case "error":
		return ui.Danger
	case "canceled":
		return ui.Warning
	case "skipped":
		return ui.Muted
	default:
		return ui.Text
	}
}

func formatNanoDuration(ns *int64) string {
	if ns == nil {
		return "—"
	}
	d := time.Duration(*ns) * time.Nanosecond
	if d < time.Second {
		return fmt.Sprintf("%dms", d.Milliseconds())
	}
	if d < time.Minute {
		return fmt.Sprintf("%.1fs", d.Seconds())
	}
	m := int(d.Minutes())
	s := int(d.Seconds()) % 60
	return fmt.Sprintf("%dm%ds", m, s)
}

func printPipelinesTSV(events []datadog.CIPipelineEvent) error {
	headers := []string{"STATUS", "LEVEL", "NAME", "SERVICE", "DURATION", "START"}
	var rows [][]string
	for _, e := range events {
		rows = append(rows, []string{
			e.Attributes.Status,
			e.Attributes.Level,
			e.Attributes.Name,
			e.Attributes.Service,
			formatNanoDuration(e.Attributes.Duration),
			e.Attributes.Start,
		})
	}
	printTSV(headers, rows)
	return nil
}

func printPipelinesTable(events []datadog.CIPipelineEvent) error {
	fmt.Println(ui.Title.Render(fmt.Sprintf(" CI Pipelines · last %dm", pipelinesMinutes)))

	var rows [][]string
	for _, e := range events {
		name := e.Attributes.Name
		if len(name) > 35 {
			name = name[:32] + "..."
		}
		service := e.Attributes.Service
		if len(service) > 20 {
			service = service[:17] + "..."
		}
		ts := e.Attributes.Start
		if t := datadog.ParseTime(ts); !t.IsZero() {
			ts = t.Format("15:04:05")
		}

		errMsg := ""
		if e.Attributes.Error != nil && e.Attributes.Error.Message != "" {
			errMsg = e.Attributes.Error.Message
			if len(errMsg) > 25 {
				errMsg = errMsg[:22] + "..."
			}
			errMsg = strings.ReplaceAll(errMsg, "\n", " ")
		}

		rows = append(rows, []string{
			e.Attributes.Status,
			e.Attributes.Level,
			name,
			service,
			formatNanoDuration(e.Attributes.Duration),
			ts,
			errMsg,
		})
	}

	t := table.New().
		Headers("STATUS", "LEVEL", "NAME", "SERVICE", "DURATION", "TIME", "ERROR").
		Rows(rows...).
		Border(lipgloss.RoundedBorder()).
		BorderStyle(lipgloss.NewStyle().Foreground(ui.Subtle)).
		StyleFunc(func(row, col int) lipgloss.Style {
			if row == table.HeaderRow {
				return lipgloss.NewStyle().Bold(true).Foreground(ui.Secondary).Padding(0, 1)
			}
			s := lipgloss.NewStyle().Padding(0, 1)
			switch col {
			case 0: // STATUS
				if row >= 0 && row < len(events) {
					s = s.Foreground(pipelineStatusColor(events[row].Attributes.Status)).Bold(true)
				}
				s = s.Width(10)
			case 1: // LEVEL
				s = s.Foreground(ui.Muted).Width(10)
			case 2: // NAME
				s = s.Foreground(ui.Text).Width(37)
			case 3: // SERVICE
				s = s.Foreground(ui.Secondary).Width(22)
			case 4: // DURATION
				s = s.Foreground(ui.Muted).Width(10)
			case 5: // TIME
				s = s.Foreground(ui.Muted).Width(10)
			case 6: // ERROR
				s = s.Foreground(ui.Danger).Width(27)
			}
			return s
		})

	fmt.Println(t)
	fmt.Println(ui.Dimmed.Render(fmt.Sprintf("  %d events", len(events))))
	return nil
}

func init() {
	pipelinesCmd.Flags().BoolVar(&pipelinesJSON, "json", false, "Output as JSON array")
	pipelinesCmd.Flags().BoolVar(&pipelinesPlain, "plain", false, "Force plain TSV output")
	pipelinesCmd.Flags().IntVarP(&pipelinesLimit, "limit", "n", 25, "Maximum number of results")
	pipelinesCmd.Flags().IntVar(&pipelinesMinutes, "minutes", 60, "Minutes of history to search (default 60)")
	rootCmd.AddCommand(pipelinesCmd)
}
