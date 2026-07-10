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
	slosJSON     bool
	slosPlain    bool
	slosQuery    string
	sloShowJSON  bool
)

var slosCmd = &cobra.Command{
	Use:   "slos",
	Short: "List and view SLOs",
	Long: `List Datadog Service Level Objectives (SLOs).

Output adapts automatically:
  • Terminal  → colored table with SLI, target, and error budget
  • Piped     → tab-separated plain text (TSV)
  • --json    → structured JSON array

Examples:
  datadog slos                             # all SLOs
  datadog slos -q "api"                    # filter by name
  datadog slos --json | jq '.[].name'      # extract names
  datadog slos --plain                     # TSV output`,
	RunE: func(cmd *cobra.Command, args []string) error {
		slos, err := client.ListSLOs(slosQuery)
		if err != nil {
			return err
		}

		if slosJSON {
			return printJSON(slosToJSON(slos))
		}

		if len(slos) == 0 {
			if isTTY() && !slosPlain {
				fmt.Println(ui.Dimmed.Render("  No SLOs found."))
			}
			return nil
		}

		if !isTTY() || slosPlain {
			return printSLOsTSV(slos)
		}

		return printSLOsTable(slos)
	},
}

var slosShowCmd = &cobra.Command{
	Use:   "show <slo-id>",
	Short: "Show full SLO details",
	Long: `Display complete information about a Datadog SLO.

Examples:
  datadog slos show abc123def456
  datadog slos show abc123def456 --json`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		id := args[0]

		slo, err := client.GetSLO(id)
		if err != nil {
			return err
		}

		if sloShowJSON {
			return printJSON(slo)
		}

		renderSLO(slo)
		return nil
	},
}

// --- helpers ---

type sloJSONOut struct {
	ID          string  `json:"id"`
	Name        string  `json:"name"`
	Type        string  `json:"type"`
	Target      float64 `json:"target,omitempty"`
	Timeframe   string  `json:"timeframe,omitempty"`
	Description string  `json:"description,omitempty"`
	Tags        []string `json:"tags,omitempty"`
	URL         string  `json:"url"`
}

func slosToJSON(slos []datadog.SLO) []sloJSONOut {
	out := make([]sloJSONOut, len(slos))
	for i, s := range slos {
		o := sloJSONOut{
			ID:          s.ID,
			Name:        s.Name,
			Type:        s.Type,
			Description: s.Description,
			Tags:        s.Tags,
			URL:         client.BrowseURL(fmt.Sprintf("/slo/%s", s.ID)),
		}
		if len(s.Thresholds) > 0 {
			o.Target = s.Thresholds[0].Target
			o.Timeframe = s.Thresholds[0].Timeframe
		}
		out[i] = o
	}
	return out
}

func printSLOsTSV(slos []datadog.SLO) error {
	headers := []string{"ID", "TYPE", "TARGET", "TIMEFRAME", "NAME"}
	var rows [][]string
	for _, s := range slos {
		target := "—"
		timeframe := "—"
		if len(s.Thresholds) > 0 {
			target = fmt.Sprintf("%.2f%%", s.Thresholds[0].Target)
			timeframe = s.Thresholds[0].Timeframe
		}
		rows = append(rows, []string{
			s.ID,
			s.Type,
			target,
			timeframe,
			s.Name,
		})
	}
	printTSV(headers, rows)
	return nil
}

func printSLOsTable(slos []datadog.SLO) error {
	header := " SLOs"
	if slosQuery != "" {
		header += " · " + slosQuery
	}
	fmt.Println(ui.Title.Render(header))

	var rows [][]string
	for _, s := range slos {
		name := s.Name
		if len(name) > 50 {
			name = name[:47] + "..."
		}
		target := "—"
		timeframe := "—"
		if len(s.Thresholds) > 0 {
			target = fmt.Sprintf("%.2f%%", s.Thresholds[0].Target)
			timeframe = s.Thresholds[0].Timeframe
		}
		id := s.ID
		if len(id) > 14 {
			id = id[:14]
		}

		rows = append(rows, []string{
			id,
			s.Type,
			target,
			timeframe,
			name,
		})
	}

	t := table.New().
		Headers("ID", "TYPE", "TARGET", "WINDOW", "NAME").
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
				s = s.Bold(true).Foreground(lipgloss.Color("#FFFFFF")).Width(16)
			case 1: // TYPE
				s = s.Foreground(ui.Muted).Width(8)
			case 2: // TARGET
				s = s.Foreground(ui.Success).Bold(true).Width(8)
			case 3: // WINDOW
				s = s.Foreground(ui.Muted).Width(8)
			case 4: // NAME
				s = s.Foreground(ui.Text).Width(52)
			}
			return s
		})

	fmt.Println(t)
	fmt.Println(ui.Dimmed.Render(fmt.Sprintf("  %d SLOs", len(slos))))
	return nil
}

