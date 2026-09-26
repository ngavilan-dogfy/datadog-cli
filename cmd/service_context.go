package cmd

import (
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/ngavilan-dogfy/datadog-cli/datadog"
	"github.com/ngavilan-dogfy/datadog-cli/ui"

	"github.com/spf13/cobra"
)

// services context <name>: the one-call dossier for a single service —
// catalog metadata, its monitors, SLOs, log volume, error logs, error
// spans, events and downtimes, gathered concurrently into one document.

var (
	svcCtxSince string
	svcCtxFrom  string
	svcCtxTo    string
	svcCtxLimit int
	svcCtxJSON  bool
)

type serviceContextOut struct {
	GeneratedAt string            `json:"generated_at"`
	Service     string            `json:"service"`
	Window      triageWindowOut   `json:"window"`
	Summary     map[string]int    `json:"summary"`
	Errors      map[string]string `json:"errors,omitempty"`

	Catalog           *datadog.ServiceCatalogEntry `json:"catalog,omitempty"`
	Monitors          []datadog.MonitorSearchHit   `json:"monitors"`
	SLOs              []datadog.SLO                `json:"slos"`
	LogVolumeByStatus map[string]int               `json:"log_volume_by_status"`
	ErrorLogs         []logJSONOut                 `json:"error_logs"`
	ErrorSpans        []spanJSONOut                `json:"error_spans"`
	Events            []eventJSONOut               `json:"events"`
	DowntimesActive   []datadog.DowntimeData       `json:"downtimes_active"`
}

var servicesContextCmd = &cobra.Command{
	Use:   "context <service-name>",
	Short: "Everything about one service, in one call",
	Long: `Gather the full picture of a single service concurrently:

  • Service Catalog entry (team, tier, links, contacts)
  • All monitors tagged service:<name>, with current state
  • SLOs tagged with the service
  • Log volume by status in the window (is the error rate unusual?)
  • Recent error logs
  • Recent error spans from APM (failing endpoints)
  • Events tagged with the service (deploys, alerts)
  • Active downtimes covering the service

One command for "what is going on with service X?" — designed for
incident drill-down and AI agents. Sections that fail are reported
under "errors" without losing the rest.

Examples:
  datadog services context api
  datadog svc context api --since 6h --json
  datadog svc context api --json | jq '.summary'`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		name := args[0]

		from, to, err := svcCtxWindow()
		if err != nil {
			return err
		}
		fromStr := from.Format(time.RFC3339)
		toStr := to.Format(time.RFC3339)

		out := serviceContextOut{
			GeneratedAt: time.Now().UTC().Format(time.RFC3339),
			Service:     name,
			Window:      triageWindowOut{From: fromStr, To: toStr},
			Errors:      map[string]string{},
		}

		var mu sync.Mutex
		var wg sync.WaitGroup
		fail := func(section string, err error) {
			mu.Lock()
			out.Errors[section] = err.Error()
			mu.Unlock()
		}
		run := func(f func()) {
			wg.Add(1)
			go func() {
				defer wg.Done()
				f()
			}()
		}

		run(func() {
			svc, err := client.GetService(name)
			if err != nil {
				fail("catalog", err)
				return
			}
			out.Catalog = svc
		})

		run(func() {
			res, err := client.SearchMonitorsRich(fmt.Sprintf("tag:%q", "service:"+name), 100)
			if err != nil {
				fail("monitors", err)
				return
			}
			out.Monitors = res.Monitors
		})

		run(func() {
			slos, err := client.ListSLOs("")
			if err != nil {
				fail("slos", err)
				return
			}
			for _, slo := range slos {
				if hasTag(slo.Tags, "service:"+name) {
					out.SLOs = append(out.SLOs, slo)
				}
			}
		})

		run(func() {
			res, err := client.AggregateLogs("service:"+name, fromStr, toStr, []string{"status"}, 10)
			if err != nil {
				fail("log_volume", err)
				return
			}
			vol := map[string]int{}
			for _, b := range res.Data.Buckets {
				vol[b.By["status"]] = bucketCount(b)
			}
			out.LogVolumeByStatus = vol
		})

		run(func() {
			res, err := client.SearchLogs("service:"+name+" status:error", fromStr, toStr, svcCtxLimit)
			if err != nil {
				fail("error_logs", err)
				return
			}
			out.ErrorLogs = logsToJSON(res.Data)
		})

		run(func() {
			res, err := client.SearchSpans("service:"+name+" status:error", fromStr, toStr, 10)
			if err != nil {
				fail("error_spans", err)
				return
			}
			out.ErrorSpans = spansToJSON(res.Data)
		})

		run(func() {
			events, err := client.ListEvents(from.Unix(), to.Unix(), "")
			if err != nil {
				fail("events", err)
				return
			}
			var filtered []datadog.Event
			for _, e := range events {
				if hasTag(e.Tags, "service:"+name) {
					filtered = append(filtered, e)
				}
			}
			out.Events = eventsToJSON(filtered)
		})

		run(func() {
			dts, err := client.ListDowntimes()
			if err != nil {
				fail("downtimes", err)
				return
			}
			for _, d := range dts {
				if strings.EqualFold(d.Attributes.Status, "active") &&
					strings.Contains(d.Attributes.Scope, name) {
					out.DowntimesActive = append(out.DowntimesActive, d)
				}
			}
		})

		wg.Wait()

		alerting := 0
		for _, m := range out.Monitors {
			if m.Status == "Alert" || m.Status == "Warn" {
				alerting++
			}
		}
		out.Summary = map[string]int{
			"monitors":          len(out.Monitors),
			"monitors_alerting": alerting,
			"slos":              len(out.SLOs),
			"error_logs":        len(out.ErrorLogs),
			"error_spans":       len(out.ErrorSpans),
			"events":            len(out.Events),
			"downtimes_active":  len(out.DowntimesActive),
			"log_volume_error":  out.LogVolumeByStatus["error"],
		}
		if len(out.Errors) == 0 {
			out.Errors = nil
		}

		if svcCtxJSON || !isTTY() {
			return printJSON(out)
		}
		printServiceContextTTY(out)
		return nil
	},
}

