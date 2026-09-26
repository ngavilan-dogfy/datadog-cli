package cmd

import (
	"fmt"
	"strings"

	"github.com/ngavilan-dogfy/datadog-cli/datadog"
	"github.com/ngavilan-dogfy/datadog-cli/ui"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/lipgloss/table"
	"github.com/spf13/cobra"
)

var (
	syntheticsJSON  bool
	syntheticsPlain bool
	syntheticsType  string
	synthShowJSON   bool
)

var syntheticsCmd = &cobra.Command{
	Use:     "synthetics",
	Aliases: []string{"synth"},
	Short:   "List, view, and trigger synthetic tests",
	Long: `List Datadog synthetic tests with status and type.

Output adapts automatically:
  • Terminal  → colored table
  • Piped     → tab-separated plain text (TSV)
  • --json    → structured JSON array

Filtering:
  --type    Filter by type (api, browser)

Examples:
  datadog synthetics                       # all tests
  datadog synth                            # alias
  datadog synth --type api                 # API tests only
  datadog synth --json | jq '.[].name'     # extract names
  datadog synth --plain | grep paused      # find paused tests`,
	RunE: func(cmd *cobra.Command, args []string) error {
		tests, err := client.ListSynthetics()
		if err != nil {
			return err
		}

		if syntheticsType != "" {
			tests = filterSyntheticsByType(tests, syntheticsType)
		}

		if syntheticsJSON {
			return printJSON(syntheticsToJSON(tests))
		}

		if len(tests) == 0 {
			if isTTY() && !syntheticsPlain {
				fmt.Println(ui.Dimmed.Render("  No synthetic tests found."))
			}
			return nil
		}

		if !isTTY() || syntheticsPlain {
			return printSyntheticsTSV(tests)
		}

		return printSyntheticsTable(tests)
	},
}

var syntheticsShowCmd = &cobra.Command{
	Use:   "show <public-id>",
	Short: "Show full synthetic test details",
	Long: `Display complete information about a Datadog synthetic test.

Examples:
  datadog synthetics show abc-def-123
  datadog synth show abc-def-123 --json`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		id := args[0]

		test, err := client.GetSynthetic(id)
		if err != nil {
			return err
		}

		if synthShowJSON {
			return printJSON(test)
		}

		renderSynthetic(test)
		return nil
	},
}

var syntheticsTriggerCmd = &cobra.Command{
	Use:   "trigger <public-id> [public-id...]",
	Short: "Trigger synthetic test(s)",
	Long: `Trigger one or more synthetic tests on demand.

Examples:
  datadog synthetics trigger abc-def-123
  datadog synth trigger abc-def-123 xyz-456-789
  echo "abc-def-123" | datadog synth trigger -`,
	Args: cobra.MinimumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		ids := args
		if len(ids) == 1 && ids[0] == "-" {
			ids = readStdinLines()
		}

		result, err := client.TriggerSynthetics(ids)
		if err != nil {
			return err
		}

		fmt.Println(ui.SuccessStyle.Render(fmt.Sprintf("  Triggered %d test(s)", len(ids))))
		if result.BatchID != "" {
			fmt.Println(ui.Dimmed.Render(fmt.Sprintf("  Batch ID: %s", result.BatchID)))
		}
		for _, r := range result.Results {
			fmt.Println(ui.Dimmed.Render(fmt.Sprintf("  %s → %s", r.PublicID, r.ResultID)))
		}
		return nil
	},
}

// --- helpers ---

func filterSyntheticsByType(tests []datadog.SyntheticsTest, t string) []datadog.SyntheticsTest {
	var filtered []datadog.SyntheticsTest
	for _, test := range tests {
		if strings.EqualFold(test.Type, t) {
			filtered = append(filtered, test)
		}
	}
	return filtered
}

type syntheticJSONOut struct {
	PublicID string   `json:"public_id"`
	Name     string   `json:"name"`
	Type     string   `json:"type"`
	Subtype  string   `json:"subtype,omitempty"`
	Status   string   `json:"status"`
	URL      string   `json:"url,omitempty"`
	Tags     []string `json:"tags,omitempty"`
	Modified string   `json:"modified"`
}

func syntheticsToJSON(tests []datadog.SyntheticsTest) []syntheticJSONOut {
	out := make([]syntheticJSONOut, len(tests))
	for i, t := range tests {
		out[i] = syntheticJSONOut{
			PublicID: t.PublicID,
			Name:     t.Name,
			Type:     t.Type,
			Subtype:  t.Subtype,
			Status:   t.Status,
			URL:      t.Config.Request.URL,
			Tags:     t.Tags,
			Modified: t.ModifiedAt,
		}
	}
	return out
}

func printSyntheticsTSV(tests []datadog.SyntheticsTest) error {
	headers := []string{"PUBLIC_ID", "STATUS", "TYPE", "SUBTYPE", "NAME", "URL"}
	var rows [][]string
	for _, t := range tests {
		rows = append(rows, []string{
			t.PublicID,
			t.Status,
			t.Type,
			t.Subtype,
			t.Name,
			t.Config.Request.URL,
		})
	}
	printTSV(headers, rows)
	return nil
}

