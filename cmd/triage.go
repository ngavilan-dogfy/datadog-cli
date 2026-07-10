package cmd

import (
	"fmt"
	"strings"
	"sync"
	"time"

	"datadog-cli/datadog"
	"datadog-cli/ui"

	"github.com/spf13/cobra"
)

// triage gathers every relevant signal for a time window in a single call.
// It is the context-gathering entrypoint for incident response and for AI
// agents: one invocation returns monitors, incidents, SLOs, events, error
// logs, security signals, CI pipelines and active downtimes as one JSON
// snapshot, with a summary block and per-section errors so partial API
// failures never lose the rest of the picture.

var (
	triageSince   string
	triageFrom    string
	triageTo      string
	triageAround  string
	triageWindow  time.Duration
	triageService string
	triageEnv     string
	triageQuery   string
	triageLimit   int
	triageJSON    bool
	triagePlain   bool
)

type triageSnapshot struct {
	GeneratedAt string            `json:"generated_at"`
	Site        string            `json:"site"`
	Window      triageWindowOut   `json:"window"`
	Filters     map[string]string `json:"filters,omitempty"`
	Summary     map[string]int    `json:"summary"`
	Errors      map[string]string `json:"errors,omitempty"`

	MonitorsAlerting []datadog.MonitorSearchHit `json:"monitors_alerting"`
	IncidentsOpen    []datadog.IncidentData     `json:"incidents_open"`
	SLOsAtRisk       []datadog.SLO              `json:"slos_at_risk"`
	Events           []eventJSONOut             `json:"events"`
	ErrorLogs        []logJSONOut               `json:"error_logs"`
	SecuritySignals  interface{}                `json:"security_signals,omitempty"`
	Pipelines        []datadog.CIPipelineEvent  `json:"pipelines"`
	DowntimesActive  []datadog.DowntimeData     `json:"downtimes_active"`
}

type triageWindowOut struct {
	From string `json:"from"`
	To   string `json:"to"`
}

