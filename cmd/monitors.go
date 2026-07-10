package cmd

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"datadog-cli/datadog"
	"datadog-cli/ui"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/lipgloss/table"
	"github.com/spf13/cobra"
)

var (
	monitorsJSON    bool
	monitorsPlain   bool
	monitorsLimit   int
	monitorsState   string
	monitorsType    string
	monitorShowJSON bool
	muteDuration    string
	monitorsWatch   bool
)

var monitorsCmd = &cobra.Command{
	Use:     "monitors",
	Aliases: []string{"mon"},
	Short:   "List and manage monitors",
	Long: `List Datadog monitors with status, type, priority, and name.

Output adapts automatically:
  • Terminal  → colored table with borders
  • Piped     → tab-separated plain text (TSV)
  • --json    → structured JSON array

Filtering:
  --state     Filter by overall state (OK, Alert, Warn, "No Data")
  --type      Filter by monitor type (metric, host, process, log alert, ...)
  -n, --limit Max results (default 50)

Examples:
  datadog monitors                         # all monitors
  datadog monitors --state Alert           # only alerting
  datadog monitors --state Warn --json     # warnings as JSON
  datadog monitors --type "log alert"      # log monitors only
  datadog monitors --plain | grep Alert    # TSV + grep
  datadog monitors --json | jq '.[].name'  # extract names`,
	RunE: func(cmd *cobra.Command, args []string) error {
		if monitorsWatch {
			return watchMonitors(monitorsState, monitorsType, monitorsLimit, 10*time.Second)
		}

		monitors, err := client.ListMonitors("", monitorsLimit)
		if err != nil {
			return err
		}

		// Apply local filters
		if monitorsState != "" {
			monitors = filterMonitorsByState(monitors, monitorsState)
		}
		if monitorsType != "" {
			monitors = filterMonitorsByType(monitors, monitorsType)
		}

		if monitorsJSON {
			return printJSON(monitorsToJSON(monitors))
		}

		if len(monitors) == 0 {
			if isTTY() && !monitorsPlain {
				fmt.Println(ui.Dimmed.Render("  No monitors found."))
			}
			return nil
		}

		if !isTTY() || monitorsPlain {
			return printMonitorsTSV(monitors)
		}

		return printMonitorsTable(monitors)
	},
}

var monitorsShowCmd = &cobra.Command{
	Use:   "show <monitor-id>",
	Short: "Show full monitor details",
	Long: `Display complete information about a Datadog monitor.

Output:
  • TTY    → rich colored display
  • Piped  → plain text
  • --json → full structured JSON

Examples:
  datadog monitors show 12345               # full details
  datadog monitors show 12345 --json        # JSON for scripting
  datadog monitors show                     # interactive fzf picker
  datadog monitors show 12345 --json | jq   # pipe to jq`,
	Args: cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		id, err := pickMonitorID(args)
		if err != nil {
			return err
		}

		monitor, err := client.GetMonitor(id)
		if err != nil {
			return err
		}

		if monitorShowJSON {
			return printJSON(monitor)
		}

		renderMonitor(monitor)
		return nil
	},
}

var monitorsMuteCmd = &cobra.Command{
	Use:   "mute <monitor-id>",
	Short: "Mute a monitor",
	Long: `Mute a Datadog monitor to suppress notifications.

Duration format: 30m, 1h, 2h, 4h, 8h, 24h, 7d (default: no end)

Examples:
  datadog monitors mute 12345               # mute indefinitely
  datadog monitors mute 12345 -d 1h         # mute for 1 hour
  datadog monitors mute 12345 -d 30m        # mute for 30 minutes
  datadog monitors mute                     # interactive fzf picker`,
	Args: cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		id, err := pickMonitorID(args)
		if err != nil {
			return err
		}

		var end int64
		if muteDuration != "" {
			d, err := parseDuration(muteDuration)
			if err != nil {
				return err
			}
			end = time.Now().Add(d).Unix()
		}

		if err := client.MuteMonitor(id, end); err != nil {
			return err
		}

		msg := fmt.Sprintf("  Muted monitor %d", id)
		if muteDuration != "" {
			msg += fmt.Sprintf(" for %s", muteDuration)
		}
		fmt.Println(ui.SuccessStyle.Render(msg))
		return nil
	},
}

var monitorsUnmuteCmd = &cobra.Command{
	Use:   "unmute <monitor-id>",
	Short: "Unmute a monitor",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		id, err := strconv.ParseInt(args[0], 10, 64)
		if err != nil {
			return fmt.Errorf("invalid monitor ID: %s", args[0])
		}

		if err := client.UnmuteMonitor(id); err != nil {
			return err
		}

		fmt.Println(ui.SuccessStyle.Render(fmt.Sprintf("  Unmuted monitor %d", id)))
		return nil
	},
}

