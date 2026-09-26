package cmd

import (
	"errors"
	"fmt"
	"os"
	"os/exec"

	"github.com/ngavilan-dogfy/datadog-cli/internal/selfupdate"

	"github.com/spf13/cobra"
)

var (
	updateYes   bool
	updateCheck bool
)

var updateCmd = &cobra.Command{
	Use:     "update",
	Aliases: []string{"upgrade"},
	Short:   "Update datadog to the latest version",
	Long: `Update to the latest release: shows what's new, downloads it, checks the
checksum and that the new binary runs, then replaces this one. Your settings
are not touched. Every release is built from conventional commits on main;
the notes list each feature and fix.

Output:
  Terminal: an inline progress view. Without a terminal: one line per step.

Examples:
  datadog update           # see what's new, confirm, update
  datadog update --yes     # no questions
  datadog update --check   # only say whether there's a new version`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		t := updateTool()
		if updateCheck {
			rel, err := selfupdate.Latest(t)
			if errors.Is(err, selfupdate.ErrNoReleases) {
				fmt.Println("no releases published yet")
				return nil
			}
			if err != nil {
				return err
			}
			if selfupdate.IsRelease(t.Current) && !selfupdate.Newer(rel.Tag, t.Current) {
				fmt.Printf("datadog %s is the latest version\n", t.Current)
				return nil
			}
			fmt.Printf("%s is available (you have %s) — run 'datadog update'\n", rel.Tag, currentBuild().Short())
			return nil
		}
		err := selfupdate.Run(t, selfupdate.Options{
			Yes: updateYes, Interactive: interactive(), Out: os.Stdout,
			// The skill ships inside the binary: the new one refreshes it.
			OnInstalled: func(path string) {
				if skillInstalled() && exec.Command(path, "skill", "install", "--quiet").Run() == nil {
					sayOK("Claude Code skill refreshed")
					fmt.Println()
				}
			},
		})
		if selfupdate.IsQuiet(err) {
			return quietError{err}
		}
		return err
	},
}

func init() {
	updateCmd.Flags().BoolVarP(&updateYes, "yes", "y", false, "Update without asking")
	updateCmd.Flags().BoolVar(&updateCheck, "check", false, "Only check whether a newer version exists")
	rootCmd.AddCommand(updateCmd)
}