var triageCmd = &cobra.Command{
	Use:   "triage",
	Short: "One-shot context snapshot: every signal in a window, in one call",
	Long: `Gather everything relevant to "what is going on right now" in one call:

  • Monitors currently in Alert/Warn
  • Open incidents (plus which started inside the window)
  • SLOs in WARNING or BREACHED
  • Events in the window
  • Error logs in the window (status:error, optionally scoped by service/env)
  • Security signals in the window
  • CI pipeline runs in the window (deploy correlation)
  • Active downtimes (what is muted and why)

All sections are fetched concurrently. With --json the result is a single
structured snapshot with a summary block — designed so an AI agent or script
can gather full incident context with one command. Sections that fail are
reported under "errors" instead of aborting the whole snapshot.

Time window (default: last 1h):
  --since 30m                                  lookback from now
  --around <ts> --window 10m                   centered window
  --from <ts> --to <ts>                        explicit range
  Timestamps accept RFC3339 or epoch seconds/millis.

Examples:
  datadog triage                               # last hour, everything
  datadog triage --since 15m --service api     # scoped to a service
  datadog triage --around 1747632000 --window 20m
  datadog triage --json | jq '.summary'
  datadog triage --json --query "service:api (status:error OR status:warn)"`,
	RunE: func(cmd *cobra.Command, args []string) error {
		from, to, err := resolveTriageWindow()
		if err != nil {
			return err
		}

		snap := triageSnapshot{
			GeneratedAt: time.Now().UTC().Format(time.RFC3339),
			Site:        cfg.Site,
			Window:      triageWindowOut{From: from.Format(time.RFC3339), To: to.Format(time.RFC3339)},
			Errors:      map[string]string{},
			Summary:     map[string]int{},
		}
		if triageService != "" || triageEnv != "" {
			snap.Filters = map[string]string{}
			if triageService != "" {
				snap.Filters["service"] = triageService
			}
			if triageEnv != "" {
				snap.Filters["env"] = triageEnv
			}
		}

		var mu sync.Mutex
		var wg sync.WaitGroup
		fail := func(section string, err error) {
			mu.Lock()
			snap.Errors[section] = err.Error()
			mu.Unlock()
		}
		run := func(f func()) {
			wg.Add(1)
			go func() {
				defer wg.Done()
				f()
			}()
		}

		fromStr := from.Format(time.RFC3339)
		toStr := to.Format(time.RFC3339)

		// Monitors in Alert/Warn
		run(func() {
			q := "status:(Alert OR Warn)"
			if triageService != "" {
				q += " tag:\"service:" + triageService + "\""
			}
			res, err := client.SearchMonitorsRich(q, 100)
			if err != nil {
				fail("monitors", err)
				return
			}
			snap.MonitorsAlerting = res.Monitors
		})

		// Open incidents
		run(func() {
			incs, err := client.ListIncidents()
			if err != nil {
				fail("incidents", err)
				return
			}
			for _, inc := range incs {
				if !strings.EqualFold(inc.Attributes.Status, "resolved") {
					snap.IncidentsOpen = append(snap.IncidentsOpen, inc)
				}
			}
		})

		// SLOs at risk
		run(func() {
			slos, err := client.ListSLOs("")
			if err != nil {
				fail("slos", err)
				return
			}
			for _, slo := range slos {
				st := strings.ToLower(strings.TrimSpace(sloOverallStatus(slo)))
				if st == "warning" || st == "breached" {
					snap.SLOsAtRisk = append(snap.SLOsAtRisk, slo)
				}
			}
		})

		// Events in window
		run(func() {
			events, err := client.ListEvents(from.Unix(), to.Unix(), "")
			if err != nil {
				fail("events", err)
				return
			}
			var filtered []datadog.Event
			for _, e := range events {
				if triageService != "" && !hasTag(e.Tags, "service:"+triageService) {
					continue
				}
				if triageEnv != "" && !hasTag(e.Tags, "env:"+triageEnv) {
					continue
				}
				filtered = append(filtered, e)
			}
			snap.Events = eventsToJSON(filtered)
		})

		// Error logs in window
		run(func() {
			q := triageQuery
			if q == "" {
				parts := []string{"status:error"}
				if triageService != "" {
					parts = append(parts, "service:"+triageService)
				}
				if triageEnv != "" {
					parts = append(parts, "env:"+triageEnv)
				}
				q = strings.Join(parts, " ")
			}
			res, err := client.SearchLogs(q, fromStr, toStr, triageLimit)
			if err != nil {
				fail("error_logs", err)
				return
			}
			snap.ErrorLogs = logsToJSON(res.Data)
		})

		// Security signals in window
		run(func() {
			res, err := client.SearchSecuritySignals("", fromStr, toStr, 50)
			if err != nil {
				fail("security_signals", err)
				return
			}
			snap.SecuritySignals = res
		})

		// CI pipelines in window (deploys)
		run(func() {
			res, err := client.SearchPipelines("", fromStr, toStr, 50)
			if err != nil {
				fail("pipelines", err)
				return
			}
			snap.Pipelines = res
		})

		// Active downtimes
		run(func() {
			dts, err := client.ListDowntimes()
			if err != nil {
				fail("downtimes", err)
				return
			}
			for _, d := range dts {
				if strings.EqualFold(d.Attributes.Status, "active") {
					snap.DowntimesActive = append(snap.DowntimesActive, d)
				}
			}
		})

		wg.Wait()

		secCount := 0
		if res, ok := snap.SecuritySignals.(*datadog.SecuritySignalsResponse); ok && res != nil {
			secCount = len(res.Data)
		}
		snap.Summary = map[string]int{
			"monitors_alerting": len(snap.MonitorsAlerting),
			"incidents_open":    len(snap.IncidentsOpen),
			"slos_at_risk":      len(snap.SLOsAtRisk),
			"events":            len(snap.Events),
			"error_logs":        len(snap.ErrorLogs),
			"security_signals":  secCount,
			"pipelines":         len(snap.Pipelines),
			"downtimes_active":  len(snap.DowntimesActive),
		}
		if len(snap.Errors) == 0 {
			snap.Errors = nil
		}

		if triageJSON || !isTTY() || triagePlain {
			return printJSON(snap)
		}
		printTriageTTY(snap)
		return nil
	},
}

func resolveTriageWindow() (time.Time, time.Time, error) {
	if triageFrom != "" && triageTo != "" {
		f, err := parseDate(triageFrom)
		if err != nil {
			return time.Time{}, time.Time{}, fmt.Errorf("--from: %w", err)
		}
		t, err := parseDate(triageTo)
		if err != nil {
			return time.Time{}, time.Time{}, fmt.Errorf("--to: %w", err)
		}
		return f, t, nil
	}
	if triageAround != "" {
		center, err := parseDate(triageAround)
		if err != nil {
			return time.Time{}, time.Time{}, fmt.Errorf("--around: %w", err)
		}
		w := triageWindow
		if w == 0 {
			w = 10 * time.Minute
		}
		return center.Add(-w / 2), center.Add(w / 2), nil
	}
	lookback := time.Hour
	if triageSince != "" {
		d, err := parseDuration(triageSince)
		if err != nil {
			return time.Time{}, time.Time{}, fmt.Errorf("--since: %w", err)
		}
		lookback = d
	}
	to := time.Now()
	return to.Add(-lookback), to, nil
}

