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
	Long: `Datadog from your terminal: a fast UI for people, and commands that speak
JSON for scripts and AI agents.

Get started:
  datadog setup                     Connect to your Datadog, step by step
  datadog ui                        Dashboards, monitors, logs and metrics
  datadog ui --demo                 Look around a made-up org first (no keys)

Ask it (times as people say them: "yesterday 18:00", "hace 2h"):
  datadog find checkout payments    What a question's words are in Datadog: services, endpoints, monitors…
  datadog investigate checkout      Why is it failing: correlates everything into a report with references
  datadog monitors review           Which monitors are noisy, blind or loose, with the fix for each

What's going on?
  datadog triage                    Alerts, incidents, errors and changes, in one call
  datadog services context api      One service: monitors, SLOs, errors, deploys
  datadog last                      What just fired, what was just deployed
  datadog correlate --around <time> Every signal around a moment
  datadog events search "<q>"       Monitor transitions, deploys and resource changes

Understand it (made for agents: facts instead of raw data, --md or --json):
  datadog read <link>               Any Datadog link, read with its window and variables
  datadog trace <id>                One request across services: where time went, what failed
  datadog metrics describe "<q>"    A chart in one line: levels, changes, peaks, gaps
  datadog logs patterns "<q>"       Thousands of logs as a few patterns; --compare 1d marks new ones
  datadog dashboards read <id>      What every widget shows, and what's wrong with it
  datadog monitors explain <id>     What a monitor watches, and what its data did
  datadog coverage                  Services nobody watches, with the monitors to add

Look things up:
  datadog monitors                  Monitors and their state (show, search)
  datadog logs "service:api status:error" --since 2h
  datadog metrics query "avg:system.cpu.user{*} by {host}"
  datadog dashboards · incidents · slos · hosts · events · audit · traces · rum
  datadog api <path>                Any API endpoint, with your keys

Change things (ask first when an agent does it):
  datadog monitors mute 12345 -d 1h
  datadog downtimes schedule · incidents create · events post · dashboards create

Every command adapts to where its output goes: colors in a terminal, TSV
when piped, --json on most commands. 'datadog schema' lists them all as JSON.

Keep it working:
  datadog doctor                    Check config, keys and access, with fixes
  datadog update                    Update to the latest release
  datadog skill install             Teach Claude Code this CLI (/datadog)

DD_API_KEY, DD_APP_KEY and DD_SITE override the profile, and are enough on
their own: no setup needed in CI or containers. DATADOG_READ_ONLY=1 refuses
every change for the session.`,
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
			return guardReadOnly(cmd)
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
		return guardReadOnly(cmd)
	},
}

// needsAuth reports whether a command talks to Datadog.
func needsAuth(cmd *cobra.Command) bool {
	if !cmd.HasParent() {
		return false // bare 'datadog' shows help or the welcome
	}
	if cmd == uiCmd && uiDemo {
		return false
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
