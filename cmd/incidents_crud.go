package cmd

import (
	"fmt"

	"github.com/ngavilan-dogfy/datadog-cli/ui"

	"github.com/spf13/cobra"
)

var (
	incCreateSeverity string
	incCreateImpact   bool
	incCreateJSON     bool
	incUpdateStatus   string
	incUpdateTitle    string
	incUpdateSeverity string
)

var incidentsCreateCmd = &cobra.Command{
	Use:   "create <title>",
	Short: "Create a new incident",
	Long: `Create a new Datadog incident.

Severity levels: SEV-1, SEV-2, SEV-3, SEV-4, SEV-5

Examples:
  datadog incidents create "API latency spike"
  datadog incidents create "Database outage" --severity SEV-1 --customer-impacted
  datadog incidents create "Deploy rollback" --severity SEV-3 --json`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		title := args[0]

		incident, err := client.CreateIncident(title, incCreateImpact, incCreateSeverity)
		if err != nil {
			return err
		}

		if incCreateJSON {
			return printJSON(incident)
		}

		fmt.Println(ui.SuccessStyle.Render(fmt.Sprintf("  Created incident %s", incident.ID)))
		fmt.Println(ui.Dimmed.Render(fmt.Sprintf("  %s", title)))
		if incCreateSeverity != "" {
			fmt.Println(ui.Dimmed.Render(fmt.Sprintf("  Severity: %s", incCreateSeverity)))
		}
		fmt.Println(ui.Dimmed.Render(fmt.Sprintf("  %s", client.BrowseURL(fmt.Sprintf("/incidents/%s", incident.ID)))))
		return nil
	},
}

var incidentsUpdateCmd = &cobra.Command{
	Use:   "update <incident-id>",
	Short: "Update an incident",
	Long: `Update the status, title, or severity of an incident.

Status values: active, stable, resolved

Examples:
  datadog incidents update abc-123 --status stable
  datadog incidents update abc-123 --status resolved
  datadog incidents update abc-123 --severity SEV-2
  datadog incidents update abc-123 --title "Updated title" --status resolved`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		id := args[0]

		if !cmd.Flags().Changed("status") && !cmd.Flags().Changed("title") && !cmd.Flags().Changed("severity") {
			return fmt.Errorf("nothing to update — use --status, --title, or --severity")
		}

		incident, err := client.UpdateIncident(id, incUpdateStatus, incUpdateTitle, incUpdateSeverity)
		if err != nil {
			return err
		}

		fmt.Println(ui.SuccessStyle.Render(fmt.Sprintf("  Updated incident %s", id)))
		if incUpdateStatus != "" {
			fmt.Println(ui.Dimmed.Render(fmt.Sprintf("  Status: %s", incident.Attributes.Status)))
		}
		if incUpdateTitle != "" {
			fmt.Println(ui.Dimmed.Render(fmt.Sprintf("  Title: %s", incident.Attributes.Title)))
		}
		return nil
	},
}

func init() {
	incidentsCreateCmd.Flags().StringVar(&incCreateSeverity, "severity", "", "Severity (SEV-1 to SEV-5)")
	incidentsCreateCmd.Flags().BoolVar(&incCreateImpact, "customer-impacted", false, "Mark as customer-impacted")
	incidentsCreateCmd.Flags().BoolVar(&incCreateJSON, "json", false, "Output as JSON")

	incidentsUpdateCmd.Flags().StringVar(&incUpdateStatus, "status", "", "New status (active, stable, resolved)")
	incidentsUpdateCmd.Flags().StringVar(&incUpdateTitle, "title", "", "New title")
	incidentsUpdateCmd.Flags().StringVar(&incUpdateSeverity, "severity", "", "New severity (SEV-1 to SEV-5)")

	incidentsCmd.AddCommand(incidentsCreateCmd)
	incidentsCmd.AddCommand(incidentsUpdateCmd)
}