var monitorsSearchCmd = &cobra.Command{
	Use:   "search <query>",
	Short: "Search monitors by name",
	Long: `Search Datadog monitors by name.

Examples:
  datadog monitors search "cpu"
  datadog monitors search "disk" --json
  datadog monitors search "production" --state Alert`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		query := args[0]
		monitors, err := client.ListMonitors(query, monitorsLimit)
		if err != nil {
			return err
		}

		if monitorsState != "" {
			monitors = filterMonitorsByState(monitors, monitorsState)
		}

		if monitorsJSON {
			return printJSON(monitorsToJSON(monitors))
		}

		if len(monitors) == 0 {
			if isTTY() && !monitorsPlain {
				fmt.Println(ui.Dimmed.Render(fmt.Sprintf("  No monitors matching %q.", query)))
			}
			return nil
		}

		if !isTTY() || monitorsPlain {
			return printMonitorsTSV(monitors)
		}

		return printMonitorsTable(monitors)
	},
}

// --- helpers ---

func filterMonitorsByState(monitors []datadog.Monitor, state string) []datadog.Monitor {
	var filtered []datadog.Monitor
	for _, m := range monitors {
		if strings.EqualFold(m.OverallState, state) {
			filtered = append(filtered, m)
		}
	}
	return filtered
}

func filterMonitorsByType(monitors []datadog.Monitor, mtype string) []datadog.Monitor {
	var filtered []datadog.Monitor
	for _, m := range monitors {
		if strings.EqualFold(m.Type, mtype) {
			filtered = append(filtered, m)
		}
	}
	return filtered
}

type monitorJSONOut struct {
	ID       int64    `json:"id"`
	Name     string   `json:"name"`
	Type     string   `json:"type"`
	State    string   `json:"state"`
	Priority *int     `json:"priority,omitempty"`
	Tags     []string `json:"tags,omitempty"`
	Creator  string   `json:"creator"`
	Modified string   `json:"modified"`
	URL      string   `json:"url"`
}

func monitorsToJSON(monitors []datadog.Monitor) []monitorJSONOut {
	out := make([]monitorJSONOut, len(monitors))
	for i, m := range monitors {
		out[i] = monitorJSONOut{
			ID:       m.ID,
			Name:     m.Name,
			Type:     m.Type,
			State:    m.OverallState,
			Priority: m.Priority,
			Tags:     m.Tags,
			Creator:  m.Creator.Handle,
			Modified: m.Modified,
			URL:      client.BrowseURL(fmt.Sprintf("/monitors/%d", m.ID)),
		}
	}
	return out
}

func printMonitorsTSV(monitors []datadog.Monitor) error {
	headers := []string{"ID", "STATE", "TYPE", "PRIORITY", "NAME"}
	var rows [][]string
	for _, m := range monitors {
		pri := "—"
		if m.Priority != nil {
			pri = fmt.Sprintf("P%d", *m.Priority)
		}
		rows = append(rows, []string{
			strconv.FormatInt(m.ID, 10),
			m.OverallState,
			m.Type,
			pri,
			m.Name,
		})
	}
	printTSV(headers, rows)
	return nil
}

func printMonitorsTable(monitors []datadog.Monitor) error {
	header := " Monitors"
	if monitorsState != "" {
		header += " · " + monitorsState
	}
	if monitorsType != "" {
		header += " · " + monitorsType
	}
	fmt.Println(ui.Title.Render(header))

	var rows [][]string
	for _, m := range monitors {
		name := m.Name
		if len(name) > 60 {
			name = name[:57] + "..."
		}
		pri := "—"
		if m.Priority != nil {
			pri = fmt.Sprintf("P%d", *m.Priority)
		}

		rows = append(rows, []string{
			strconv.FormatInt(m.ID, 10),
			ui.MonitorTypeIcon(m.Type),
			m.OverallState,
			pri,
			name,
			datadog.RelativeTime(m.Modified),
		})
	}

	t := table.New().
		Headers("ID", "", "STATE", "PRI", "NAME", "MODIFIED").
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
				s = s.Bold(true).Foreground(lipgloss.Color("#FFFFFF")).Width(10)
			case 1: // TYPE icon
				s = s.Width(2).Padding(0, 0)
			case 2: // STATE
				if row >= 0 && row < len(monitors) {
					s = s.Foreground(ui.MonitorStateColor(monitors[row].OverallState)).Bold(true)
				}
			case 3: // PRIORITY
				if row >= 0 && row < len(monitors) {
					s = s.Width(4)
					if monitors[row].Priority != nil {
						switch *monitors[row].Priority {
						case 1:
							s = s.Foreground(ui.Danger).Bold(true)
						case 2:
							s = s.Foreground(lipgloss.Color("#F97316")).Bold(true)
						case 3:
							s = s.Foreground(ui.Warning)
						default:
							s = s.Foreground(ui.Muted)
						}
					} else {
						s = s.Foreground(ui.Muted)
					}
				}
			case 4: // NAME
				s = s.Width(62).Foreground(ui.Text)
			case 5: // MODIFIED
				s = s.Foreground(ui.Muted)
			}
			return s
		})

	fmt.Println(t)
	fmt.Println(ui.Dimmed.Render(fmt.Sprintf("  %d monitors", len(monitors))))
	return nil
}

