package cmd

import (
	"fmt"
	"strings"

	"datadog-cli/ui"

	"github.com/spf13/cobra"
)

var tagsJSON bool

var tagsCmd = &cobra.Command{
	Use:   "tags",
	Short: "Manage host tags",
}

var tagsGetCmd = &cobra.Command{
	Use:   "get <hostname>",
	Short: "Get tags for a host",
	Long: `Get all tags assigned to a host.

Examples:
  datadog tags get web-01
  datadog tags get web-01 --json`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		hostname := args[0]

		tags, err := client.GetHostTags(hostname)
		if err != nil {
			return err
		}

		if tagsJSON {
			return printJSON(tags)
		}

		if len(tags) == 0 {
			fmt.Println(ui.Dimmed.Render(fmt.Sprintf("  No tags on %s", hostname)))
			return nil
		}

		fmt.Println(ui.Title.Render(fmt.Sprintf(" Tags · %s", hostname)))
		for _, tag := range tags {
			parts := strings.SplitN(tag, ":", 2)
			if len(parts) == 2 {
				fmt.Printf("  %s:%s\n",
					ui.Subtitle.Render(parts[0]),
					ui.Key.Render(parts[1]))
			} else {
				fmt.Printf("  %s\n", ui.Key.Render(tag))
			}
		}
		fmt.Println()
		return nil
	},
}

var tagsAddCmd = &cobra.Command{
	Use:   "add <hostname> <tag> [tag...]",
	Short: "Add tags to a host",
	Long: `Add one or more tags to a host. Tags use key:value format.

Examples:
  datadog tags add web-01 env:prod
  datadog tags add web-01 env:prod team:platform service:api`,
	Args: cobra.MinimumNArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		hostname := args[0]
		tags := args[1:]

		if err := client.AddHostTags(hostname, tags); err != nil {
			return err
		}

		fmt.Println(ui.SuccessStyle.Render(fmt.Sprintf("  Added %d tag(s) to %s", len(tags), hostname)))
		for _, tag := range tags {
			fmt.Println(ui.Dimmed.Render("  + " + tag))
		}
		return nil
	},
}

var tagsSetCmd = &cobra.Command{
	Use:   "set <hostname> <tag> [tag...]",
	Short: "Replace all tags on a host",
	Long: `Replace all tags on a host with the given tags.

Examples:
  datadog tags set web-01 env:prod team:platform`,
	Args: cobra.MinimumNArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		hostname := args[0]
		tags := args[1:]

		if err := client.UpdateHostTags(hostname, tags); err != nil {
			return err
		}

		fmt.Println(ui.SuccessStyle.Render(fmt.Sprintf("  Set %d tag(s) on %s", len(tags), hostname)))
		for _, tag := range tags {
			fmt.Println(ui.Dimmed.Render("  = " + tag))
		}
		return nil
	},
}

var tagsRmCmd = &cobra.Command{
	Use:     "rm <hostname>",
	Aliases: []string{"remove", "delete"},
	Short:   "Remove all tags from a host",
	Long: `Remove all user-assigned tags from a host.

Note: this removes ALL tags. To update tags, use 'datadog tags set'.

Examples:
  datadog tags rm web-01`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		hostname := args[0]

		if err := client.RemoveHostTags(hostname); err != nil {
			return err
		}

		fmt.Println(ui.SuccessStyle.Render(fmt.Sprintf("  Removed all tags from %s", hostname)))
		return nil
	},
}

func init() {
	tagsGetCmd.Flags().BoolVar(&tagsJSON, "json", false, "Output as JSON")

	tagsCmd.AddCommand(tagsGetCmd)
	tagsCmd.AddCommand(tagsAddCmd)
	tagsCmd.AddCommand(tagsSetCmd)
	tagsCmd.AddCommand(tagsRmCmd)
	rootCmd.AddCommand(tagsCmd)
}
