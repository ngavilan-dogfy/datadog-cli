package cmd

import (
	"fmt"
	"strings"
	"time"

	"datadog-cli/datadog"
	"datadog-cli/ui"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/lipgloss/table"
	"github.com/spf13/cobra"
)

var (
	securityJSON    bool
	securityPlain   bool
	securityLimit   int
	securityMinutes int
)

var securityCmd = &cobra.Command{
	Use:     "security",
	Aliases: []string{"sec"},
	Short:   "Search security signals",
	Long: `Search Datadog security monitoring signals.

By default shows signals from the last 60 minutes.

Output adapts automatically:
  • Terminal  → colored table
  • Piped     → tab-separated plain text (TSV)
  • --json    → structured JSON array

Examples:
  datadog security                         # recent signals
  datadog sec                              # alias
  datadog sec --minutes 1440               # last 24h
  datadog sec --json | jq '.[].title'      # extract titles`,
	Args: cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		query := "*"
		if len(args) > 0 {
			query = args[0]
		}

		from := time.Now().Add(-time.Duration(securityMinutes) * time.Minute).Format(time.RFC3339)
		to := time.Now().Format(time.RFC3339)

		result, err := client.SearchSecuritySignals(query, from, to, securityLimit)
		if err != nil {
			return err
		}

		if securityJSON {
			return printJSON(result.Data)
		}

		if len(result.Data) == 0 {
			if isTTY() && !securityPlain {
				fmt.Println(ui.Dimmed.Render("  No security signals found."))
			}
			return nil
		}

		if !isTTY() || securityPlain {
			return printSecurityTSV(result.Data)
		}

		return printSecurityTable(result.Data)
	},
}

// --- helpers ---

func printSecurityTSV(signals []datadog.SecuritySignalData) error {
	headers := []string{"ID", "STATUS", "SEVERITY", "TITLE", "TIMESTAMP"}
	var rows [][]string
	for _, s := range signals {
		title := s.Attributes.Title
		if title == "" {
			title = s.Attributes.Message
			if len(title) > 80 {
				title = title[:77] + "..."
			}
		}
		rows = append(rows, []string{
			s.ID,
			s.Attributes.Status,
			s.Attributes.Severity,
			title,
			s.Attributes.Timestamp,
		})
	}
	printTSV(headers, rows)
	return nil
}

func printSecurityTable(signals []datadog.SecuritySignalData) error {
	fmt.Println(ui.Title.Render(fmt.Sprintf(" Security Signals · last %dm", securityMinutes)))

	var rows [][]string
	for _, s := range signals {
		title := s.Attributes.Title
		if title == "" {
			title = s.Attributes.Message
		}
		if len(title) > 55 {
			title = title[:52] + "..."
		}
		title = strings.ReplaceAll(title, "\n", " ")

		ts := s.Attributes.Timestamp
		if t := datadog.ParseTime(ts); !t.IsZero() {
			ts = t.Format("15:04:05")
		}

		id := s.ID
		if len(id) > 12 {
			id = id[:12]
		}

		rows = append(rows, []string{
			id,
			s.Attributes.Status,
			s.Attributes.Severity,
			title,
			ts,
		})
	}

	t := table.New().
		Headers("ID", "STATUS", "SEV", "TITLE", "TIME").
		Rows(rows...).
		Border(lipgloss.RoundedBorder()).
		BorderStyle(lipgloss.NewStyle().Foreground(ui.Subtle)).
		StyleFunc(func(row, col int) lipgloss.Style {
			if row == table.HeaderRow {
				return lipgloss.NewStyle().Bold(true).Foreground(ui.Secondary).Padding(0, 1)
			}
			s := lipgloss.NewStyle().Padding(0, 1)
			switch col {
			case 0: // ID
				s = s.Bold(true).Foreground(lipgloss.Color("#FFFFFF")).Width(14)
			case 1: // STATUS
				if row >= 0 && row < len(signals) {
					switch signals[row].Attributes.Status {
					case "high", "critical":
						s = s.Foreground(ui.Danger).Bold(true)
					case "medium":
						s = s.Foreground(ui.Warning).Bold(true)
					case "low", "info":
						s = s.Foreground(ui.Muted)
					default:
						s = s.Foreground(ui.Text)
					}
				}
				s = s.Width(10)
			case 2: // SEV
				if row >= 0 && row < len(signals) {
					s = s.Foreground(ui.SeverityColor(signals[row].Attributes.Severity)).Bold(true)
				}
				s = s.Width(8)
			case 3: // TITLE
				s = s.Foreground(ui.Text).Width(57)
			case 4: // TIME
				s = s.Foreground(ui.Muted)
			}
			return s
		})

	fmt.Println(t)
	fmt.Println(ui.Dimmed.Render(fmt.Sprintf("  %d signals", len(signals))))
	return nil
}

func init() {
	securityCmd.Flags().BoolVar(&securityJSON, "json", false, "Output as JSON array")
	securityCmd.Flags().BoolVar(&securityPlain, "plain", false, "Force plain TSV output")
	securityCmd.Flags().IntVarP(&securityLimit, "limit", "n", 25, "Maximum number of results")
	securityCmd.Flags().IntVar(&securityMinutes, "minutes", 60, "Minutes of history to search (default 60)")
	rootCmd.AddCommand(securityCmd)
}
