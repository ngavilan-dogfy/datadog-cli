package cmd

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/ngavilan-dogfy/datadog-cli/datadog"
	"github.com/ngavilan-dogfy/datadog-cli/ui"

	"github.com/spf13/cobra"
)

var (
	rumJSON     bool
	rumPlain    bool
	rumLimit    int
	rumMinutes  int
	rumFrom     string
	rumTo       string
	rumOnlyType string
)

var rumCmd = &cobra.Command{
	Use:   "rum <query>",
	Short: "Search RUM (Real User Monitoring) events",
	Long: `Search Datadog RUM events.

Filter by event type with --type: view | session | action | resource | error | long_task.

Examples:
  datadog rum "@application.id:shop"
  datadog rum "service:web" --type error
  datadog rum "@view.url:shop.example.com/fr" --minutes 60
  datadog rum "service:web @geo.country:FR" --type session
  datadog rum "*" --type error --json | jq '.[].message'`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		query := args[0]
		if rumOnlyType != "" {
			query = fmt.Sprintf("(%s) @type:%s", query, rumOnlyType)
		}
		from := rumFrom
		to := rumTo
		if from == "" {
			from = time.Now().Add(-time.Duration(rumMinutes) * time.Minute).Format(time.RFC3339)
		}
		if to == "" {
			to = time.Now().Format(time.RFC3339)
		}

		result, err := client.SearchRUM(query, from, to, rumLimit)
		if err != nil {
			return err
		}

		if rumJSON {
			return printJSON(rumToJSON(result.Data))
		}
		if len(result.Data) == 0 {
			if isTTY() && !rumPlain {
				fmt.Println(ui.Dimmed.Render("  No RUM events found."))
			}
			return nil
		}
		if !isTTY() || rumPlain {
			return printRUMTSV(result.Data)
		}
		return printRUMTable(result.Data, query)
	},
}

type rumJSONOut struct {
	Timestamp string                 `json:"timestamp"`
	Type      string                 `json:"type"`
	Service   string                 `json:"service"`
	Summary   string                 `json:"summary"`
	URL       string                 `json:"url,omitempty"`
	Tags      []string               `json:"tags,omitempty"`
	Attrs     map[string]interface{} `json:"attributes,omitempty"`
}

func rumToJSON(events []datadog.RUMEvent) []rumJSONOut {
	out := make([]rumJSONOut, len(events))
	for i, e := range events {
		out[i] = rumJSONOut{
			Timestamp: e.Attributes.Timestamp,
			Type:      rumLookupString(e.Attributes.Attributes, "type"),
			Service:   e.Attributes.Service,
			URL:       rumLookupString(e.Attributes.Attributes, "view.url"),
			Summary:   rumSummary(e),
			Tags:      e.Attributes.Tags,
			Attrs:     e.Attributes.Attributes,
		}
	}
	return out
}

func printRUMTSV(events []datadog.RUMEvent) error {
	fmt.Println("TIMESTAMP\tTYPE\tSERVICE\tSUMMARY\tURL")
	for _, e := range events {
		t := rumLookupString(e.Attributes.Attributes, "type")
		url := rumLookupString(e.Attributes.Attributes, "view.url")
		fmt.Printf("%s\t%s\t%s\t%s\t%s\n",
			e.Attributes.Timestamp, t, e.Attributes.Service,
			strings.ReplaceAll(rumSummary(e), "\t", " "), url)
	}
	return nil
}

func printRUMTable(events []datadog.RUMEvent, query string) error {
	fmt.Println(ui.Title.Render(fmt.Sprintf(" RUM events · %s · %d hits", query, len(events))))
	for _, e := range events {
		t := rumLookupString(e.Attributes.Attributes, "type")
		url := rumLookupString(e.Attributes.Attributes, "view.url")
		stateBadge := "Ignored"
		if t == "error" {
			stateBadge = "Alert"
		} else if t == "long_task" {
			stateBadge = "Warn"
		}
		fmt.Printf("  %s  %s  %s\n      %s\n",
			ui.MonitorStateBadge(stateBadge),
			ui.Subtitle.Render(t),
			rumSummary(e),
			ui.Dimmed.Render(fmt.Sprintf("svc=%s url=%s ts=%s",
				e.Attributes.Service, url, e.Attributes.Timestamp)))
	}
	return nil
}

func rumSummary(e datadog.RUMEvent) string {
	// Best-effort: pick the most informative field per event type.
	attrs := e.Attributes.Attributes
	if attrs == nil {
		return ""
	}
	for _, k := range []string{"error.message", "action.target.name", "view.name", "resource.url"} {
		if v := rumLookupString(attrs, k); v != "" {
			return v
		}
	}
	// Fallback: marshal a tiny slice of the attrs as one-liner
	if v, ok := attrs["message"].(string); ok && v != "" {
		return v
	}
	b, _ := json.Marshal(attrs)
	s := string(b)
	if len(s) > 100 {
		s = s[:100] + "…"
	}
	return s
}

// rumLookupString navigates nested attribute objects with dot-paths.
func rumLookupString(attrs map[string]interface{}, path string) string {
	parts := strings.Split(path, ".")
	var cur interface{} = attrs
	for _, p := range parts {
		m, ok := cur.(map[string]interface{})
		if !ok {
			return ""
		}
		cur = m[p]
	}
	if s, ok := cur.(string); ok {
		return s
	}
	return ""
}

func init() {
	rumCmd.Flags().BoolVar(&rumJSON, "json", false, "Output as JSON")
	rumCmd.Flags().BoolVar(&rumPlain, "plain", false, "Force TSV output")
	rumCmd.Flags().IntVarP(&rumLimit, "limit", "n", 25, "Maximum number of events")
	rumCmd.Flags().IntVarP(&rumMinutes, "minutes", "m", 15, "Look-back window (minutes)")
	rumCmd.Flags().StringVar(&rumFrom, "from", "", "Custom start (RFC3339)")
	rumCmd.Flags().StringVar(&rumTo, "to", "", "Custom end (RFC3339)")
	rumCmd.Flags().StringVar(&rumOnlyType, "type", "", "Filter by event type: view|session|action|resource|error|long_task")
	rootCmd.AddCommand(rumCmd)
}
