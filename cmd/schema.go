package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

// schema dumps the full command tree as JSON so scripts and AI agents can
// discover the CLI's capabilities programmatically instead of parsing --help.

var schemaFull bool

type flagSchema struct {
	Name      string `json:"name"`
	Shorthand string `json:"shorthand,omitempty"`
	Type      string `json:"type"`
	Default   string `json:"default,omitempty"`
	Usage     string `json:"usage"`
}

type cmdSchema struct {
	Name        string       `json:"name"`
	Use         string       `json:"use"`
	Short       string       `json:"short"`
	Long        string       `json:"long,omitempty"`
	Aliases     []string     `json:"aliases,omitempty"`
	JSONOutput  bool         `json:"json_output"`
	Flags       []flagSchema `json:"flags,omitempty"`
	Subcommands []cmdSchema  `json:"subcommands,omitempty"`
}

var schemaCmd = &cobra.Command{
	Use:   "schema [command...]",
	Short: "Dump the CLI command tree as JSON (for scripts and AI agents)",
	Long: `Print a machine-readable description of every command, subcommand and flag.

Designed for automation: an AI agent or script can call this once to learn
what the CLI can do, which commands support --json, and what flags exist.

Examples:
  datadog schema                    # whole command tree
  datadog schema monitors           # just the monitors subtree
  datadog schema logs --full        # include full help text (examples)
  datadog schema | jq '.subcommands[].name'`,
	RunE: func(cmd *cobra.Command, args []string) error {
		target := rootCmd
		if len(args) > 0 {
			found, _, err := rootCmd.Find(args)
			if err != nil || found == rootCmd {
				return fmt.Errorf("unknown command %q", args)
			}
			target = found
		}
		return printJSON(buildSchema(target))
	},
}

func buildSchema(c *cobra.Command) cmdSchema {
	s := cmdSchema{
		Name:    c.CommandPath(),
		Use:     c.Use,
		Short:   c.Short,
		Aliases: c.Aliases,
	}
	if schemaFull {
		s.Long = c.Long
	}

	collect := func(f *pflag.Flag) {
		if f.Hidden {
			return
		}
		if f.Name == "json" {
			s.JSONOutput = true
		}
		s.Flags = append(s.Flags, flagSchema{
			Name:      f.Name,
			Shorthand: f.Shorthand,
			Type:      f.Value.Type(),
			Default:   f.DefValue,
			Usage:     f.Usage,
		})
	}
	c.LocalFlags().VisitAll(collect)

	for _, sub := range c.Commands() {
		if sub.Hidden || sub.Name() == "help" || sub.Name() == "completion" {
			continue
		}
		s.Subcommands = append(s.Subcommands, buildSchema(sub))
	}
	return s
}

func init() {
	schemaCmd.Flags().BoolVar(&schemaFull, "full", false, "Include full help text (long descriptions with examples)")
	rootCmd.AddCommand(schemaCmd)
}
