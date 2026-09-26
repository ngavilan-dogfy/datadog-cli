package cmd

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/ngavilan-dogfy/datadog-cli/internal/selfupdate"

	"github.com/spf13/cobra"
)

// Version is set at build time: -X github.com/ngavilan-dogfy/datadog-cli/cmd.Version=v1.2.0
var Version = "dev"

func currentBuild() selfupdate.Build { return selfupdate.CurrentBuild(Version) }

// updateTool describes this CLI for the self-updater.
func updateTool() selfupdate.Tool {
	return selfupdate.Tool{Repo: "ngavilan-dogfy/datadog-cli", Binary: "datadog", Current: currentBuild().Version}
}

// cacheDir is where datadog keeps caches (TUI data, update checks).
func cacheDir() string {
	if d, err := os.UserCacheDir(); err == nil {
		return filepath.Join(d, "datadog-cli")
	}
	return filepath.Join(os.TempDir(), "datadog-cli")
}

var versionJSON bool

var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Show version, commit and where this binary lives",
	Long: `Show the version of this datadog binary, the commit it was built from and
where it's installed. Handy for bug reports and for 'datadog update'.

Output:
  TTY → human summary · piped → the version string · --json → all fields

Examples:
  datadog version
  datadog version --json`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		b := currentBuild()
		if versionJSON {
			return printJSON(b)
		}
		if !isTTY() {
			fmt.Println(b.Version)
			return nil
		}
		fmt.Println()
		fmt.Println("  " + wzAccent.Render("datadog") + " " + wzBold.Render(b.Short()))
		for _, r := range [][2]string{{"Built", b.Date}, {"Go", b.Go}, {"Platform", b.Platform}, {"Binary", tildePath(b.Path)}} {
			if r[1] != "" {
				fmt.Printf("    %s %s\n", wzMuted.Render(fmt.Sprintf("%-9s", r[0])), r[1])
			}
		}
		fmt.Println()
		return nil
	},
}

func init() {
	versionCmd.Flags().BoolVar(&versionJSON, "json", false, "Output as JSON")
	rootCmd.AddCommand(versionCmd)
	rootCmd.Version = currentBuild().Short()
}
