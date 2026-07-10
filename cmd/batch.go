package cmd

import (
	"bufio"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"datadog-cli/ui"

	"github.com/spf13/cobra"
)

var batchDuration string

var batchCmd = &cobra.Command{
	Use:   "batch",
	Short: "Batch operations on monitors",
	Long: `Run batch operations on multiple monitors.

Reads monitor IDs from stdin (one per line) and applies the action.

Examples:
  echo "12345\n67890" | datadog batch mute
  datadog monitors --state Alert --plain | awk '{print $1}' | datadog batch mute -d 1h
  datadog monitors --plain | awk '{print $1}' | datadog batch unmute
  cat monitor_ids.txt | datadog batch mute -d 30m`,
}

var batchMuteCmd = &cobra.Command{
	Use:   "mute",
	Short: "Mute monitors from stdin",
	Long: `Read monitor IDs from stdin and mute each one.

Examples:
  echo "12345" | datadog batch mute
  datadog monitors --state Alert --plain | awk '{print $1}' | datadog batch mute -d 1h`,
	RunE: func(cmd *cobra.Command, args []string) error {
		ids := readStdinLines()
		if len(ids) == 0 {
			return fmt.Errorf("no monitor IDs on stdin")
		}

		var end int64
		if batchDuration != "" {
			d, err := parseDuration(batchDuration)
			if err != nil {
				return err
			}
			end = time.Now().Add(d).Unix()
		}

		ok, fail := 0, 0
		for _, raw := range ids {
			id, err := strconv.ParseInt(strings.TrimSpace(raw), 10, 64)
			if err != nil {
				fmt.Fprintf(os.Stderr, "  skip %q (invalid ID)\n", raw)
				fail++
				continue
			}

			if err := client.MuteMonitor(id, end); err != nil {
				fmt.Fprintf(os.Stderr, "  %s %d: %s\n", ui.ErrorStyle.Render("FAIL"), id, err)
				fail++
			} else {
				fmt.Printf("  %s %d\n", ui.SuccessStyle.Render("muted"), id)
				ok++
			}
		}

		fmt.Println()
		fmt.Println(ui.Dimmed.Render(fmt.Sprintf("  %d muted, %d failed", ok, fail)))
		return nil
	},
}

var batchUnmuteCmd = &cobra.Command{
	Use:   "unmute",
	Short: "Unmute monitors from stdin",
	Long: `Read monitor IDs from stdin and unmute each one.

Examples:
  echo "12345" | datadog batch unmute
  cat monitor_ids.txt | datadog batch unmute`,
	RunE: func(cmd *cobra.Command, args []string) error {
		ids := readStdinLines()
		if len(ids) == 0 {
			return fmt.Errorf("no monitor IDs on stdin")
		}

		ok, fail := 0, 0
		for _, raw := range ids {
			id, err := strconv.ParseInt(strings.TrimSpace(raw), 10, 64)
			if err != nil {
				fmt.Fprintf(os.Stderr, "  skip %q (invalid ID)\n", raw)
				fail++
				continue
			}

			if err := client.UnmuteMonitor(id); err != nil {
				fmt.Fprintf(os.Stderr, "  %s %d: %s\n", ui.ErrorStyle.Render("FAIL"), id, err)
				fail++
			} else {
				fmt.Printf("  %s %d\n", ui.SuccessStyle.Render("unmuted"), id)
				ok++
			}
		}

		fmt.Println()
		fmt.Println(ui.Dimmed.Render(fmt.Sprintf("  %d unmuted, %d failed", ok, fail)))
		return nil
	},
}

// readStdinLines reads non-empty lines from stdin.
func readStdinLines() []string {
	var lines []string
	scanner := bufio.NewScanner(os.Stdin)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line != "" {
			lines = append(lines, line)
		}
	}
	return lines
}

func init() {
	batchMuteCmd.Flags().StringVarP(&batchDuration, "duration", "d", "", "Mute duration (30m, 1h, 2h, 1d)")

	batchCmd.AddCommand(batchMuteCmd)
	batchCmd.AddCommand(batchUnmuteCmd)
	rootCmd.AddCommand(batchCmd)
}
