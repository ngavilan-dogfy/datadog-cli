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
	correlateFrom    string
	correlateTo      string
	correlateAround  string
	correlateWindow  time.Duration
	correlateJSON    bool
	correlatePlain   bool
	correlateService string
	correlateEnv     string
)

var correlateCmd = &cobra.Command{
	Use:   "correlate",
	Short: "Pull every signal (events, incidents, security) in a time window",
	Long: `Given a time window, fetch the related signals across Datadog:
events, currently-open incidents that started in the window, and security signals.
Useful right after an alert fires — paste the spike timestamp and get the
adjacent context in a single call.

Time can be specified as:
  --around <RFC3339|epoch> --window 10m    (centered window)
  --from <RFC3339|epoch> --to <RFC3339|epoch>
  default: last 15 minutes

Examples:
  datadog correlate                                    # last 15m
  datadog correlate --around 2026-05-19T04:54:00Z --window 10m
  datadog correlate --from 2026-05-19T04:48:54Z --to 2026-05-19T05:00:00Z
  datadog correlate --service web-store            # filter events/signals by service tag
  datadog correlate --json | jq '.events[].title'`,
	RunE: func(cmd *cobra.Command, args []string) error {
		from, to, err := resolveCorrelateWindow()
		if err != nil {
			return err
		}

		events, _ := client.ListEvents(from.Unix(), to.Unix(), "")
		// Filter by service tag if requested
		var filteredEvents []datadog.Event
		for _, e := range events {
			if correlateService != "" && !hasTag(e.Tags, "service:"+correlateService) {
				continue
			}
			if correlateEnv != "" && !hasTag(e.Tags, "env:"+correlateEnv) {
				continue
			}
			filteredEvents = append(filteredEvents, e)
		}

		// Incidents that *started* in the window (use Created field, RFC3339)
		var incidentsInWindow []datadog.IncidentData
		incs, _ := client.ListIncidents()
		for _, inc := range incs {
			t, err := time.Parse(time.RFC3339, inc.Attributes.Created)
			if err != nil {
				continue
			}
			if t.Before(from) || t.After(to) {
				continue
			}
			incidentsInWindow = append(incidentsInWindow, inc)
		}

		// Security signals in the window
		secRes, _ := client.SearchSecuritySignals("", from.Format(time.RFC3339), to.Format(time.RFC3339), 50)

		// Pipelines (CI) — useful to detect deploys that hit in the window
		piRes, _ := client.SearchPipelines("", from.Format(time.RFC3339), to.Format(time.RFC3339), 50)

		if correlateJSON {
			return printJSON(map[string]interface{}{
				"from":      from.Format(time.RFC3339),
				"to":        to.Format(time.RFC3339),
				"events":    eventsToJSON(filteredEvents),
				"incidents": incidentsInWindow,
				"security":  secRes,
				"pipelines": piRes,
			})
		}

		if !isTTY() || correlatePlain {
			return printCorrelateTSV(filteredEvents, incidentsInWindow, secRes, piRes)
		}
		printCorrelateTTY(from, to, filteredEvents, incidentsInWindow, secRes, piRes)
		return nil
	},
}

func resolveCorrelateWindow() (time.Time, time.Time, error) {
	if correlateFrom != "" && correlateTo != "" {
		f, err := parseDate(correlateFrom)
		if err != nil {
			return time.Time{}, time.Time{}, fmt.Errorf("--from: %w", err)
		}
		t, err := parseDate(correlateTo)
		if err != nil {
			return time.Time{}, time.Time{}, fmt.Errorf("--to: %w", err)
		}
		return f, t, nil
	}
	if correlateAround != "" {
		center, err := parseDate(correlateAround)
		if err != nil {
			return time.Time{}, time.Time{}, fmt.Errorf("--around: %w", err)
		}
		w := correlateWindow
		if w == 0 {
			w = 10 * time.Minute
		}
		return center.Add(-w / 2), center.Add(w / 2), nil
	}
	// Default: last 15 minutes
	to := time.Now()
	return to.Add(-15 * time.Minute), to, nil
}

func parseDate(s string) (time.Time, error) {
	s = strings.TrimSpace(s)
	// Try epoch seconds (10 digits) or millis (13 digits)
	if len(s) == 10 {
		if v, err := time.Parse("2006-01-02", s); err == nil {
			return v, nil
		}
	}
	for _, layout := range []string{time.RFC3339, time.RFC3339Nano, "2006-01-02T15:04:05Z", "2006-01-02 15:04:05"} {
		if v, err := time.Parse(layout, s); err == nil {
			return v, nil
		}
	}
	// Pure number → epoch (seconds or millis)
	if v, err := time.Parse("2006-01-02T15:04", s); err == nil {
		return v, nil
	}
	return time.Time{}, fmt.Errorf("unrecognized timestamp %q (try RFC3339)", s)
}

