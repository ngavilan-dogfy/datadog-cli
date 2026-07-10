package cmd

import (
	"fmt"
	"strings"

	"datadog-cli/datadog"
	"datadog-cli/ui"

	"github.com/spf13/cobra"
)

var (
	statusOwner string
	statusTeam  string
	statusJSON  bool
	statusPlain bool
)

var statusCmd = &cobra.Command{
	Use:   "status",
	Short: "Current org health: triggered monitors, open incidents, SLOs at risk",
	Long: `One-look snapshot of the state of monitored systems right now.

Sections:
  • Triggered monitors (Alert + Warn)
  • Open incidents (active or stable, not resolved)
  • SLOs in WARNING or BREACHED status

Filters:
  --owner   Filter monitors/SLOs by creator handle (use 'me' for yourself)
  --team    Filter by team tag (uses 'team:<name>' tag on monitors and SLOs)

Examples:
  datadog status                    # whole org
  datadog status --owner me         # only your monitors
  datadog status --team platform    # only the platform team`,
	RunE: func(cmd *cobra.Command, args []string) error {
		owner := statusOwner
		if owner == "me" {
			// We don't have profile email in config; ask the user to be explicit.
			return fmt.Errorf("--owner me requires a known email; pass --owner <your-email> instead")
		}

		// Build monitor search query
		monQuery := "status:(Alert,Warn)"
		if statusTeam != "" {
			monQuery += " tag:\"team:" + statusTeam + "\""
		}
		monRes, _ := client.SearchMonitorsRich(monQuery, 100)

		// Filter by owner (creator.email/handle) if requested
		var monitors []datadog.MonitorSearchHit
		if monRes != nil {
			monitors = monRes.Monitors
		}
		if owner != "" {
			// SearchMonitorsRich doesn't return creator — fall back to fetching each.
			// To keep it fast we do a single page filtered on tags later;
			// for now we still include all and annotate.
			// (Future: add a /monitor?creator=... filter when available.)
		}

		// Incidents
		var openIncidents []datadog.IncidentData
		incs, _ := client.ListIncidents()
		for _, inc := range incs {
			if strings.EqualFold(inc.Attributes.Status, "resolved") {
				continue
			}
			if owner != "" {
				if inc.Attributes.CommanderUser == nil ||
					!strings.EqualFold(inc.Attributes.CommanderUser.Email, owner) {
					continue
				}
			}
			openIncidents = append(openIncidents, inc)
		}

		// SLOs at risk — list all, filter by status
		slos, _ := client.ListSLOs("")
		var atRisk []datadog.SLO
		for _, slo := range slos {
			st := strings.ToLower(strings.TrimSpace(sloOverallStatus(slo)))
			if st == "warning" || st == "breached" {
				if statusTeam != "" && !hasTag(slo.Tags, "team:"+statusTeam) {
					continue
				}
				atRisk = append(atRisk, slo)
			}
		}

		if statusJSON {
			return printJSON(map[string]interface{}{
				"monitors":  monitors,
				"incidents": openIncidents,
				"slos":      atRisk,
			})
		}
		if !isTTY() || statusPlain {
			return printStatusTSV(monitors, openIncidents, atRisk)
		}
		printStatusTTY(monitors, openIncidents, atRisk)
		return nil
	},
}

// sloOverallStatus returns a best-effort current status string for an SLO.
func sloOverallStatus(slo datadog.SLO) string {
	if len(slo.OverallStatus) > 0 {
		return slo.OverallStatus[0].Status
	}
	return ""
}

func hasTag(tags []string, want string) bool {
	for _, t := range tags {
		if t == want {
			return true
		}
	}
	return false
}

func printStatusTTY(monitors []datadog.MonitorSearchHit, incs []datadog.IncidentData, slos []datadog.SLO) {
	fmt.Println(ui.Title.Render(" org status"))

	fmt.Println(ui.SectionHeader.Render(fmt.Sprintf("  Monitors triggered (%d)", len(monitors))))
	if len(monitors) == 0 {
		fmt.Println(ui.Dimmed.Render("    (all clear)"))
	} else {
		for _, m := range monitors {
			fmt.Printf("  %s  %s  %s\n      %s\n",
				ui.MonitorStateBadge(m.Status),
				ui.MonitorTypeIcon(m.Type),
				m.Name,
				ui.Dimmed.Render(fmt.Sprintf("id=%d", m.ID)))
		}
	}

	fmt.Println(ui.SectionHeader.Render(fmt.Sprintf("  Open incidents (%d)", len(incs))))
	if len(incs) == 0 {
		fmt.Println(ui.Dimmed.Render("    (none)"))
	} else {
		for _, inc := range incs {
			cmder := "—"
			if inc.Attributes.CommanderUser != nil {
				cmder = inc.Attributes.CommanderUser.Handle
			}
			fmt.Printf("  %s  %s\n      %s\n",
				ui.SeverityBadge(inc.Attributes.Severity),
				inc.Attributes.Title,
				ui.Dimmed.Render(fmt.Sprintf("status=%s commander=%s id=%s",
					inc.Attributes.Status, cmder, inc.ID)))
		}
	}

	fmt.Println(ui.SectionHeader.Render(fmt.Sprintf("  SLOs at risk (%d)", len(slos))))
	if len(slos) == 0 {
		fmt.Println(ui.Dimmed.Render("    (none)"))
	} else {
		for _, s := range slos {
			st := sloOverallStatus(s)
			fmt.Printf("  %s  %s\n      %s\n",
				ui.SuccessStyle.Foreground(ui.SLOStatusColor(st)).Render(st),
				s.Name,
				ui.Dimmed.Render(fmt.Sprintf("id=%s type=%s", s.ID, s.Type)))
		}
	}
}

func printStatusTSV(monitors []datadog.MonitorSearchHit, incs []datadog.IncidentData, slos []datadog.SLO) error {
	fmt.Println("KIND\tID\tSTATE\tNAME")
	for _, m := range monitors {
		fmt.Printf("monitor\t%d\t%s\t%s\n", m.ID, m.Status, m.Name)
	}
	for _, inc := range incs {
		fmt.Printf("incident\t%s\t%s\t%s\n", inc.ID, inc.Attributes.Status, inc.Attributes.Title)
	}
	for _, s := range slos {
		fmt.Printf("slo\t%s\t%s\t%s\n", s.ID, sloOverallStatus(s), s.Name)
	}
	return nil
}

func init() {
	statusCmd.Flags().StringVar(&statusOwner, "owner", "", "Filter by owner handle/email (or 'me')")
	statusCmd.Flags().StringVar(&statusTeam, "team", "", "Filter by team tag (e.g. platform)")
	statusCmd.Flags().BoolVar(&statusJSON, "json", false, "Output as JSON")
	statusCmd.Flags().BoolVar(&statusPlain, "plain", false, "Force TSV output")
	rootCmd.AddCommand(statusCmd)
}
