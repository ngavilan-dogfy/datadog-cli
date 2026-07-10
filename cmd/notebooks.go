package cmd

import (
	"fmt"
	"os/exec"
	"runtime"
	"strings"

	"datadog-cli/datadog"
	"datadog-cli/ui"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/lipgloss/table"
	"github.com/spf13/cobra"
)

var (
	notebooksJSON  bool
	notebooksPlain bool
	notebooksQuery string
)

var notebooksCmd = &cobra.Command{
	Use:     "notebooks",
	Aliases: []string{"nb"},
	Short:   "List and open notebooks",
	Long: `List Datadog notebooks.

Output adapts automatically:
  • Terminal  → colored table
  • Piped     → tab-separated plain text (TSV)
  • --json    → structured JSON array

Examples:
  datadog notebooks                        # all notebooks
  datadog nb                               # alias
  datadog nb -q "postmortem"               # filter by name
  datadog nb --json | jq '.[].name'        # extract names`,
	RunE: func(cmd *cobra.Command, args []string) error {
		notebooks, err := client.ListNotebooks(notebooksQuery)
		if err != nil {
			return err
		}

		if notebooksJSON {
			return printJSON(notebooksToJSON(notebooks))
		}

		if len(notebooks) == 0 {
			if isTTY() && !notebooksPlain {
				fmt.Println(ui.Dimmed.Render("  No notebooks found."))
			}
			return nil
		}

		if !isTTY() || notebooksPlain {
			return printNotebooksTSV(notebooks)
		}

		return printNotebooksTable(notebooks)
	},
}

var notebooksOpenCmd = &cobra.Command{
	Use:   "open <notebook-id>",
	Short: "Open notebook in browser",
	Long: `Open a Datadog notebook in your default web browser.

Examples:
  datadog notebooks open 12345
  datadog nb open 12345`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		id := args[0]
		url := client.BrowseURL(fmt.Sprintf("/notebook/%s", id))

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

		fmt.Println(ui.SuccessStyle.Render(fmt.Sprintf("  OK  Opened notebook %s", id)))
		fmt.Println(ui.Dimmed.Render("  " + url))
		return nil
	},
}

// --- helpers ---

type notebookJSONOut struct {
	ID       int64  `json:"id"`
	Name     string `json:"name"`
	Author   string `json:"author"`
	Status   string `json:"status"`
	Modified string `json:"modified"`
	URL      string `json:"url"`
}

func notebooksToJSON(notebooks []datadog.NotebookEntry) []notebookJSONOut {
	out := make([]notebookJSONOut, len(notebooks))
	for i, n := range notebooks {
		out[i] = notebookJSONOut{
			ID:       n.ID,
			Name:     n.Attributes.Name,
			Author:   n.Attributes.Author.Handle,
			Status:   n.Attributes.Status,
			Modified: n.Attributes.Modified,
			URL:      client.BrowseURL(fmt.Sprintf("/notebook/%d", n.ID)),
		}
	}
	return out
}

func printNotebooksTSV(notebooks []datadog.NotebookEntry) error {
	headers := []string{"ID", "STATUS", "AUTHOR", "NAME"}
	var rows [][]string
	for _, n := range notebooks {
		author := n.Attributes.Author.Handle
		if idx := strings.Index(author, "@"); idx > 0 {
			author = author[:idx]
		}
		rows = append(rows, []string{
			fmt.Sprintf("%d", n.ID),
			n.Attributes.Status,
			author,
			n.Attributes.Name,
		})
	}
	printTSV(headers, rows)
	return nil
}

func printNotebooksTable(notebooks []datadog.NotebookEntry) error {
	header := " Notebooks"
	if notebooksQuery != "" {
		header += " · " + notebooksQuery
	}
	fmt.Println(ui.Title.Render(header))

	var rows [][]string
	for _, n := range notebooks {
		name := n.Attributes.Name
		if len(name) > 55 {
			name = name[:52] + "..."
		}
		author := n.Attributes.Author.Handle
		if idx := strings.Index(author, "@"); idx > 0 {
			author = author[:idx]
		}

		rows = append(rows, []string{
			fmt.Sprintf("%d", n.ID),
			n.Attributes.Status,
			author,
			name,
			datadog.RelativeTime(n.Attributes.Modified),
		})
	}

	t := table.New().
		Headers("ID", "STATUS", "AUTHOR", "NAME", "MODIFIED").
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
			case 1: // STATUS
				if row >= 0 && row < len(notebooks) {
					if notebooks[row].Attributes.Status == "published" {
						s = s.Foreground(ui.Success)
					} else {
						s = s.Foreground(ui.Muted)
					}
				}
				s = s.Width(10)
			case 2: // AUTHOR
				s = s.Foreground(ui.Muted).Width(14)
			case 3: // NAME
				s = s.Foreground(ui.Text).Width(57)
			case 4: // MODIFIED
				s = s.Foreground(ui.Muted)
			}
			return s
		})

	fmt.Println(t)
	fmt.Println(ui.Dimmed.Render(fmt.Sprintf("  %d notebooks", len(notebooks))))
	return nil
}

func init() {
	notebooksCmd.Flags().BoolVar(&notebooksJSON, "json", false, "Output as JSON array")
	notebooksCmd.Flags().BoolVar(&notebooksPlain, "plain", false, "Force plain TSV output")
	notebooksCmd.Flags().StringVarP(&notebooksQuery, "query", "q", "", "Filter by name")

	notebooksCmd.AddCommand(notebooksOpenCmd)
	rootCmd.AddCommand(notebooksCmd)
}
