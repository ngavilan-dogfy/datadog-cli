package cmd

import (
	"fmt"
	"strings"

	"datadog-cli/config"
	"datadog-cli/ui"

	"github.com/spf13/cobra"
)

var configCmd = &cobra.Command{
	Use:   "config",
	Short: "Manage profile properties",
}

// --- set ---

var configSetCmd = &cobra.Command{
	Use:   "set <key> <value>",
	Short: "Set a property on the active profile",
	Long:  "Valid keys: " + strings.Join(config.ValidKeys, ", "),
	Args:  cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		name := config.ActiveName()
		p, err := config.Load(name)
		if err != nil {
			return err
		}

		if err := p.Set(args[0], args[1]); err != nil {
			return err
		}

		if err := config.Save(p); err != nil {
			return err
		}

		fmt.Println(ui.SuccessStyle.Render(fmt.Sprintf("  %s = %s", args[0], args[1])))
		fmt.Println(ui.Dimmed.Render("  Profile: " + name))
		return nil
	},
}

// --- get ---

var configGetCmd = &cobra.Command{
	Use:   "get <key>",
	Short: "Get a property from the active profile",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		name := config.ActiveName()
		p, err := config.Load(name)
		if err != nil {
			return err
		}

		v, err := p.Get(args[0])
		if err != nil {
			return err
		}

		if v == "" {
			v = "(not set)"
		}

		fmt.Println(v)
		return nil
	},
}

// --- list ---

var configListCmd = &cobra.Command{
	Use:     "ls",
	Aliases: []string{"list"},
	Short:   "List all properties of the active profile",
	RunE: func(cmd *cobra.Command, args []string) error {
		name := config.ActiveName()
		p, err := config.Load(name)
		if err != nil {
			return err
		}

		fmt.Println(ui.Title.Render(fmt.Sprintf(" Config [%s]", name)))

		keys := []string{"site", "api_key", "app_key", "max_results"}
		for _, k := range keys {
			v, _ := p.Get(k)
			if v == "" {
				v = ui.Dimmed.Render("(not set)")
			}
			label := ui.Label.Render("  " + k)
			fmt.Printf("%s  %s\n", label, v)
		}
		fmt.Println()

		return nil
	},
}

func init() {
	configCmd.AddCommand(configSetCmd)
	configCmd.AddCommand(configGetCmd)
	configCmd.AddCommand(configListCmd)
	rootCmd.AddCommand(configCmd)
}
