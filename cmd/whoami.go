package cmd

import (
	"fmt"

	"github.com/ngavilan-dogfy/datadog-cli/config"
	"github.com/ngavilan-dogfy/datadog-cli/ui"

	"github.com/spf13/cobra"
)

var whoamiJSON bool

var whoamiCmd = &cobra.Command{
	Use:   "whoami",
	Short: "Validate credentials and show profile info",
	Long: `Validate your Datadog API key and show current profile configuration.

Examples:
  datadog whoami                # validate and show info
  datadog whoami --json         # JSON output`,
	RunE: func(cmd *cobra.Command, args []string) error {
		result, err := client.Validate()
		if err != nil {
			return fmt.Errorf("validation failed: %w", err)
		}

		if whoamiJSON {
			return printJSON(map[string]interface{}{
				"valid":   result.Valid,
				"profile": cfg.Name,
				"site":    cfg.Site,
				"api_url": cfg.APIURL(),
				"app_url": cfg.AppURL(),
			})
		}

		if !result.Valid {
			fmt.Println(ui.ErrorStyle.Render("  Invalid credentials"))
			return fmt.Errorf("API key is invalid — run 'datadog setup'")
		}

		fmt.Println(ui.Title.Render(" Datadog CLI"))
		fmt.Println()

		name := config.ActiveName()
		type row struct{ label, value string }
		rows := []row{
			{"Profile", name},
			{"Site", cfg.Site},
			{"API URL", cfg.APIURL()},
			{"App URL", cfg.AppURL()},
			{"Status", ui.SuccessStyle.Render("Authenticated")},
		}

		for _, r := range rows {
			label := ui.Label.Render("  " + r.label + ":")
			fmt.Printf("%s %s\n", label, r.value)
		}
		fmt.Println()

		return nil
	},
}

func init() {
	whoamiCmd.Flags().BoolVar(&whoamiJSON, "json", false, "Output as JSON")
	rootCmd.AddCommand(whoamiCmd)
}