func renderMonitor(m *datadog.Monitor) {
	title := fmt.Sprintf("%s  %s",
		ui.Key.Render(strconv.FormatInt(m.ID, 10)),
		m.Name)
	fmt.Println(ui.Title.Render(title))
	fmt.Println()

	metaStyle := lipgloss.NewStyle().PaddingLeft(2)

	type row struct{ label, value string }
	rows := []row{
		{"State", ui.MonitorStateBadge(m.OverallState)},
		{"Type", ui.MonitorTypeIcon(m.Type) + " " + m.Type},
	}

	if m.Priority != nil {
		rows = append(rows, row{"Priority", ui.PriorityLabel(m.Priority)})
	}
	rows = append(rows, row{"Creator", m.Creator.Handle})

	if len(m.Tags) > 0 {
		rows = append(rows, row{"Tags", strings.Join(m.Tags, ", ")})
	}

	if len(m.MatchingDowntimes) > 0 {
		rows = append(rows, row{"Downtimes", fmt.Sprintf("%d active", len(m.MatchingDowntimes))})
	}

	rows = append(rows,
		row{"Created", m.Created},
		row{"Modified", datadog.RelativeTime(m.Modified)},
	)

	for _, r := range rows {
		line := ui.Label.Render(r.label+":") + " " + r.value
		fmt.Println(metaStyle.Render(line))
	}

	// Query
	if m.Query != "" {
		fmt.Println()
		fmt.Println(ui.SectionHeader.Render("  Query"))
		queryStyle := lipgloss.NewStyle().PaddingLeft(4).Foreground(ui.Text)
		fmt.Println(queryStyle.Render(m.Query))
	}

	// Message
	if m.Message != "" {
		fmt.Println()
		fmt.Println(ui.SectionHeader.Render("  Message"))
		msgStyle := lipgloss.NewStyle().PaddingLeft(4).Foreground(ui.Text)
		// Truncate very long messages
		msg := m.Message
		if len(msg) > 500 {
			msg = msg[:497] + "..."
		}
		fmt.Println(msgStyle.Render(msg))
	}

	fmt.Println()
	fmt.Println(ui.Dimmed.Render(fmt.Sprintf("  %s", client.BrowseURL(fmt.Sprintf("/monitors/%d", m.ID)))))
}

func parseDuration(s string) (time.Duration, error) {
	s = strings.TrimSpace(strings.ToLower(s))
	if strings.HasSuffix(s, "d") {
		days := 0
		if _, err := fmt.Sscanf(s, "%dd", &days); err == nil && days > 0 {
			return time.Duration(days) * 24 * time.Hour, nil
		}
	}
	d, err := time.ParseDuration(s)
	if err != nil {
		return 0, fmt.Errorf("invalid duration %q — use formats like 30m, 1h, 2h, 1d", s)
	}
	return d, nil
}

func init() {
	monitorsCmd.Flags().BoolVar(&monitorsJSON, "json", false, "Output as JSON array")
	monitorsCmd.Flags().BoolVar(&monitorsPlain, "plain", false, "Force plain TSV output")
	monitorsCmd.Flags().IntVarP(&monitorsLimit, "limit", "n", 50, "Maximum number of results")
	monitorsCmd.Flags().StringVar(&monitorsState, "state", "", "Filter by state (OK, Alert, Warn, \"No Data\")")
	monitorsCmd.Flags().StringVar(&monitorsType, "type", "", "Filter by monitor type")
	monitorsCmd.Flags().BoolVarP(&monitorsWatch, "watch", "w", false, "Live refresh every 10s (Ctrl+C to stop)")

	monitorsShowCmd.Flags().BoolVar(&monitorShowJSON, "json", false, "Output as JSON")

	monitorsMuteCmd.Flags().StringVarP(&muteDuration, "duration", "d", "", "Mute duration (30m, 1h, 2h, 1d)")

	monitorsSearchCmd.Flags().BoolVar(&monitorsJSON, "json", false, "Output as JSON array")
	monitorsSearchCmd.Flags().BoolVar(&monitorsPlain, "plain", false, "Force plain TSV output")
	monitorsSearchCmd.Flags().IntVarP(&monitorsLimit, "limit", "n", 50, "Maximum number of results")
	monitorsSearchCmd.Flags().StringVar(&monitorsState, "state", "", "Filter by state")

	monitorsCmd.AddCommand(monitorsShowCmd)
	monitorsCmd.AddCommand(monitorsMuteCmd)
	monitorsCmd.AddCommand(monitorsUnmuteCmd)
	monitorsCmd.AddCommand(monitorsSearchCmd)
	rootCmd.AddCommand(monitorsCmd)
}
