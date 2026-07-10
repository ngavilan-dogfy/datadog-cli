package cmd

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"datadog-cli/datadog"
	"datadog-cli/ui"

	"github.com/spf13/cobra"
)

var (
	lastMinutes int
	lastJSON    bool
	lastPlain   bool
	lastQuiet   bool // skip incidents/SLO sections, just monitors
)

var lastCmd = &cobra.Command{
	Use:   "last",
	Short: "What just happened? Recent triggered monitors, deploys, and open incidents",
	Long: `One-shot triage view for "what just fired?".

Aggregates three signals in a single call:
  • Monitors currently in Alert/Warn state (sorted by most recent trigger)
  • Deploy/notice events posted in the window
  • Currently open incidents

Defaults to a 30-minute window. Increase with --minutes.

Examples:
  datadog last                        # last 30 minutes
  datadog last --minutes 120          # last 2 hours
  datadog last --json | jq '.monitors[].id'
  datadog last --plain | grep Alert`,
	RunE: func(cmd *cobra.Command, args []string) error {
		end := time.Now()
		start := end.Add(-time.Duration(lastMinutes) * time.Minute)

		// 1) Triggered monitors — use the rich search endpoint
		monRes, _ := client.SearchMonitorsRich("status:(Alert,Warn,\"No Data\")", 100)

		// 2) Events in the window — pick the noteworthy ones (alert/error/success deploys)
		events, _ := client.ListEvents(start.Unix(), end.Unix(), "")

		// 3) Open incidents (independent of the window — they're "currently open")
		var openIncidents []datadog.IncidentData
		if !lastQuiet {
			all, _ := client.ListIncidents()
			for _, inc := range all {
				if !strings.EqualFold(inc.Attributes.Status, "resolved") {
					openIncidents = append(openIncidents, inc)
				}
			}
		}

		if lastJSON {
			return printJSON(map[string]interface{}{
				"window_minutes": lastMinutes,
				"monitors":       monRes,
				"events":         eventsToJSON(events),
				"incidents":      openIncidents,
			})
		}

		if !isTTY() || lastPlain {
			return printLastTSV(monRes, events, openIncidents)
		}

		return printLastTTY(start, end, monRes, events, openIncidents)
	},
}

func printLastTSV(monRes *datadog.MonitorSearchResponse, events []datadog.Event, incidents []datadog.IncidentData) error {
	fmt.Println("KIND\tID\tSTATE\tNAME")
	if monRes != nil {
		mons := monRes.Monitors
		sort.SliceStable(mons, func(i, j int) bool {
			return mons[i].Last_triggered_ts > mons[j].Last_triggered_ts
		})
		for _, m := range mons {
			fmt.Printf("monitor\t%d\t%s\t%s\n", m.ID, m.Status, strings.ReplaceAll(m.Name, "\t", " "))
		}
	}
	for _, e := range events {
		if !isInterestingEvent(e) {
			continue
		}
		fmt.Printf("event\t%d\t%s\t%s\n", e.ID, e.AlertType, strings.ReplaceAll(e.Title, "\t", " "))
	}
	for _, inc := range incidents {
		fmt.Printf("incident\t%s\t%s\t%s\n",
			inc.ID, inc.Attributes.Severity, strings.ReplaceAll(inc.Attributes.Title, "\t", " "))
	}
	return nil
}

func printLastTTY(start, end time.Time, monRes *datadog.MonitorSearchResponse, events []datadog.Event, incidents []datadog.IncidentData) error {
	header := fmt.Sprintf(" what happened · last %dm  (%s → %s)",
		lastMinutes, start.Format("15:04"), end.Format("15:04"))
	fmt.Println(ui.Title.Render(header))

	// Triggered monitors
	if monRes != nil && len(monRes.Monitors) > 0 {
		mons := monRes.Monitors
		sort.SliceStable(mons, func(i, j int) bool {
			return mons[i].Last_triggered_ts > mons[j].Last_triggered_ts
		})
		fmt.Println(ui.SectionHeader.Render(fmt.Sprintf("  Triggered monitors (%d)", len(mons))))
		for _, m := range mons {
			ts := ""
			if m.Last_triggered_ts > 0 {
				t := time.Unix(m.Last_triggered_ts, 0)
				ts = ui.Dimmed.Render(time.Since(t).Round(time.Second).String() + " ago")
			}
			fmt.Printf("  %s  %s  %s\n      %s  %s\n",
				ui.MonitorStateBadge(m.Status),
				ui.MonitorTypeIcon(m.Type),
				m.Name,
				ui.Dimmed.Render(fmt.Sprintf("id=%d", m.ID)), ts)
		}
	} else {
		fmt.Println(ui.SectionHeader.Render("  Triggered monitors"))
		fmt.Println(ui.Dimmed.Render("    (none)"))
	}

	// Events
	var interesting []datadog.Event
	for _, e := range events {
		if isInterestingEvent(e) {
			interesting = append(interesting, e)
		}
	}
	fmt.Println(ui.SectionHeader.Render(fmt.Sprintf("  Events (%d)", len(interesting))))
	if len(interesting) == 0 {
		fmt.Println(ui.Dimmed.Render("    (none in window)"))
	} else {
		// Sort newest first
		sort.SliceStable(interesting, func(i, j int) bool {
			return interesting[i].DateHappened > interesting[j].DateHappened
		})
		for _, e := range interesting {
			when := time.Unix(e.DateHappened, 0)
			ago := time.Since(when).Round(time.Second).String() + " ago"
			fmt.Printf("  %s  %s\n      %s  %s\n",
				eventBadge(e.AlertType), e.Title,
				ui.Dimmed.Render(ago),
				ui.Dimmed.Render(fmt.Sprintf("source=%s", e.Source)))
		}
	}

	// Open incidents
	if !lastQuiet {
		fmt.Println(ui.SectionHeader.Render(fmt.Sprintf("  Open incidents (%d)", len(incidents))))
		if len(incidents) == 0 {
			fmt.Println(ui.Dimmed.Render("    (none)"))
		} else {
			for _, inc := range incidents {
				fmt.Printf("  %s  %s\n      %s\n",
					ui.SeverityBadge(inc.Attributes.Severity),
					inc.Attributes.Title,
					ui.Dimmed.Render(fmt.Sprintf("id=%s status=%s", inc.ID, inc.Attributes.Status)))
			}
		}
	}

	return nil
}

// isInterestingEvent filters out chatter (info events, internal noise).
func isInterestingEvent(e datadog.Event) bool {
	at := strings.ToLower(e.AlertType)
	if at == "error" || at == "warning" || at == "success" {
		return true
	}
	// Deploy/notice-style events with explicit source
	if strings.Contains(strings.ToLower(e.Source), "deploy") {
		return true
	}
	// Priority "normal" with no alert_type is usually noise
	if e.Priority == "low" {
		return false
	}
	return false
}

func eventBadge(alertType string) string {
	at := strings.ToLower(alertType)
	if at == "" {
		at = "info"
	}
	return ui.MonitorStateBadge(map[string]string{
		"error":   "Alert",
		"warning": "Warn",
		"success": "OK",
		"info":    "Ignored",
	}[at])
}

func init() {
	lastCmd.Flags().IntVarP(&lastMinutes, "minutes", "m", 30, "Look-back window in minutes")
	lastCmd.Flags().BoolVar(&lastJSON, "json", false, "Output as JSON")
	lastCmd.Flags().BoolVar(&lastPlain, "plain", false, "Force TSV output")
	lastCmd.Flags().BoolVar(&lastQuiet, "quiet", false, "Skip open-incidents section")
	rootCmd.AddCommand(lastCmd)
}
