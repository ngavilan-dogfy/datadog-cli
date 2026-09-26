package cmd

import (
	"fmt"
	"os/exec"
	"runtime"
	"strings"

	"github.com/ngavilan-dogfy/datadog-cli/ui"

	"github.com/spf13/cobra"
)

var openCmd = &cobra.Command{
	Use:   "open [path]",
	Short: "Open a Datadog page in browser",
	Long: `Open any Datadog page in your default web browser.

If no path is given, opens the Datadog home page.

Output:
  • TTY    → success message with URL
  • Piped  → just the URL

Examples:
  datadog open                             # open DD home
  datadog open /monitors                   # monitors page
  datadog open /dashboard/abc-def-ghi      # specific dashboard
  datadog open /logs?query=service:api     # logs with query
  datadog open /apm/services               # APM services
  datadog open /infrastructure/map         # infra map`,
	Args: cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		path := "/"
		if len(args) > 0 {
			path = args[0]
			if !strings.HasPrefix(path, "/") {
				path = "/" + path
			}
		}

		url := client.BrowseURL(path)

		var openBrowser *exec.Cmd
		switch runtime.GOOS {
		case "darwin":
			openBrowser = exec.Command("open", url)
		case "linux":
			openBrowser = exec.Command("xdg-open", url)
		default:
			openBrowser = exec.Command("open", url)
		}

		if err := openBrowser.Run(); err != nil {
			return fmt.Errorf("failed to open browser: %w", err)
		}

		if !isTTY() {
			fmt.Println(url)
			return nil
		}

		fmt.Println(ui.SuccessStyle.Render("  OK  Opened Datadog"))
		fmt.Println(ui.Dimmed.Render("  " + url))
		return nil
	},
}

func init() {
	rootCmd.AddCommand(openCmd)
}
