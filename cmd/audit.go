package cmd

import (
	"fmt"
	"time"

	"datadog-cli/datadog"
	"datadog-cli/ui"

	"github.com/spf13/cobra"
)

var (
	auditSince string
	auditFrom  string
	auditTo    string
	auditQuery string
	auditLimit int
	auditJSON  bool
	auditPlain bool
)

type auditJSONOut struct {
	ID        string   `json:"id"`
	Timestamp string   `json:"timestamp"`
	Product   string   `json:"product"`
	Action    string   `json:"action"`
	Actor     string   `json:"actor"`
	Resource  string   `json:"resource"`
	Message   string   `json:"message,omitempty"`
	Tags      []string `json:"tags,omitempty"`
}

var auditCmd = &cobra.Command{
	Use:   "audit",
	Short: "Audit trail: who changed what, and when",
	Long: `Search the Datadog audit trail: monitor edits, dashboard changes,
key rotations, downtime scheduling — every configuration change with its author.

Invaluable during triage: an alert that fires right after someone edited a
monitor or deleted a downtime usually isn't a coincidence.

Examples:
  datadog audit                                # last hour of changes
  datadog audit --since 24h                    # last day
  datadog audit --query "@evt.name:Monitor"    # only monitor changes
  datadog audit --query "@usr.email:ana@x.com" # by author
  datadog audit --json | jq '.[].event'`,
	RunE: func(cmd *cobra.Command, args []string) error {
		from, to, err := auditWindow()
		if err != nil {
			return err
		}

		events, err := client.SearchAuditEvents(auditQuery, from.Format(time.RFC3339), to.Format(time.RFC3339), auditLimit)
		if err != nil {
			return err
		}

		if auditJSON {
			return printJSON(auditToJSON(events))
		}

		if len(events) == 0 {
			if isTTY() && !auditPlain {
				fmt.Println(ui.Dimmed.Render("  No audit events found."))
			}
			return nil
		}

		if !isTTY() || auditPlain {
			headers := []string{"TIMESTAMP", "PRODUCT", "ACTION", "ACTOR", "RESOURCE"}
			var rows [][]string
			for _, e := range events {
				rows = append(rows, []string{
					e.Attributes.Timestamp, e.Attributes.Product(), e.Attributes.Action(),
					e.Attributes.Actor(), e.Attributes.ResourceName(),
				})
			}
			printTSV(headers, rows)
			return nil
		}

		fmt.Println(ui.Title.Render(fmt.Sprintf(" audit · %s → %s",
			from.Format("2006-01-02 15:04"), to.Format("15:04"))))
		for _, e := range events {
			ts := e.Attributes.Timestamp
			if t := datadog.ParseTime(ts); !t.IsZero() {
				ts = t.Format("01-02 15:04:05")
			}
			actor := e.Attributes.Actor()
			if actor == "" {
				actor = "—"
			}
			line := fmt.Sprintf("  %s  %s %s  %s",
				ui.Dimmed.Render(ts),
				ui.SuccessStyle.Render(e.Attributes.Product()),
				e.Attributes.Action(),
				actor)
			if res := e.Attributes.ResourceName(); res != "" {
				line += ui.Dimmed.Render("  → " + res)
			}
			fmt.Println(line)
		}
		fmt.Println(ui.Dimmed.Render(fmt.Sprintf("  %d changes", len(events))))
		return nil
	},
}

func auditWindow() (time.Time, time.Time, error) {
	if auditFrom != "" && auditTo != "" {
		f, err := parseDate(auditFrom)
		if err != nil {
			return time.Time{}, time.Time{}, fmt.Errorf("--from: %w", err)
		}
		t, err := parseDate(auditTo)
		if err != nil {
			return time.Time{}, time.Time{}, fmt.Errorf("--to: %w", err)
		}
		return f, t, nil
	}
	lookback := time.Hour
	if auditSince != "" {
		d, err := parseDuration(auditSince)
		if err != nil {
			return time.Time{}, time.Time{}, fmt.Errorf("--since: %w", err)
		}
		lookback = d
	}
	to := time.Now()
	return to.Add(-lookback), to, nil
}

func auditToJSON(events []datadog.AuditEvent) []auditJSONOut {
	out := make([]auditJSONOut, len(events))
	for i, e := range events {
		out[i] = auditJSONOut{
			ID:        e.ID,
			Timestamp: e.Attributes.Timestamp,
			Product:   e.Attributes.Product(),
			Action:    e.Attributes.Action(),
			Actor:     e.Attributes.Actor(),
			Resource:  e.Attributes.ResourceName(),
			Message:   e.Attributes.Message,
			Tags:      e.Attributes.Tags,
		}
	}
	return out
}

func init() {
	auditCmd.Flags().StringVar(&auditSince, "since", "", "Lookback window as duration (default 1h; e.g. 30m, 24h, 7d)")
	auditCmd.Flags().StringVar(&auditFrom, "from", "", "Start timestamp (RFC3339 or epoch)")
	auditCmd.Flags().StringVar(&auditTo, "to", "", "End timestamp (RFC3339 or epoch)")
	auditCmd.Flags().StringVar(&auditQuery, "query", "", "Audit query (e.g. \"@evt.name:Monitor\", \"@usr.email:x@y.com\")")
	auditCmd.Flags().IntVarP(&auditLimit, "limit", "n", 50, "Maximum number of results")
	auditCmd.Flags().BoolVar(&auditJSON, "json", false, "Output as JSON array")
	auditCmd.Flags().BoolVar(&auditPlain, "plain", false, "Force plain TSV output")
	rootCmd.AddCommand(auditCmd)
}