func renderSLO(slo *datadog.SLO) {
	title := fmt.Sprintf("%s  %s",
		ui.Key.Render(slo.ID),
		slo.Name)
	fmt.Println(ui.Title.Render(title))
	fmt.Println()

	metaStyle := lipgloss.NewStyle().PaddingLeft(2)

	type row struct{ label, value string }
	rows := []row{
		{"Type", slo.Type},
	}

	if slo.Description != "" {
		desc := slo.Description
		if len(desc) > 80 {
			desc = desc[:77] + "..."
		}
		rows = append(rows, row{"Description", desc})
	}

	if len(slo.Tags) > 0 {
		rows = append(rows, row{"Tags", strings.Join(slo.Tags, ", ")})
	}

	rows = append(rows, row{"Creator", slo.Creator.Handle})

	if slo.CreatedAt > 0 {
		rows = append(rows, row{"Created", time.Unix(slo.CreatedAt, 0).Format("2006-01-02 15:04")})
	}
	if slo.ModifiedAt > 0 {
		rows = append(rows, row{"Modified", datadog.UnixRelativeTime(slo.ModifiedAt)})
	}

	for _, r := range rows {
		line := ui.Label.Render(r.label+":") + " " + r.value
		fmt.Println(metaStyle.Render(line))
	}

	// Thresholds
	if len(slo.Thresholds) > 0 {
		fmt.Println()
		fmt.Println(ui.SectionHeader.Render("  Thresholds"))
		for _, th := range slo.Thresholds {
			target := fmt.Sprintf("%.2f%%", th.Target)
			line := fmt.Sprintf("    %s  target %s",
				ui.Subtitle.Render(th.Timeframe),
				ui.SuccessStyle.Render(target))
			if th.Warning != nil {
				line += fmt.Sprintf("  warn %s",
					lipgloss.NewStyle().Foreground(ui.Warning).Render(fmt.Sprintf("%.2f%%", *th.Warning)))
			}
			fmt.Println(line)
		}
	}

	// Overall status
	if len(slo.OverallStatus) > 0 {
		fmt.Println()
		fmt.Println(ui.SectionHeader.Render("  Status"))
		for _, st := range slo.OverallStatus {
			sli := fmt.Sprintf("%.4f%%", st.SLI)
			budget := fmt.Sprintf("%.2f%%", st.ErrorBudgetRemaining)
			status := st.Status

			statusColor := ui.SLOStatusColor(status)
			sliStyled := lipgloss.NewStyle().Foreground(statusColor).Bold(true).Render(sli)
			budgetStyled := lipgloss.NewStyle().Foreground(statusColor).Render(budget)

			fmt.Printf("    %s  SLI %s  budget %s  %s\n",
				ui.Subtitle.Render(st.Timeframe),
				sliStyled,
				budgetStyled,
				lipgloss.NewStyle().Foreground(statusColor).Bold(true).Render(status))
		}
	}

	fmt.Println()
	fmt.Println(ui.Dimmed.Render(fmt.Sprintf("  %s", client.BrowseURL(fmt.Sprintf("/slo/%s", slo.ID)))))
}

func init() {
	slosCmd.Flags().BoolVar(&slosJSON, "json", false, "Output as JSON array")
	slosCmd.Flags().BoolVar(&slosPlain, "plain", false, "Force plain TSV output")
	slosCmd.Flags().StringVarP(&slosQuery, "query", "q", "", "Filter by name")

	slosShowCmd.Flags().BoolVar(&sloShowJSON, "json", false, "Output as JSON")

	slosCmd.AddCommand(slosShowCmd)
	rootCmd.AddCommand(slosCmd)
}
