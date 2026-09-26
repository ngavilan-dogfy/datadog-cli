package cmd

import (
	"fmt"

	"github.com/ngavilan-dogfy/datadog-cli/ui"

	"github.com/spf13/cobra"
)

var metricsMetaJSON bool

var metricsMetaCmd = &cobra.Command{
	Use:   "meta <metric-name>",
	Short: "Show metric metadata (type, unit, description)",
	Long: `Fetch a metric's metadata: type (gauge/count/rate), unit, description
and source integration. Useful before querying a metric you don't know —
the unit tells you how to interpret the numbers.

Examples:
  datadog metrics meta system.cpu.user
  datadog metrics meta aws.elb.latency --json`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		name := args[0]

		meta, err := client.GetMetricMetadata(name)
		if err != nil {
			return err
		}

		if metricsMetaJSON || !isTTY() {
			return printJSON(map[string]interface{}{
				"metric":          name,
				"type":            meta.Type,
				"unit":            meta.Unit,
				"per_unit":        meta.PerUnit,
				"description":     meta.Description,
				"short_name":      meta.ShortName,
				"integration":     meta.Integration,
				"statsd_interval": meta.StatsdInterval,
			})
		}

		fmt.Println(ui.Title.Render(" " + name))
		type row struct{ label, value string }
		rows := []row{
			{"Type", meta.Type},
			{"Unit", meta.Unit},
			{"Per unit", meta.PerUnit},
			{"Integration", meta.Integration},
			{"Description", meta.Description},
		}
		for _, r := range rows {
			if r.value == "" {
				continue
			}
			fmt.Printf("%s %s\n", ui.Label.Render("  "+r.label+":"), r.value)
		}
		return nil
	},
}

func init() {
	metricsMetaCmd.Flags().BoolVar(&metricsMetaJSON, "json", false, "Output as JSON")
	metricsCmd.AddCommand(metricsMetaCmd)
}
