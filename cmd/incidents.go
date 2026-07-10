package cmd

import (
	"fmt"
	"strings"

	"datadog-cli/datadog"
	"datadog-cli/ui"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/lipgloss/table"
	"github.com/spf13/cobra"
)

var (
	incidentsJSON     bool
	incidentsPlain    bool
	incidentShowJSON  bool
)

var incidentsCmd = &cobra.Command{
	Use:   "incidents",
	Short: "List and view incidents",
	Long: `List Datadog incidents with status, severity, and title.

Output adapts automatically:
  • Terminal  → colored table
  • Piped     → tab-separated plain text (TSV)
  • --json    → structured JSON array

Examples:
  datadog incidents                        # all incidents
  datadog incidents --json                 # JSON output
  datadog incidents --plain | grep active  # TSV + grep`,
	RunE: func(cmd *cobra.Command, args []string) error {
		incidents, err := client.ListIncidents()
		if err != nil {
			return err
		}

		if incidentsJSON {
			return printJSON(incidentsToJSON(incidents))
		}

		if len(incidents) == 0 {
			if isTTY() && !incidentsPlain {
				fmt.Println(ui.Dimmed.Render("  No incidents found."))
			}
			return nil
		}

		if !isTTY() || incidentsPlain {
			return printIncidentsTSV(incidents)
		}

		return printIncidentsTable(incidents)
	},
}

var incidentsShowCmd = &cobra.Command{
	Use:   "show <incident-id>",
	Short: "Show full incident details",
	Long: `Display complete information about a Datadog incident.

Examples:
  datadog incidents show abc-def-123
  datadog incidents show abc-def-123 --json`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		id := args[0]

		incident, err := client.GetIncident(id)
		if err != nil {
			return err
		}

		if incidentShowJSON {
			return printJSON(incident)
		}

		renderIncident(incident)
		return nil
	},
}

// --- helpers ---

type incidentJSONOut struct {
	ID               string  `json:"id"`
	Title            string  `json:"title"`
	Status           string  `json:"status"`
	Severity         string  `json:"severity"`
	CustomerImpacted bool    `json:"customer_impacted"`
	Created          string  `json:"created"`
	Resolved         *string `json:"resolved,omitempty"`
	URL              string  `json:"url"`
}

func incidentsToJSON(incidents []datadog.IncidentData) []incidentJSONOut {
	out := make([]incidentJSONOut, len(incidents))
	for i, inc := range incidents {
		out[i] = incidentJSONOut{
			ID:               inc.ID,
			Title:            inc.Attributes.Title,
			Status:           inc.Attributes.Status,
			Severity:         inc.Attributes.Severity,
			CustomerImpacted: inc.Attributes.CustomerImpacted,
			Created:          inc.Attributes.Created,
			Resolved:         inc.Attributes.Resolved,
			URL:              client.BrowseURL(fmt.Sprintf("/incidents/%s", inc.ID)),
		}
	}
	return out
}

func printIncidentsTSV(incidents []datadog.IncidentData) error {
	headers := []string{"ID", "STATUS", "SEVERITY", "TITLE", "CREATED"}
	var rows [][]string
	for _, inc := range incidents {
		rows = append(rows, []string{
			inc.ID,
			inc.Attributes.Status,
			inc.Attributes.Severity,
			inc.Attributes.Title,
			datadog.RelativeTime(inc.Attributes.Created),
		})
	}
	printTSV(headers, rows)
	return nil
}