func printCorrelateTTY(from, to time.Time, events []datadog.Event, incs []datadog.IncidentData, sec *datadog.SecuritySignalsResponse, pi []datadog.CIPipelineEvent) {
	fmt.Println(ui.Title.Render(fmt.Sprintf(" correlate · %s → %s",
		from.Format("2006-01-02 15:04:05"), to.Format("15:04:05"))))

	// Events
	fmt.Println(ui.SectionHeader.Render(fmt.Sprintf("  Events (%d)", len(events))))
	if len(events) == 0 {
		fmt.Println(ui.Dimmed.Render("    (none)"))
	} else {
		sort.SliceStable(events, func(i, j int) bool { return events[i].DateHappened < events[j].DateHappened })
		for _, e := range events {
			when := time.Unix(e.DateHappened, 0).Format("15:04:05")
			fmt.Printf("  %s  %s  %s\n      %s\n",
				ui.Dimmed.Render(when),
				eventBadge(e.AlertType),
				e.Title,
				ui.Dimmed.Render(fmt.Sprintf("source=%s tags=%s",
					e.Source, strings.Join(e.Tags, ","))))
		}
	}

	// Incidents
	fmt.Println(ui.SectionHeader.Render(fmt.Sprintf("  Incidents started in window (%d)", len(incs))))
	if len(incs) == 0 {
		fmt.Println(ui.Dimmed.Render("    (none)"))
	} else {
		for _, inc := range incs {
			fmt.Printf("  %s  %s\n      %s\n",
				ui.SeverityBadge(inc.Attributes.Severity),
				inc.Attributes.Title,
				ui.Dimmed.Render(fmt.Sprintf("status=%s created=%s id=%s",
					inc.Attributes.Status, inc.Attributes.Created, inc.ID)))
		}
	}

	// Security signals
	secCount := 0
	if sec != nil {
		secCount = len(sec.Data)
	}
	fmt.Println(ui.SectionHeader.Render(fmt.Sprintf("  Security signals (%d)", secCount)))
	if secCount == 0 {
		fmt.Println(ui.Dimmed.Render("    (none)"))
	} else {
		for _, s := range sec.Data {
			fmt.Printf("  %s  %s\n",
				ui.MonitorStateBadge("Warn"),
				s.Attributes.Message)
		}
	}

	// CI pipelines / deploys
	fmt.Println(ui.SectionHeader.Render(fmt.Sprintf("  CI pipelines (%d)", len(pi))))
	if len(pi) == 0 {
		fmt.Println(ui.Dimmed.Render("    (none)"))
	} else {
		for _, p := range pi {
			fmt.Printf("  %s  %s\n      %s\n",
				ui.MonitorStateBadge(map[string]string{"success": "OK", "error": "Alert", "canceled": "Ignored"}[p.Attributes.Status]),
				p.Attributes.Name,
				ui.Dimmed.Render(fmt.Sprintf("status=%s start=%s",
					p.Attributes.Status, p.Attributes.Start)))
		}
	}
}

func printCorrelateTSV(events []datadog.Event, incs []datadog.IncidentData, sec *datadog.SecuritySignalsResponse, pi []datadog.CIPipelineEvent) error {
	fmt.Println("KIND\tTIMESTAMP\tSEVERITY/STATE\tNAME")
	for _, e := range events {
		when := time.Unix(e.DateHappened, 0).Format(time.RFC3339)
		fmt.Printf("event\t%s\t%s\t%s\n", when, e.AlertType, strings.ReplaceAll(e.Title, "\t", " "))
	}
	for _, inc := range incs {
		fmt.Printf("incident\t%s\t%s\t%s\n", inc.Attributes.Created, inc.Attributes.Severity, inc.Attributes.Title)
	}
	if sec != nil {
		for _, s := range sec.Data {
			fmt.Printf("security\t%s\t-\t%s\n", s.Attributes.Timestamp, s.Attributes.Message)
		}
	}
	for _, p := range pi {
		fmt.Printf("pipeline\t%s\t%s\t%s\n", p.Attributes.Start, p.Attributes.Status, p.Attributes.Name)
	}
	return nil
}

func init() {
	correlateCmd.Flags().StringVar(&correlateFrom, "from", "", "Start timestamp (RFC3339)")
	correlateCmd.Flags().StringVar(&correlateTo, "to", "", "End timestamp (RFC3339)")
	correlateCmd.Flags().StringVar(&correlateAround, "around", "", "Center timestamp; pair with --window")
	correlateCmd.Flags().DurationVar(&correlateWindow, "window", 10*time.Minute, "Window size around --around")
	correlateCmd.Flags().StringVar(&correlateService, "service", "", "Filter events/signals by service tag")
	correlateCmd.Flags().StringVar(&correlateEnv, "env", "", "Filter events/signals by env tag")
	correlateCmd.Flags().BoolVar(&correlateJSON, "json", false, "Output as JSON")
	correlateCmd.Flags().BoolVar(&correlatePlain, "plain", false, "Force TSV output")
	rootCmd.AddCommand(correlateCmd)
}
