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
	hostsJSON    bool
	hostsPlain   bool
	hostsFilter  string
	hostsLimit   int
	hostMuteDur  string
	hostMuteMsg  string
)

var hostsCmd = &cobra.Command{
	Use:   "hosts",
	Short: "List and manage infrastructure hosts",
	Long: `List Datadog infrastructure hosts with status, apps, and agent info.

Output adapts automatically:
  • Terminal  → colored table with borders
  • Piped     → tab-separated plain text (TSV)
  • --json    → structured JSON array

Filtering:
  -f, --filter  Filter by hostname or tag (substring match)
  -n, --limit   Max results (default 100)

Examples:
  datadog hosts                            # all hosts
  datadog hosts -f "web"                   # filter by name
  datadog hosts --json | jq '.[].name'     # extract hostnames
  datadog hosts --plain | grep DOWN        # find down hosts`,
	RunE: func(cmd *cobra.Command, args []string) error {
		result, err := client.ListHosts(hostsFilter, hostsLimit)
		if err != nil {
			return err
		}

		if hostsJSON {
			return printJSON(hostsToJSON(result.HostList))
		}

		if len(result.HostList) == 0 {
			if isTTY() && !hostsPlain {
				fmt.Println(ui.Dimmed.Render("  No hosts found."))
			}
			return nil
		}

		if !isTTY() || hostsPlain {
			return printHostsTSV(result.HostList)
		}

		return printHostsTable(result)
	},
}

var hostsMuteCmd = &cobra.Command{
	Use:   "mute <hostname>",
	Short: "Mute a host",
	Long: `Mute a host to suppress notifications.

Examples:
  datadog hosts mute web-01                # mute indefinitely
  datadog hosts mute web-01 -d 1h          # mute for 1 hour
  datadog hosts mute web-01 -m "deploy"    # mute with message`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		hostname := args[0]

		var end int64
		if hostMuteDur != "" {
			d, err := parseDuration(hostMuteDur)
			if err != nil {
				return err
			}
			end = time.Now().Add(d).Unix()
		}

		if err := client.MuteHost(hostname, end, hostMuteMsg); err != nil {
			return err
		}

		msg := fmt.Sprintf("  Muted host %s", hostname)
		if hostMuteDur != "" {
			msg += fmt.Sprintf(" for %s", hostMuteDur)
		}
		fmt.Println(ui.SuccessStyle.Render(msg))
		return nil
	},
}

var hostsUnmuteCmd = &cobra.Command{
	Use:   "unmute <hostname>",
	Short: "Unmute a host",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		hostname := args[0]

		if err := client.UnmuteHost(hostname); err != nil {
			return err
		}

		fmt.Println(ui.SuccessStyle.Render(fmt.Sprintf("  Unmuted host %s", hostname)))
		return nil
	},
}

// --- helpers ---

type hostJSONOut struct {
	Name         string   `json:"name"`
	Up           bool     `json:"up"`
	IsMuted      bool     `json:"is_muted"`
	Apps         []string `json:"apps"`
	Platform     string   `json:"platform"`
	AgentVersion string   `json:"agent_version"`
	LastReported string   `json:"last_reported"`
	URL          string   `json:"url"`
}

func hostsToJSON(hosts []datadog.Host) []hostJSONOut {
	out := make([]hostJSONOut, len(hosts))
	for i, h := range hosts {
		out[i] = hostJSONOut{
			Name:         h.Name,
			Up:           h.Up,
			IsMuted:      h.IsMuted,
			Apps:         h.Apps,
			Platform:     h.Meta.Platform,
			AgentVersion: h.Meta.AgentVersion,
			LastReported: datadog.FormatUnix(h.LastReportedTime),
			URL:          client.BrowseURL(fmt.Sprintf("/infrastructure?host=%s", h.Name)),
		}
	}
	return out
}