func printIncidentsTable(incidents []datadog.IncidentData) error {
	fmt.Println(ui.Title.Render(" Incidents"))

	var rows [][]string
	for _, inc := range incidents {
		title := inc.Attributes.Title
		if len(title) > 55 {
			title = title[:52] + "..."
		}
		id := inc.ID
		if len(id) > 12 {
			id = id[:12]
		}

		rows = append(rows, []string{
			id,
			inc.Attributes.Status,
			inc.Attributes.Severity,
			title,
			datadog.RelativeTime(inc.Attributes.Created),
		})
	}

	t := table.New().
		Headers("ID", "STATUS", "SEV", "TITLE", "CREATED").
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
				if row >= 0 && row < len(incidents) {
					s = s.Foreground(ui.IncidentStatusColor(incidents[row].Attributes.Status)).Bold(true).Width(10)
				}
			case 2: // SEV
				if row >= 0 && row < len(incidents) {
					s = s.Foreground(ui.SeverityColor(incidents[row].Attributes.Severity)).Bold(true).Width(6)
				}
			case 3: // TITLE
				s = s.Foreground(ui.Text).Width(57)
			case 4: // CREATED
				s = s.Foreground(ui.Muted)
			}
			return s
		})

	fmt.Println(t)
	fmt.Println(ui.Dimmed.Render(fmt.Sprintf("  %d incidents", len(incidents))))
	return nil
}

func renderIncident(inc *datadog.IncidentData) {
	title := fmt.Sprintf("%s  %s",
		ui.Key.Render(inc.ID),
		inc.Attributes.Title)
	fmt.Println(ui.Title.Render(title))
	fmt.Println()

	metaStyle := lipgloss.NewStyle().PaddingLeft(2)

	statusStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color("#000000")).
		Background(ui.IncidentStatusColor(inc.Attributes.Status)).
		Bold(true).
		Padding(0, 1)

	type row struct{ label, value string }
	rows := []row{
		{"Status", statusStyle.Render(inc.Attributes.Status)},
	}

	if inc.Attributes.Severity != "" {
		rows = append(rows, row{"Severity", ui.SeverityBadge(inc.Attributes.Severity)})
	}

	if inc.Attributes.CustomerImpacted {
		rows = append(rows, row{"Customer", ui.ErrorStyle.Render("Impacted")})
	}
	if inc.Attributes.CustomerImpactScope != "" {
		rows = append(rows, row{"Impact", inc.Attributes.CustomerImpactScope})
	}

	if inc.Attributes.CommanderUser != nil {
		commander := inc.Attributes.CommanderUser.Name
		if commander == "" {
			commander = inc.Attributes.CommanderUser.Handle
		}
		rows = append(rows, row{"Commander", commander})
	}

	rows = append(rows, row{"Created", datadog.RelativeTime(inc.Attributes.Created)})

	if inc.Attributes.Detected != "" {
		rows = append(rows, row{"Detected", datadog.RelativeTime(inc.Attributes.Detected)})
	}
	if inc.Attributes.Resolved != nil {
		rows = append(rows, row{"Resolved", datadog.RelativeTime(*inc.Attributes.Resolved)})
	}

	if inc.Attributes.TimeToDetect != nil {
		rows = append(rows, row{"TTD", formatSeconds(*inc.Attributes.TimeToDetect)})
	}
	if inc.Attributes.TimeToRepair != nil {
		rows = append(rows, row{"TTR", formatSeconds(*inc.Attributes.TimeToRepair)})
	}

	for _, r := range rows {
		line := ui.Label.Render(r.label+":") + " " + r.value
		fmt.Println(metaStyle.Render(line))
	}

	fmt.Println()
	fmt.Println(ui.Dimmed.Render(fmt.Sprintf("  %s", client.BrowseURL(fmt.Sprintf("/incidents/%s", inc.ID)))))
}

func formatSeconds(secs int64) string {
	if secs < 60 {
		return fmt.Sprintf("%ds", secs)
	}
	if secs < 3600 {
		return fmt.Sprintf("%dm", secs/60)
	}
	h := secs / 3600
	m := (secs % 3600) / 60
	parts := []string{fmt.Sprintf("%dh", h)}
	if m > 0 {
		parts = append(parts, fmt.Sprintf("%dm", m))
	}
	return strings.Join(parts, "")
}

func init() {
	incidentsCmd.Flags().BoolVar(&incidentsJSON, "json", false, "Output as JSON array")
	incidentsCmd.Flags().BoolVar(&incidentsPlain, "plain", false, "Force plain TSV output")

	incidentsShowCmd.Flags().BoolVar(&incidentShowJSON, "json", false, "Output as JSON")

	incidentsCmd.AddCommand(incidentsShowCmd)
	rootCmd.AddCommand(incidentsCmd)
}
