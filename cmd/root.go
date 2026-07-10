package cmd

import (
	"fmt"
	"os"
	"strings"

	"datadog-cli/config"
	"datadog-cli/datadog"

	"github.com/spf13/cobra"
)

var (
	cfg    *config.Profile
	client *datadog.Client
)

var Version = "dev"

var rootCmd = &cobra.Command{
	Use:     "datadog",
	Short:   "Fast CLI for Datadog monitoring and observability",
	Version: Version,
	Long: `A fast command-line tool for Datadog. Fully scriptable and pipe-friendly.

All commands auto-detect TTY:
  • Terminal  → colored, human-friendly output
  • Piped     → machine-readable TSV or plain text
  • --json    → structured JSON (on most commands)

Monitors:
  datadog monitors                List monitors with status
  datadog monitors show 12345     Full monitor details
  datadog monitors mute 12345     Mute a monitor
  datadog monitors unmute 12345   Unmute a monitor
  datadog monitors search "cpu"   Search monitors by name

Dashboards:
  datadog dashboards              List all dashboards
  datadog dashboards open abc-123 Open dashboard in browser

Hosts:
  datadog hosts                   List infrastructure hosts
  datadog hosts mute web-01       Mute a host
  datadog hosts unmute web-01     Unmute a host

Events:
  datadog events                  Recent events (last 24h)
  datadog events post "Deploy"    Post an event

Logs:
  datadog logs "service:api"      Search logs

Downtimes:
  datadog downtimes               List scheduled downtimes
  datadog downtimes schedule      Create a downtime
  datadog downtimes cancel <id>   Cancel a downtime

Incidents:
  datadog incidents               List incidents
  datadog incidents show <id>     Incident details

SLOs:
  datadog slos                    List SLOs
  datadog slos show <id>          SLO details

Metrics:
  datadog metrics search "cpu"    Search metric names
  datadog metrics query "avg:system.cpu.user{*}"  Query timeseries

Other:
  datadog open /monitors          Open any DD page in browser
  datadog whoami                  Validate credentials + show org
  datadog login                   Setup API key + App key
  datadog logout                  Clear credentials
  datadog config set/get/ls       Profile settings
  datadog profile create/ls/use/delete/show

Environment variables DD_API_KEY, DD_APP_KEY, DD_SITE override profile settings.`,
	PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
		cfg, _ = config.LoadActive()

		path := cmd.CommandPath()
		noAuth := []string{
			"datadog login", "datadog logout", "datadog setup",
			"datadog profile", "datadog config",
			"datadog help", "datadog completion",
		}
		needsAuth := true
		for _, prefix := range noAuth {
			if strings.HasPrefix(path, prefix) {
				needsAuth = false
				break
			}
		}

		if cfg != nil && cfg.IsAuthenticated() {
			client = datadog.NewClient(cfg.APIURL(), cfg.AppURL(), cfg.APIKey, cfg.AppKey)
		} else if needsAuth {
			if cfg == nil {
				return fmt.Errorf("no profile found — run 'datadog login' to get started")
			}
			return fmt.Errorf("not logged in — run 'datadog login' (profile: %s)", cfg.Name)
		}

		return nil
	},
}

func Execute() {
	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}
