package cmd

import (
	"fmt"
	"os/exec"
	"runtime"
	"strings"

	"github.com/ngavilan-dogfy/datadog-cli/datadog"
	"github.com/ngavilan-dogfy/datadog-cli/ui"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/lipgloss/table"
	"github.com/spf13/cobra"
)

var (
	dashJSON  bool
	dashPlain bool
	dashQuery string
)

var dashboardsCmd = &cobra.Command{
	Use:     "dashboards",
	Aliases: []string{"dash"},
	Short:   "List and open dashboards",
	Long: `List Datadog dashboards with title, author, and layout type.

Output adapts automatically:
  • Terminal  → colored table with borders
  • Piped     → tab-separated plain text (TSV)
  • --json    → structured JSON array

Filtering:
  -q, --query    Filter by title (case-insensitive substring match)

Examples:
  datadog dashboards                       # all dashboards
  datadog dash                             # alias
  datadog dash -q "api"                    # filter by title
  datadog dash --json | jq '.[].title'     # extract titles
  datadog dash --plain | grep production   # TSV + grep`,
	RunE: func(cmd *cobra.Command, args []string) error {
		dashboards, err := client.ListDashboards()
		if err != nil {
			return err
		}

		if dashQuery != "" {
			dashboards = filterDashboards(dashboards, dashQuery)
		}

		if dashJSON {
			return printJSON(dashboardsToJSON(dashboards))
		}

		if len(dashboards) == 0 {
			if isTTY() && !dashPlain {
				fmt.Println(ui.Dimmed.Render("  No dashboards found."))
			}
			return nil
		}

		if !isTTY() || dashPlain {
			return printDashboardsTSV(dashboards)
		}

		return printDashboardsTable(dashboards)
	},
}

var dashboardsOpenCmd = &cobra.Command{
	Use:   "open <dashboard-id>",
	Short: "Open dashboard in browser",
	Long: `Open a Datadog dashboard in your default web browser.

Examples:
  datadog dashboards open abc-def-ghi      # open by ID
  datadog dash open abc-def-ghi            # alias`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		id := args[0]
		url := client.BrowseURL(fmt.Sprintf("/dashboard/%s", id))

		var openBrowser *exec.Cmd
		switch runtime.GOOS {
		case "darwin":
			openBrowser = exec.Command("open", url)
		case "linux":
			openBrowser = exec.Command("xdg-open", url)
		default:
			openBrowser = exec.Command("open", url)
		}

		if err := openBrowser.Run(); err != nil {
			return fmt.Errorf("failed to open browser: %w", err)
		}

		if !isTTY() {
			fmt.Println(url)
			return nil
		}

		fmt.Println(ui.SuccessStyle.Render(fmt.Sprintf("  OK  Opened dashboard %s", id)))
		fmt.Println(ui.Dimmed.Render("  " + url))
		return nil
	},
}

// --- helpers ---

func filterDashboards(dashboards []datadog.DashboardSummary, query string) []datadog.DashboardSummary {
	q := strings.ToLower(query)
	var filtered []datadog.DashboardSummary
	for _, d := range dashboards {
		if strings.Contains(strings.ToLower(d.Title), q) {
			filtered = append(filtered, d)
		}
	}
	return filtered
}

type dashboardJSONOut struct {
	ID       string `json:"id"`
	Title    string `json:"title"`
	Author   string `json:"author"`
	Layout   string `json:"layout"`
	Modified string `json:"modified"`
	URL      string `json:"url"`
}

func dashboardsToJSON(dashboards []datadog.DashboardSummary) []dashboardJSONOut {
	out := make([]dashboardJSONOut, len(dashboards))
	for i, d := range dashboards {
		out[i] = dashboardJSONOut{
			ID:       d.ID,
			Title:    d.Title,
			Author:   d.AuthorHandle,
			Layout:   d.LayoutType,
			Modified: d.ModifiedAt,
			URL:      client.BrowseURL(fmt.Sprintf("/dashboard/%s", d.ID)),
		}
	}
	return out
}

func printDashboardsTSV(dashboards []datadog.DashboardSummary) error {
	headers := []string{"ID", "LAYOUT", "AUTHOR", "TITLE"}
	var rows [][]string
	for _, d := range dashboards {
		rows = append(rows, []string{
			d.ID,
			d.LayoutType,
			d.AuthorHandle,
			d.Title,
		})
	}
	printTSV(headers, rows)
	return nil
}

func printDashboardsTable(dashboards []datadog.DashboardSummary) error {
	header := " Dashboards"
	if dashQuery != "" {
		header += " · " + dashQuery
	}
	fmt.Println(ui.Title.Render(header))

	var rows [][]string
	for _, d := range dashboards {
		title := d.Title
		if len(title) > 60 {
			title = title[:57] + "..."
		}
		author := d.AuthorHandle
		if idx := strings.Index(author, "@"); idx > 0 {
			author = author[:idx]
		}

		rows = append(rows, []string{
			d.ID,
			d.LayoutType,
			author,
			title,
			datadog.RelativeTime(d.ModifiedAt),
		})
	}

	t := table.New().
		Headers("ID", "LAYOUT", "AUTHOR", "TITLE", "MODIFIED").
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
				s = s.Bold(true).Foreground(lipgloss.Color("#FFFFFF")).Width(15)
			case 1: // LAYOUT
				s = s.Foreground(ui.Muted).Width(12)
			case 2: // AUTHOR
				s = s.Foreground(ui.Muted).Width(14)
			case 3: // TITLE
				s = s.Width(62).Foreground(ui.Text)
			case 4: // MODIFIED
				s = s.Foreground(ui.Muted)
			}
			return s
		})

	fmt.Println(t)
	fmt.Println(ui.Dimmed.Render(fmt.Sprintf("  %d dashboards", len(dashboards))))
	return nil
}

func init() {
	dashboardsCmd.Flags().BoolVar(&dashJSON, "json", false, "Output as JSON array")
	dashboardsCmd.Flags().BoolVar(&dashPlain, "plain", false, "Force plain TSV output")
	dashboardsCmd.Flags().StringVarP(&dashQuery, "query", "q", "", "Filter by title")

	dashboardsCmd.AddCommand(dashboardsOpenCmd)
	rootCmd.AddCommand(dashboardsCmd)
}