// bucketCount extracts the count from an aggregate bucket (computes are
// keyed c0, c1... by the API; we only ever request one).
func bucketCount(b datadog.LogsAggregateBucket) int {
	for _, v := range b.Computes {
		if f, ok := v.(float64); ok {
			return int(f)
		}
	}
	return 0
}

func svcCtxWindow() (time.Time, time.Time, error) {
	if svcCtxFrom != "" && svcCtxTo != "" {
		f, err := parseDate(svcCtxFrom)
		if err != nil {
			return time.Time{}, time.Time{}, fmt.Errorf("--from: %w", err)
		}
		t, err := parseDate(svcCtxTo)
		if err != nil {
			return time.Time{}, time.Time{}, fmt.Errorf("--to: %w", err)
		}
		return f, t, nil
	}
	lookback := time.Hour
	if svcCtxSince != "" {
		d, err := parseDuration(svcCtxSince)
		if err != nil {
			return time.Time{}, time.Time{}, fmt.Errorf("--since: %w", err)
		}
		lookback = d
	}
	to := time.Now()
	return to.Add(-lookback), to, nil
}

func printServiceContextTTY(out serviceContextOut) {
	fmt.Println(ui.Title.Render(fmt.Sprintf(" service · %s · %s → %s",
		out.Service, out.Window.From, out.Window.To)))

	if out.Catalog != nil {
		s := out.Catalog.Attributes.Schema
		fmt.Println(ui.SectionHeader.Render("  Catalog"))
		if s.Team != "" {
			fmt.Printf("%s %s\n", ui.Label.Render("  Team:"), s.Team)
		}
		if s.Tier != "" {
			fmt.Printf("%s %s\n", ui.Label.Render("  Tier:"), s.Tier)
		}
		if s.Description != "" {
			fmt.Printf("%s %s\n", ui.Label.Render("  Description:"), s.Description)
		}
	}

	fmt.Println(ui.SectionHeader.Render("  Log volume by status"))
	if len(out.LogVolumeByStatus) == 0 {
		fmt.Println(ui.Dimmed.Render("    (no logs in window)"))
	} else {
		for status, count := range out.LogVolumeByStatus {
			fmt.Printf("  %s %d\n", ui.Label.Render(status+":"), count)
		}
	}

	fmt.Println(ui.SectionHeader.Render(fmt.Sprintf("  Monitors (%d, %d triggered)",
		out.Summary["monitors"], out.Summary["monitors_alerting"])))
	for _, m := range out.Monitors {
		fmt.Printf("  %s  %s\n", ui.MonitorStateBadge(m.Status), m.Name)
	}

	if len(out.ErrorSpans) > 0 {
		fmt.Println(ui.SectionHeader.Render(fmt.Sprintf("  Error spans (%d)", len(out.ErrorSpans))))
		for _, s := range out.ErrorSpans {
			fmt.Printf("  %s  %s %s\n", ui.Dimmed.Render(s.Timestamp), s.Resource,
				ui.Dimmed.Render(fmt.Sprintf("%.0fms trace=%s", s.DurationMS, s.TraceID)))
		}
	}

	if len(out.ErrorLogs) > 0 {
		fmt.Println(ui.SectionHeader.Render(fmt.Sprintf("  Error logs (%d)", len(out.ErrorLogs))))
		for _, l := range out.ErrorLogs {
			msg := strings.ReplaceAll(l.Message, "\n", " ")
			if len(msg) > 100 {
				msg = msg[:97] + "..."
			}
			fmt.Printf("  %s  %s\n", ui.Dimmed.Render(l.Timestamp), msg)
		}
	}

	fmt.Println(ui.SectionHeader.Render(fmt.Sprintf("  SLOs (%d) · Events (%d) · Active downtimes (%d)",
		len(out.SLOs), len(out.Events), len(out.DowntimesActive))))

	if len(out.Errors) > 0 {
		fmt.Println(ui.SectionHeader.Render("  Sections with errors"))
		for k, v := range out.Errors {
			fmt.Printf("  %s %s: %s\n", ui.ErrorStyle.Render("!"), k, v)
		}
	}

	fmt.Println()
	fmt.Println(ui.Dimmed.Render("  Full detail: datadog services context " + out.Service + " --json"))
}

func init() {
	servicesContextCmd.Flags().StringVar(&svcCtxSince, "since", "", "Lookback window as duration (default 1h; e.g. 30m, 6h, 1d)")
	servicesContextCmd.Flags().StringVar(&svcCtxFrom, "from", "", "Start timestamp (RFC3339 or epoch)")
	servicesContextCmd.Flags().StringVar(&svcCtxTo, "to", "", "End timestamp (RFC3339 or epoch)")
	servicesContextCmd.Flags().IntVarP(&svcCtxLimit, "limit", "n", 20, "Max error logs to include")
	servicesContextCmd.Flags().BoolVar(&svcCtxJSON, "json", false, "Output as one JSON document")
	servicesCmd.AddCommand(servicesContextCmd)
}
