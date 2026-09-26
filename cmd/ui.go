package cmd

import (
	"fmt"

	"github.com/ngavilan-dogfy/datadog-cli/tui"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/spf13/cobra"
)

var uiCmd = &cobra.Command{
	Use:   "ui",
	Short: "Launch interactive TUI for monitors",
	Long: `Launch an interactive terminal UI for browsing and managing monitors.

Features:
  • Live monitor list with auto-refresh (30s)
  • Filter by state (OK, Alert, Warn, No Data)
  • Search/filter by name
  • View full monitor details
  • Mute/unmute from the TUI
  • Open in browser

Shortcuts:
  enter  View monitor details
  m      Mute selected monitor
  u      Unmute selected monitor
  o      Open in browser
  f      Cycle state filter
  r      Refresh
  /      Search
  q      Quit`,
	RunE: func(cmd *cobra.Command, args []string) error {
		if client == nil || cfg == nil {
			return fmt.Errorf("not logged in — run 'datadog login' to get started")
		}
		m := tui.New(client)
		p := tea.NewProgram(m, tea.WithAltScreen())
		_, err := p.Run()
		return err
	},
}

func init() {
	rootCmd.AddCommand(uiCmd)
}
