package cmd

import (
	"fmt"
	"strconv"
	"time"

	"github.com/ngavilan-dogfy/datadog-cli/datadog"
	"github.com/ngavilan-dogfy/datadog-cli/ui"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/lipgloss/table"
	"github.com/spf13/cobra"
)

var (
	downtimesJSON  bool
	downtimesPlain bool
	schedScope     string
	schedMsg       string
	schedDuration  string
	schedMonitor   string
)

var downtimesCmd = &cobra.Command{
	Use:   "downtimes",
	Short: "List and manage scheduled downtimes",
	Long: `List Datadog scheduled downtimes.

Output adapts automatically:
  • Terminal  → colored table
  • Piped     → tab-separated plain text (TSV)
  • --json    → structured JSON array

Examples:
  datadog downtimes                        # all downtimes
  datadog downtimes --json                 # JSON output`,
	RunE: func(cmd *cobra.Command, args []string) error {
		downtimes, err := client.ListDowntimes()
		if err != nil {
			return err
		}

		if downtimesJSON {
			return printJSON(downtimes)
		}

		if len(downtimes) == 0 {
			if isTTY() && !downtimesPlain {
				fmt.Println(ui.Dimmed.Render("  No downtimes found."))
			}
			return nil
		}

		if !isTTY() || downtimesPlain {
			return printDowntimesTSV(downtimes)
		}

		return printDowntimesTable(downtimes)
	},
}

var downtimesScheduleCmd = &cobra.Command{
	Use:   "schedule",
	Short: "Schedule a new downtime",
	Long: `Schedule a downtime to suppress monitor notifications.

The scope parameter uses Datadog tag syntax (e.g. "host:web-01", "env:prod").

Examples:
  datadog downtimes schedule --scope "host:web-01" -d 1h
  datadog downtimes schedule --scope "env:staging" -d 2h -m "deploy"
  datadog downtimes schedule --scope "*" -d 30m --monitor 12345
  datadog downtimes schedule --scope "service:api" -d 4h`,
	RunE: func(cmd *cobra.Command, args []string) error {
		if schedScope == "" {
			return fmt.Errorf("--scope is required (e.g. host:web-01, env:prod, *)")
		}

		start := time.Now()
		var end time.Time

		if schedDuration != "" {
			d, err := parseDuration(schedDuration)
			if err != nil {
				return err
			}
			end = start.Add(d)
		}

		var monitorID *int64
		if schedMonitor != "" {
			id, err := strconv.ParseInt(schedMonitor, 10, 64)
			if err != nil {
				return fmt.Errorf("invalid monitor ID: %s", schedMonitor)
			}
			monitorID = &id
		}

		dt, err := client.ScheduleDowntime(schedScope, schedMsg, start, end, monitorID)
		if err != nil {
			return err
		}

		fmt.Println(ui.SuccessStyle.Render(fmt.Sprintf("  Scheduled downtime %s", dt.ID)))
		fmt.Println(ui.Dimmed.Render(fmt.Sprintf("  Scope: %s", schedScope)))
		if schedDuration != "" {
			fmt.Println(ui.Dimmed.Render(fmt.Sprintf("  Duration: %s", schedDuration)))
		}
		return nil
	},
}

var downtimesCancelCmd = &cobra.Command{
	Use:   "cancel <downtime-id>",
	Short: "Cancel a scheduled downtime",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		id := args[0]
		if err := client.CancelDowntime(id); err != nil {
			return err
		}

		fmt.Println(ui.SuccessStyle.Render(fmt.Sprintf("  Cancelled downtime %s", id)))
		return nil
	},
}

// --- helpers ---

func printDowntimesTSV(downtimes []datadog.DowntimeData) error {
	headers := []string{"ID", "STATUS", "SCOPE", "MESSAGE", "START", "END"}
	var rows [][]string
	for _, d := range downtimes {
		msg := ""
		if d.Attributes.Message != nil {
			msg = *d.Attributes.Message
		}
		start := ""
		end := ""
		if d.Attributes.Schedule != nil {
			start = d.Attributes.Schedule.Start
			if d.Attributes.Schedule.End != nil {
				end = *d.Attributes.Schedule.End
			}
		}
		rows = append(rows, []string{
			d.ID,
			d.Attributes.Status,
			d.Attributes.Scope,
			msg,
			start,
			end,
		})
	}
	printTSV(headers, rows)
	return nil
}

func printDowntimesTable(downtimes []datadog.DowntimeData) error {
	fmt.Println(ui.Title.Render(" Downtimes"))

	var rows [][]string
	for _, d := range downtimes {
		scope := d.Attributes.Scope
		if len(scope) > 30 {
			scope = scope[:27] + "..."
		}
		msg := ""
		if d.Attributes.Message != nil {
			msg = *d.Attributes.Message
			if len(msg) > 30 {
				msg = msg[:27] + "..."
			}
		}
		start := ""
		end := "—"
		if d.Attributes.Schedule != nil {
			if t := datadog.ParseTime(d.Attributes.Schedule.Start); !t.IsZero() {
				start = t.Format("Jan 02 15:04")
			}
			if d.Attributes.Schedule.End != nil {
				if t := datadog.ParseTime(*d.Attributes.Schedule.End); !t.IsZero() {
					end = t.Format("Jan 02 15:04")
				}
			}
		}

		rows = append(rows, []string{
			d.ID,
			d.Attributes.Status,
			scope,
			msg,
			start,
			end,
		})
	}

	t := table.New().
		Headers("ID", "STATUS", "SCOPE", "MESSAGE", "START", "END").
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
				s = s.Bold(true).Foreground(lipgloss.Color("#FFFFFF"))
			case 1: // STATUS
				if row >= 0 && row < len(downtimes) {
					status := downtimes[row].Attributes.Status
					switch status {
					case "active":
						s = s.Foreground(ui.Success).Bold(true)
					case "canceled", "cancelled":
						s = s.Foreground(ui.Danger)
					case "ended":
						s = s.Foreground(ui.Muted)
					default:
						s = s.Foreground(ui.Warning)
					}
				}
			case 2: // SCOPE
				s = s.Foreground(ui.Text).Width(32)
			case 3: // MESSAGE
				s = s.Foreground(ui.Muted).Width(32)
			case 4, 5: // START/END
				s = s.Foreground(ui.Muted)
			}
			return s
		})

	fmt.Println(t)
	fmt.Println(ui.Dimmed.Render(fmt.Sprintf("  %d downtimes", len(downtimes))))
	return nil
}

func init() {
	downtimesCmd.Flags().BoolVar(&downtimesJSON, "json", false, "Output as JSON array")
	downtimesCmd.Flags().BoolVar(&downtimesPlain, "plain", false, "Force plain TSV output")

	downtimesScheduleCmd.Flags().StringVar(&schedScope, "scope", "", "Downtime scope (e.g. host:web-01, env:prod, *)")
	downtimesScheduleCmd.Flags().StringVarP(&schedMsg, "message", "m", "", "Downtime reason")
	downtimesScheduleCmd.Flags().StringVarP(&schedDuration, "duration", "d", "", "Duration (30m, 1h, 2h, 1d)")
	downtimesScheduleCmd.Flags().StringVar(&schedMonitor, "monitor", "", "Monitor ID to target")

	downtimesCmd.AddCommand(downtimesScheduleCmd)
	downtimesCmd.AddCommand(downtimesCancelCmd)
	rootCmd.AddCommand(downtimesCmd)
}
