package cmd

import (
	"fmt"
	"time"

	"github.com/ngavilan-dogfy/datadog-cli/datadog"
	"github.com/ngavilan-dogfy/datadog-cli/ui"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/lipgloss/table"
	"github.com/spf13/cobra"
)

var (
	usageJSON   bool
	usagePlain  bool
	usageMonths int
)

var usageCmd = &cobra.Command{
	Use:   "usage",
	Short: "Show usage summary and estimated costs",
	Long: `Show Datadog usage summary with host counts, container counts,
log volumes, and more.

By default shows the current month.

Examples:
  datadog usage                            # current month
  datadog usage --months 3                 # last 3 months
  datadog usage --json                     # JSON output`,
	RunE: func(cmd *cobra.Command, args []string) error {
		startDate := time.Now().AddDate(0, -(usageMonths - 1), 0).Format("2006-01")
		endDate := time.Now().Format("2006-01")

		result, err := client.GetUsageSummary(startDate, endDate)
		if err != nil {
			return err
		}

		if usageJSON {
			return printJSON(result)
		}

		if len(result.Usage) == 0 {
			fmt.Println(ui.Dimmed.Render("  No usage data found."))
			return nil
		}

		if !isTTY() || usagePlain {
			return printUsageTSV(result.Usage)
		}

		return printUsageTable(result)
	},
}

// --- helpers ---

func intOrDash(v *int64) string {
	if v == nil {
		return "—"
	}
	return fmt.Sprintf("%d", *v)
}

func bytesToHuman(v *int64) string {
	if v == nil {
		return "—"
	}
	b := float64(*v)
	switch {
	case b >= 1e12:
		return fmt.Sprintf("%.1f TB", b/1e12)
	case b >= 1e9:
		return fmt.Sprintf("%.1f GB", b/1e9)
	case b >= 1e6:
		return fmt.Sprintf("%.1f MB", b/1e6)
	case b >= 1e3:
		return fmt.Sprintf("%.1f KB", b/1e3)
	default:
		return fmt.Sprintf("%d B", *v)
	}
}

func printUsageTSV(usage []datadog.UsageSummary) error {
	headers := []string{"DATE", "HOSTS", "CONTAINERS", "CUSTOM_TS", "INDEXED_LOGS", "INGESTED_BYTES", "APM_HOSTS", "SYNTHETICS"}
	var rows [][]string
	for _, u := range usage {
		rows = append(rows, []string{
			u.Date,
			intOrDash(u.AgentHostCount),
			intOrDash(u.ContainerCount),
			intOrDash(u.CustomTSCount),
			intOrDash(u.IndexedEventsCount),
			intOrDash(u.IngestedEventsBytes),
			intOrDash(u.APMHostCount),
			intOrDash(u.SyntheticsCount),
		})
	}
	printTSV(headers, rows)
	return nil
}

func printUsageTable(result *datadog.UsageSummaryResponse) error {
	fmt.Println(ui.Title.Render(" Usage Summary"))

	var rows [][]string
	for _, u := range result.Usage {
		date := u.Date
		if len(date) > 10 {
			date = date[:10]
		}

		rows = append(rows, []string{
			date,
			intOrDash(u.AgentHostCount),
			intOrDash(u.ContainerCount),
			intOrDash(u.CustomTSCount),
			intOrDash(u.IndexedEventsCount),
			bytesToHuman(u.IngestedEventsBytes),
			intOrDash(u.APMHostCount),
			intOrDash(u.SyntheticsCount),
		})
	}

	t := table.New().
		Headers("DATE", "HOSTS", "CONTAINERS", "CUSTOM TS", "LOGS IDX", "LOGS VOL", "APM", "SYNTH").
		Rows(rows...).
		Border(lipgloss.RoundedBorder()).
		BorderStyle(lipgloss.NewStyle().Foreground(ui.Subtle)).
		StyleFunc(func(row, col int) lipgloss.Style {
			if row == table.HeaderRow {
				return lipgloss.NewStyle().Bold(true).Foreground(ui.Secondary).Padding(0, 1)
			}
			s := lipgloss.NewStyle().Padding(0, 1)
			switch col {
			case 0: // DATE
				s = s.Bold(true).Foreground(lipgloss.Color("#FFFFFF")).Width(12)
			default:
				s = s.Foreground(ui.Text).Align(lipgloss.Right).Width(12)
			}
			return s
		})

	fmt.Println(t)
	return nil
}

func init() {
	usageCmd.Flags().BoolVar(&usageJSON, "json", false, "Output as JSON")
	usageCmd.Flags().BoolVar(&usagePlain, "plain", false, "Force plain TSV output")
	usageCmd.Flags().IntVar(&usageMonths, "months", 1, "Number of months to show")
	rootCmd.AddCommand(usageCmd)
}
