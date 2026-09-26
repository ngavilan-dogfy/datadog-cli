package cmd

import (
	"fmt"
	"strings"

	"github.com/ngavilan-dogfy/datadog-cli/internal/selfupdate"
	"github.com/ngavilan-dogfy/datadog-cli/tui"

	"github.com/spf13/cobra"
)

var uiTab string

var uiCmd = &cobra.Command{
	Use:   "ui [dashboard-id | dashboard link]",
	Short: "Dashboards, monitors, logs and metrics in your terminal",
	Long: `An interactive Datadog in your terminal.

Tabs (1-5): Now · Dashboards · Monitors · Logs · Metrics

Dashboards render like in Datadog — time series, query values, top lists,
notes, groups, log streams, monitor summaries — with template variables and
a shared time range. Move between widgets with the arrows (or hjkl), zoom
one with enter to read exact values under a cursor, and fold groups.

Charts are drawn with braille dots (finer, needs a font with braille) or
blocks (works everywhere): press B to switch; the choice is remembered.

Keys (press ? inside for the full list, : to jump anywhere):
  1-5 tabs · t time range · v template variables · ⏎ open/zoom · o browser
  r refresh · B chart style · q back/quit

Examples:
  datadog ui                           # start on the Now tab
  datadog ui abc-def-ghi               # open a dashboard right away
  datadog ui https://app.datadoghq.eu/dashboard/abc-def-ghi/on-call
  datadog ui --tab monitors`,
	Args: cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		if client == nil || cfg == nil {
			return fmt.Errorf("not connected — run 'datadog setup' to get started")
		}
		opts := tui.Options{Site: cfg.Site, Profile: cfg.Name, Tab: strings.ToLower(uiTab),
			UpdateNotes: selfupdate.KnownNewer(updateTool(), cacheDir())}
		if len(args) == 1 {
			opts.Dashboard = dashboardID(args[0])
		}
		return tui.Run(tui.New(client, opts))
	},
}

// dashboardID accepts an id (abc-123-xyz) or a dashboard link.
func dashboardID(s string) string {
	if i := strings.Index(s, "/dashboard/"); i >= 0 {
		s = s[i+len("/dashboard/"):]
		if j := strings.IndexAny(s, "/?#"); j >= 0 {
			s = s[:j]
		}
	}
	return strings.TrimSpace(s)
}

func init() {
	uiCmd.Flags().StringVar(&uiTab, "tab", "", "Tab to start on: now, dashboards, monitors, logs, metrics")
	rootCmd.AddCommand(uiCmd)
}