func printTriageTTY(snap triageSnapshot) {
	fmt.Println(ui.Title.Render(fmt.Sprintf(" triage · %s → %s", snap.Window.From, snap.Window.To)))

	section := func(title string, count int, empty string) bool {
		fmt.Println(ui.SectionHeader.Render(fmt.Sprintf("  %s (%d)", title, count)))
		if count == 0 {
			fmt.Println(ui.Dimmed.Render("    " + empty))
			return false
		}
		return true
	}

	if section("Monitors triggered", len(snap.MonitorsAlerting), "(all clear)") {
		for _, m := range snap.MonitorsAlerting {
			fmt.Printf("  %s  %s\n      %s\n",
				ui.MonitorStateBadge(m.Status), m.Name,
				ui.Dimmed.Render(fmt.Sprintf("id=%d", m.ID)))
		}
	}

	if section("Open incidents", len(snap.IncidentsOpen), "(none)") {
		for _, inc := range snap.IncidentsOpen {
			fmt.Printf("  %s  %s\n      %s\n",
				ui.SeverityBadge(inc.Attributes.Severity), inc.Attributes.Title,
				ui.Dimmed.Render(fmt.Sprintf("status=%s id=%s", inc.Attributes.Status, inc.ID)))
		}
	}

	if section("SLOs at risk", len(snap.SLOsAtRisk), "(none)") {
		for _, s := range snap.SLOsAtRisk {
			st := sloOverallStatus(s)
			fmt.Printf("  %s  %s\n      %s\n",
				ui.SuccessStyle.Foreground(ui.SLOStatusColor(st)).Render(st), s.Name,
				ui.Dimmed.Render("id="+s.ID))
		}
	}

	if section("Events", len(snap.Events), "(none)") {
		for _, e := range snap.Events {
			fmt.Printf("  %s  %s\n", ui.Dimmed.Render(e.Date), e.Title)
		}
	}

	if section("Error logs", len(snap.ErrorLogs), "(none)") {
		for _, l := range snap.ErrorLogs {
			msg := l.Message
			if len(msg) > 100 {
				msg = msg[:97] + "..."
			}
			msg = strings.ReplaceAll(msg, "\n", " ")
			fmt.Printf("  %s  %s %s\n", ui.Dimmed.Render(l.Timestamp), l.Service, msg)
		}
	}

	fmt.Println(ui.SectionHeader.Render(fmt.Sprintf("  Security signals (%d) · Pipelines (%d) · Active downtimes (%d)",
		snap.Summary["security_signals"], len(snap.Pipelines), len(snap.DowntimesActive))))

	if len(snap.Errors) > 0 {
		fmt.Println(ui.SectionHeader.Render("  Sections with errors"))
		for k, v := range snap.Errors {
			fmt.Printf("  %s %s: %s\n", ui.ErrorStyle.Render("!"), k, v)
		}
	}

	fmt.Println()
	fmt.Println(ui.Dimmed.Render("  Full detail: datadog triage --json"))
}

func init() {
	triageCmd.Flags().StringVar(&triageSince, "since", "", "Lookback window as duration (default 1h; e.g. 30m, 2h, 1d)")
	triageCmd.Flags().StringVar(&triageFrom, "from", "", "Start timestamp (RFC3339 or epoch)")
	triageCmd.Flags().StringVar(&triageTo, "to", "", "End timestamp (RFC3339 or epoch)")
	triageCmd.Flags().StringVar(&triageAround, "around", "", "Center timestamp; pair with --window")
	triageCmd.Flags().DurationVar(&triageWindow, "window", 10*time.Minute, "Window size around --around")
	triageCmd.Flags().StringVar(&triageService, "service", "", "Scope monitors/events/logs to a service tag")
	triageCmd.Flags().StringVar(&triageEnv, "env", "", "Scope events/logs to an env tag")
	triageCmd.Flags().StringVar(&triageQuery, "query", "", "Override the error-logs query (default \"status:error\" + filters)")
	triageCmd.Flags().IntVarP(&triageLimit, "limit", "n", 20, "Max error logs to include")
	triageCmd.Flags().BoolVar(&triageJSON, "json", false, "Output as one JSON snapshot")
	triageCmd.Flags().BoolVar(&triagePlain, "plain", false, "Force JSON output even on a TTY")
	rootCmd.AddCommand(triageCmd)
}
