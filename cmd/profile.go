package cmd

import (
	"fmt"

	"github.com/ngavilan-dogfy/datadog-cli/config"
	"github.com/ngavilan-dogfy/datadog-cli/ui"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/lipgloss/table"
	"github.com/spf13/cobra"
)

var profileCmd = &cobra.Command{
	Use:   "profile",
	Short: "Manage configuration profiles",
}

// --- create ---

var profileCreateCmd = &cobra.Command{
	Use:   "create <name>",
	Short: "Create a new profile",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		name := args[0]
		if _, err := config.Create(name); err != nil {
			return err
		}
		config.SetActive(name)

		fmt.Println(ui.SuccessStyle.Render(fmt.Sprintf("  Created profile %q (now active)", name)))
		fmt.Println(ui.Dimmed.Render("  Run 'datadog setup' to connect"))

		return nil
	},
}

// --- list ---

var profileListCmd = &cobra.Command{
	Use:     "ls",
	Aliases: []string{"list"},
	Short:   "List all profiles",
	RunE: func(cmd *cobra.Command, args []string) error {
		names, err := config.List()
		if err != nil {
			return err
		}

		if len(names) == 0 {
			fmt.Println(ui.Dimmed.Render("  No profiles yet. Run 'datadog setup' to create one."))
			return nil
		}

		active := config.ActiveName()
		fmt.Println(ui.Title.Render(" Profiles"))

		var rows [][]string
		for _, name := range names {
			p, err := config.Load(name)
			if err != nil {
				continue
			}

			site := p.Site
			if site == "" {
				site = "-"
			}
			auth := ""
			if p.IsAuthenticated() {
				auth = "yes"
			} else {
				auth = "-"
			}
			status := ""
			if name == active {
				status = "* active"
			}

			rows = append(rows, []string{name, site, auth, status})
		}

		t := table.New().
			Headers("NAME", "SITE", "LOGGED IN", "").
			Rows(rows...).
			Border(lipgloss.RoundedBorder()).
			BorderStyle(lipgloss.NewStyle().Foreground(ui.Subtle)).
			StyleFunc(func(row, col int) lipgloss.Style {
				if row == table.HeaderRow {
					return lipgloss.NewStyle().Bold(true).Foreground(ui.Secondary).Padding(0, 1)
				}
				s := lipgloss.NewStyle().Padding(0, 1).Foreground(ui.Text)
				if col == 0 {
					s = s.Bold(true).Foreground(lipgloss.Color("#FFFFFF"))
				}
				if col == 3 {
					s = s.Foreground(ui.Success).Bold(true)
				}
				return s
			})

		fmt.Println(t)
		return nil
	},
}

// --- use ---

var profileUseCmd = &cobra.Command{
	Use:   "use <name>",
	Short: "Switch active profile",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		name := args[0]
		if err := config.SetActive(name); err != nil {
			return err
		}

		fmt.Println(ui.SuccessStyle.Render(fmt.Sprintf("  Switched to profile %q", name)))
		return nil
	},
}

// --- delete ---

var profileDeleteCmd = &cobra.Command{
	Use:   "delete <name>",
	Short: "Delete a profile",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		name := args[0]
		if err := config.Delete(name); err != nil {
			return err
		}

		fmt.Println(ui.SuccessStyle.Render(fmt.Sprintf("  Deleted profile %q", name)))
		return nil
	},
}

// --- show ---

var profileShowCmd = &cobra.Command{
	Use:   "show",
	Short: "Show current profile details",
	RunE: func(cmd *cobra.Command, args []string) error {
		name := config.ActiveName()
		p, err := config.Load(name)
		if err != nil {
			return err
		}

		fmt.Println(ui.Title.Render(fmt.Sprintf(" Profile: %s", name)))

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
	profileCmd.AddCommand(profileCreateCmd)
	profileCmd.AddCommand(profileListCmd)
	profileCmd.AddCommand(profileUseCmd)
	profileCmd.AddCommand(profileDeleteCmd)
	profileCmd.AddCommand(profileShowCmd)
	rootCmd.AddCommand(profileCmd)
}