func printHostsTSV(hosts []datadog.Host) error {
	headers := []string{"NAME", "STATUS", "MUTED", "PLATFORM", "AGENT", "APPS", "LAST SEEN"}
	var rows [][]string
	for _, h := range hosts {
		status := "UP"
		if !h.Up {
			status = "DOWN"
		}
		muted := ""
		if h.IsMuted {
			muted = "muted"
		}
		rows = append(rows, []string{
			h.Name,
			status,
			muted,
			h.Meta.Platform,
			h.Meta.AgentVersion,
			strings.Join(h.Apps, ","),
			datadog.UnixRelativeTime(h.LastReportedTime),
		})
	}
	printTSV(headers, rows)
	return nil
}

func printHostsTable(result *datadog.HostsResponse) error {
	header := " Hosts"
	if hostsFilter != "" {
		header += " · " + hostsFilter
	}
	fmt.Println(ui.Title.Render(header))

	var rows [][]string
	for _, h := range result.HostList {
		name := h.Name
		if len(name) > 40 {
			name = name[:37] + "..."
		}
		muted := ""
		if h.IsMuted {
			muted = "muted"
		}
		apps := strings.Join(h.Apps, ", ")
		if len(apps) > 25 {
			apps = apps[:22] + "..."
		}

		rows = append(rows, []string{
			name,
			fmt.Sprintf("%v", h.Up),
			muted,
			h.Meta.Platform,
			h.Meta.AgentVersion,
			apps,
			datadog.UnixRelativeTime(h.LastReportedTime),
		})
	}

	t := table.New().
		Headers("NAME", "STATUS", "MUTED", "PLATFORM", "AGENT", "APPS", "LAST SEEN").
		Rows(rows...).
		Border(lipgloss.RoundedBorder()).
		BorderStyle(lipgloss.NewStyle().Foreground(ui.Subtle)).
		StyleFunc(func(row, col int) lipgloss.Style {
			if row == table.HeaderRow {
				return lipgloss.NewStyle().Bold(true).Foreground(ui.Secondary).Padding(0, 1)
			}
			s := lipgloss.NewStyle().Padding(0, 1)
			switch col {
			case 0: // NAME
				s = s.Bold(true).Foreground(lipgloss.Color("#FFFFFF")).Width(42)
			case 1: // STATUS
				if row >= 0 && row < len(result.HostList) {
					if result.HostList[row].Up {
						s = s.Foreground(ui.Success).Bold(true)
					} else {
						s = s.Foreground(ui.Danger).Bold(true)
					}
				}
			case 2: // MUTED
				s = s.Foreground(ui.Warning).Width(6)
			case 3: // PLATFORM
				s = s.Foreground(ui.Muted).Width(10)
			case 4: // AGENT
				s = s.Foreground(ui.Muted).Width(10)
			case 5: // APPS
				s = s.Foreground(ui.Text).Width(27)
			case 6: // LAST SEEN
				s = s.Foreground(ui.Muted)
			}
			return s
		})

	fmt.Println(t)
	fmt.Println(ui.Dimmed.Render(fmt.Sprintf("  %d of %d hosts", result.TotalReturned, result.TotalMatching)))
	return nil
}

func init() {
	hostsCmd.Flags().BoolVar(&hostsJSON, "json", false, "Output as JSON array")
	hostsCmd.Flags().BoolVar(&hostsPlain, "plain", false, "Force plain TSV output")
	hostsCmd.Flags().StringVarP(&hostsFilter, "filter", "f", "", "Filter by hostname or tag")
	hostsCmd.Flags().IntVarP(&hostsLimit, "limit", "n", 100, "Maximum number of results")

	hostsMuteCmd.Flags().StringVarP(&hostMuteDur, "duration", "d", "", "Mute duration (30m, 1h, 2h, 1d)")
	hostsMuteCmd.Flags().StringVarP(&hostMuteMsg, "message", "m", "", "Mute reason message")

	hostsCmd.AddCommand(hostsMuteCmd)
	hostsCmd.AddCommand(hostsUnmuteCmd)
	rootCmd.AddCommand(hostsCmd)
}