func printSyntheticsTable(tests []datadog.SyntheticsTest) error {
	header := " Synthetic Tests"
	if syntheticsType != "" {
		header += " · " + syntheticsType
	}
	fmt.Println(ui.Title.Render(header))

	var rows [][]string
	for _, t := range tests {
		name := t.Name
		if len(name) > 45 {
			name = name[:42] + "..."
		}
		target := t.Config.Request.URL
		if target == "" && t.Config.Request.Host != "" {
			target = fmt.Sprintf("%s:%d", t.Config.Request.Host, t.Config.Request.Port)
		}
		if len(target) > 35 {
			target = target[:32] + "..."
		}
		subtype := t.Subtype
		if subtype == "" {
			subtype = t.Type
		}

		rows = append(rows, []string{
			t.PublicID,
			t.Status,
			subtype,
			name,
			target,
		})
	}

	tbl := table.New().
		Headers("ID", "STATUS", "TYPE", "NAME", "TARGET").
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
				if row >= 0 && row < len(tests) {
					if tests[row].Status == "live" {
						s = s.Foreground(ui.Success).Bold(true)
					} else {
						s = s.Foreground(ui.Muted)
					}
				}
				s = s.Width(8)
			case 2: // TYPE
				s = s.Foreground(ui.Secondary).Width(10)
			case 3: // NAME
				s = s.Foreground(ui.Text).Width(47)
			case 4: // TARGET
				s = s.Foreground(ui.Muted).Width(37)
			}
			return s
		})

	fmt.Println(tbl)
	fmt.Println(ui.Dimmed.Render(fmt.Sprintf("  %d tests", len(tests))))
	return nil
}

func renderSynthetic(t *datadog.SyntheticsTest) {
	title := fmt.Sprintf("%s  %s",
		ui.Key.Render(t.PublicID),
		t.Name)
	fmt.Println(ui.Title.Render(title))
	fmt.Println()

	metaStyle := lipgloss.NewStyle().PaddingLeft(2)

	statusStyle := lipgloss.NewStyle().Bold(true)
	if t.Status == "live" {
		statusStyle = statusStyle.Foreground(ui.Success)
	} else {
		statusStyle = statusStyle.Foreground(ui.Muted)
	}

	type row struct{ label, value string }
	rows := []row{
		{"Status", statusStyle.Render(t.Status)},
		{"Type", t.Type},
	}
	if t.Subtype != "" {
		rows = append(rows, row{"Subtype", t.Subtype})
	}

	if t.Config.Request.URL != "" {
		method := t.Config.Request.Method
		if method == "" {
			method = "GET"
		}
		rows = append(rows, row{"Request", method + " " + t.Config.Request.URL})
	} else if t.Config.Request.Host != "" {
		rows = append(rows, row{"Host", fmt.Sprintf("%s:%d", t.Config.Request.Host, t.Config.Request.Port)})
	}

	if len(t.Locations) > 0 {
		rows = append(rows, row{"Locations", strings.Join(t.Locations, ", ")})
	}

	if t.Options.TickEvery > 0 {
		rows = append(rows, row{"Interval", fmt.Sprintf("%ds", t.Options.TickEvery)})
	}

	if len(t.Tags) > 0 {
		rows = append(rows, row{"Tags", strings.Join(t.Tags, ", ")})
	}

	rows = append(rows, row{"Creator", t.CreatedBy.Handle})

	if t.CreatedAt != "" {
		rows = append(rows, row{"Created", t.CreatedAt})
	}
	if t.ModifiedAt != "" {
		rows = append(rows, row{"Modified", datadog.RelativeTime(t.ModifiedAt)})
	}

	for _, r := range rows {
		line := ui.Label.Render(r.label+":") + " " + r.value
		fmt.Println(metaStyle.Render(line))
	}

	// Assertions
	if len(t.Config.Assertions) > 0 {
		fmt.Println()
		fmt.Println(ui.SectionHeader.Render("  Assertions"))
		for _, a := range t.Config.Assertions {
			assertStyle := lipgloss.NewStyle().PaddingLeft(4).Foreground(ui.Text)
			fmt.Println(assertStyle.Render(fmt.Sprintf("%s %s %v", a.Type, a.Operator, a.Target)))
		}
	}

	// Message
	if t.Message != "" {
		fmt.Println()
		fmt.Println(ui.SectionHeader.Render("  Message"))
		msgStyle := lipgloss.NewStyle().PaddingLeft(4).Foreground(ui.Text)
		msg := t.Message
		if len(msg) > 500 {
			msg = msg[:497] + "..."
		}
		fmt.Println(msgStyle.Render(msg))
	}

	fmt.Println()
	fmt.Println(ui.Dimmed.Render(fmt.Sprintf("  %s", client.BrowseURL(fmt.Sprintf("/synthetics/details/%s", t.PublicID)))))
}

func init() {
	syntheticsCmd.Flags().BoolVar(&syntheticsJSON, "json", false, "Output as JSON array")
	syntheticsCmd.Flags().BoolVar(&syntheticsPlain, "plain", false, "Force plain TSV output")
	syntheticsCmd.Flags().StringVar(&syntheticsType, "type", "", "Filter by type (api, browser)")

	syntheticsShowCmd.Flags().BoolVar(&synthShowJSON, "json", false, "Output as JSON")

	syntheticsCmd.AddCommand(syntheticsShowCmd)
	syntheticsCmd.AddCommand(syntheticsTriggerCmd)
	rootCmd.AddCommand(syntheticsCmd)
}
