package cmd

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/ngavilan-dogfy/datadog-cli/config"
	"github.com/ngavilan-dogfy/datadog-cli/datadog"
	"github.com/ngavilan-dogfy/datadog-cli/internal/selfupdate"

	"github.com/spf13/cobra"
	"golang.org/x/term"
)

var (
	cfg    *config.Profile
	client *datadog.Client
)

var rootCmd = &cobra.Command{
	Use:   "datadog",
	Short: "Fast CLI for Datadog monitoring and observability",
	// Runtime errors (API failures, bad ids…) shouldn't dump the usage text.
	SilenceUsage: true,
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

Interactive:
  datadog ui                      Dashboards, monitors, logs and metrics in the terminal

Other:
  datadog open /monitors          Open any DD page in browser
  datadog whoami                  Validate credentials + show org
  datadog config set/get/ls       Profile settings
  datadog profile create/ls/use/delete/show

Setup:
  datadog setup                   Connect to your Datadog, step by step
  datadog doctor                  Check that everything works (with fixes)
  datadog update                  Update to the latest version
  datadog skill install           Teach Claude Code to use this CLI (/datadog)
  datadog logout                  Remove the keys from a profile

Environment variables DD_API_KEY, DD_APP_KEY, DD_SITE override profile settings
(and are enough on their own: no setup needed in CI or containers).`,
	// Bare 'datadog': help, or a welcome that offers setup on a fresh install.
	RunE: func(cmd *cobra.Command, args []string) error {
		if cfg != nil && cfg.IsAuthenticated() {
			return cmd.Help()
		}
		return welcome()
	},
	PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
		cfg, _ = config.LoadActive()
		if cfg != nil && cfg.IsAuthenticated() {
			client = buildClient(cfg)
			return nil
		}
		if !needsAuth(cmd) {
			return nil
		}
		// Not set up yet: in a terminal, offer the guided setup and then run
		// the command that was asked for; elsewhere, say exactly what's missing.
		if !interactive() || wantsJSON() {
			return notSetUpError()
		}
		if err := offerSetup(cmd.CommandPath()); err != nil {
			return err
		}
		cfg, _ = config.LoadActive()
		if cfg == nil || !cfg.IsAuthenticated() {
			return notSetUpError()
		}
		client = buildClient(cfg)
		return nil
	},
}

// needsAuth reports whether a command talks to Datadog.
func needsAuth(cmd *cobra.Command) bool {
	if !cmd.HasParent() {
		return false // bare 'datadog' shows help or the welcome
	}
	path := cmd.CommandPath()
	for _, prefix := range []string{
		"datadog setup", "datadog logout", "datadog doctor", "datadog version", "datadog update",
		"datadog skill", "datadog profile", "datadog config", "datadog help", "datadog completion",
		"datadog schema", "datadog __complete",
	} {
		if path == prefix || strings.HasPrefix(path, prefix+" ") {
			return false
		}
	}
	return true
}

// buildClient makes the API client for a profile.
func buildClient(p *config.Profile) *datadog.Client {
	return datadog.NewClient(p.APIURL(), p.AppURL(), p.APIKey, p.AppKey)
}

// notSetUpError says what's missing, for scripts and agents that can't
// answer questions.
func notSetUpError() error {
	var set, unset []string
	for _, v := range []string{"DD_API_KEY", "DD_APP_KEY"} {
		if os.Getenv(v) != "" {
			set = append(set, v)
		} else {
			unset = append(unset, v)
		}
	}
	if len(set) > 0 && len(unset) > 0 {
		return fmt.Errorf("%s set but %s missing — both keys are needed (plus DD_SITE unless you're on datadoghq.com)",
			strings.Join(set, ", "), strings.Join(unset, ", "))
	}
	if cfg != nil {
		return fmt.Errorf("profile %q has no keys — run 'datadog setup'", cfg.Name)
	}
	return fmt.Errorf("datadog isn't connected to Datadog yet — run 'datadog setup' in a terminal, " +
		"or set DD_API_KEY, DD_APP_KEY and DD_SITE (see 'datadog setup --help')")
}

// quietError fails the command (exit 1) without printing it again: the
// command already explained the problem.
type quietError struct{ error }

func Execute() {
	datadog.UserAgent = "datadog-cli/" + currentBuild().Version
	rootCmd.SilenceErrors = true
	notifier := startNotifier()
	err := rootCmd.Execute()
	if err != nil {
		var quiet quietError
		if !errors.As(err, &quiet) {
			fmt.Fprintln(os.Stderr, "Error:", err)
		}
		if wantsJSON() {
			// One parseable line on stderr for scripts/agents running with --json.
			if enc, jerr := json.Marshal(map[string]string{"error": err.Error()}); jerr == nil {
				fmt.Fprintln(os.Stderr, string(enc))
			}
		}
	}
	notifier.Print(os.Stderr)
	if err != nil {
		os.Exit(1)
	}
}

// startNotifier checks for a newer release in the background (cached, at
// most daily) when a person is at a terminal; the notice prints after the
// command so it never gets in the way.
func startNotifier() *selfupdate.Notifier {
	if !isTTY() || !term.IsTerminal(int(os.Stderr.Fd())) || wantsJSON() {
		return nil
	}
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "update", "upgrade", "version", "completion", "__complete", "__completeNoDesc", "ui":
			return nil
		}
	}
	return selfupdate.StartNotifier(updateTool(), cacheDir())
}

// wantsJSON reports whether --json appeared anywhere on the command line.
func wantsJSON() bool {
	for _, a := range os.Args[1:] {
		if a == "--json" {
			return true
		}
	}
	return false
}
