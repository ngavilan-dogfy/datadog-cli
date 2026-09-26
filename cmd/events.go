package cmd

import (
	"fmt"
	"strings"
	"time"

	"github.com/ngavilan-dogfy/datadog-cli/datadog"
	"github.com/ngavilan-dogfy/datadog-cli/ui"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/lipgloss/table"
	"github.com/spf13/cobra"
)

var (
	eventsJSON     bool
	eventsPlain    bool
	eventsPriority string
	eventsHours    int
	eventPostPri   string
	eventPostAlert string
	eventPostTags  string
	eventPostTpl   string
	eventPostSet   []string
)

var eventsCmd = &cobra.Command{
	Use:   "events",
	Short: "List and post events",
	Long: `List recent Datadog events.

By default shows events from the last 24 hours.

Output adapts automatically:
  • Terminal  → colored table
  • Piped     → tab-separated plain text (TSV)
  • --json    → structured JSON array

Examples:
  datadog events                           # last 24h
  datadog events --hours 4                 # last 4 hours
  datadog events --priority normal         # filter by priority
  datadog events --json | jq '.[].title'   # extract titles`,
	RunE: func(cmd *cobra.Command, args []string) error {
		end := time.Now().Unix()
		start := end - int64(eventsHours*3600)

		events, err := client.ListEvents(start, end, eventsPriority)
		if err != nil {
			return err
		}

		if eventsJSON {
			return printJSON(eventsToJSON(events))
		}

		if len(events) == 0 {
			if isTTY() && !eventsPlain {
				fmt.Println(ui.Dimmed.Render("  No events found."))
			}
			return nil
		}

		if !isTTY() || eventsPlain {
			return printEventsTSV(events)
		}

		return printEventsTable(events)
	},
}

var eventsPostCmd = &cobra.Command{
	Use:   "post [<title> [text]]",
	Short: "Post an event",
	Long: `Post a custom event to Datadog.

Use --template to pull from ~/.config/datadog-cli/templates/events/<name>.yaml,
with {{placeholder}} substitution via repeated --set key=value flags.

Examples:
  datadog events post "Deploy v1.2.3"
  datadog events post "Maintenance" --priority low --alert-type info
  datadog events post --template maintenance --set service=api --set window=30m
  datadog events post --template deploy --set service=api --set env=prod --set version=v1.2.3`,
	Args: cobra.RangeArgs(0, 2),
	RunE: func(cmd *cobra.Command, args []string) error {
		var req datadog.PostEventRequest

		if eventPostTpl != "" {
			vars, err := parseSetFlags(eventPostSet)
			if err != nil {
				return err
			}
			tpl, err := loadEventTemplate(eventPostTpl, vars)
			if err != nil {
				return err
			}
			req = *tpl
		} else {
			if len(args) == 0 {
				return fmt.Errorf("title is required (or use --template)")
			}
			req.Title = args[0]
			req.Text = req.Title
			if len(args) > 1 {
				req.Text = args[1]
			}
		}

		// CLI flags override template
		if eventPostPri != "" {
			req.Priority = eventPostPri
		}
		if eventPostAlert != "" {
			req.AlertType = eventPostAlert
		}
		if eventPostTags != "" {
			req.Tags = append(req.Tags, strings.Split(eventPostTags, ",")...)
		}

		if u := findUnresolvedEvent(req); u != "" {
			return fmt.Errorf("unresolved placeholder %q — pass it with --set %s=VALUE", u, strings.Trim(u, "{}"))
		}

		event, err := client.PostEvent(req)
		if err != nil {
			return err
		}

		fmt.Println(ui.SuccessStyle.Render(fmt.Sprintf("  Posted event %d", event.ID)))
		fmt.Println(ui.Dimmed.Render(fmt.Sprintf("  %s", req.Title)))
		return nil
	},
}

func findUnresolvedEvent(req datadog.PostEventRequest) string {
	for _, s := range []string{req.Title, req.Text} {
		if i := strings.Index(s, "{{"); i >= 0 {
			if j := strings.Index(s[i:], "}}"); j > 0 {
				return s[i : i+j+2]
			}
		}
	}
	for _, t := range req.Tags {
		if strings.Contains(t, "{{") {
			return t
		}
	}
	return ""
}

// --- helpers ---

type eventJSONOut struct {
	ID        int64    `json:"id"`
	Title     string   `json:"title"`
	Text      string   `json:"text"`
	Priority  string   `json:"priority"`
	AlertType string   `json:"alert_type"`
	Source    string   `json:"source"`
	Host      string   `json:"host"`
	Tags      []string `json:"tags,omitempty"`
	Date      string   `json:"date"`
}

func eventsToJSON(events []datadog.Event) []eventJSONOut {
	out := make([]eventJSONOut, len(events))
	for i, e := range events {
		out[i] = eventJSONOut{
			ID:        e.ID,
			Title:     e.Title,
			Text:      e.Text,
			Priority:  e.Priority,
			AlertType: e.AlertType,
			Source:    e.Source,
			Host:      e.Host,
			Tags:      e.Tags,
			Date:      datadog.FormatUnix(e.DateHappened),
		}
	}
	return out
}

func printEventsTSV(events []datadog.Event) error {
	headers := []string{"ID", "ALERT", "PRIORITY", "SOURCE", "TITLE", "DATE"}
	var rows [][]string
	for _, e := range events {
		rows = append(rows, []string{
			fmt.Sprintf("%d", e.ID),
			e.AlertType,
			e.Priority,
			e.Source,
			e.Title,
			datadog.FormatUnix(e.DateHappened),
		})
	}
	printTSV(headers, rows)
	return nil
}

func printEventsTable(events []datadog.Event) error {
	header := fmt.Sprintf(" Events · last %dh", eventsHours)
	if eventsPriority != "" {
		header += " · " + eventsPriority
	}
	fmt.Println(ui.Title.Render(header))

	var rows [][]string
	for _, e := range events {
		title := e.Title
		if len(title) > 60 {
			title = title[:57] + "..."
		}
		source := e.Source
		if len(source) > 15 {
			source = source[:12] + "..."
		}

		rows = append(rows, []string{
			e.AlertType,
			e.Priority,
			source,
			title,
			datadog.UnixRelativeTime(e.DateHappened),
		})
	}

	t := table.New().
		Headers("ALERT", "PRI", "SOURCE", "TITLE", "WHEN").
		Rows(rows...).
		Border(lipgloss.RoundedBorder()).
		BorderStyle(lipgloss.NewStyle().Foreground(ui.Subtle)).
		StyleFunc(func(row, col int) lipgloss.Style {
			if row == table.HeaderRow {
				return lipgloss.NewStyle().Bold(true).Foreground(ui.Secondary).Padding(0, 1)
			}
			s := lipgloss.NewStyle().Padding(0, 1)
			switch col {
			case 0: // ALERT
				if row >= 0 && row < len(events) {
					s = s.Foreground(ui.EventAlertTypeColor(events[row].AlertType)).Bold(true).Width(8)
				}
			case 1: // PRI
				s = s.Foreground(ui.Muted).Width(8)
			case 2: // SOURCE
				s = s.Foreground(ui.Muted).Width(17)
			case 3: // TITLE
				s = s.Foreground(ui.Text).Width(62)
			case 4: // WHEN
				s = s.Foreground(ui.Muted)
			}
			return s
		})

	fmt.Println(t)
	fmt.Println(ui.Dimmed.Render(fmt.Sprintf("  %d events", len(events))))
	return nil
}

func init() {
	eventsCmd.Flags().BoolVar(&eventsJSON, "json", false, "Output as JSON array")
	eventsCmd.Flags().BoolVar(&eventsPlain, "plain", false, "Force plain TSV output")
	eventsCmd.Flags().StringVar(&eventsPriority, "priority", "", "Filter by priority (normal, low)")
	eventsCmd.Flags().IntVar(&eventsHours, "hours", 24, "Hours of history to show")

	eventsPostCmd.Flags().StringVar(&eventPostPri, "priority", "", "Event priority (normal, low)")
	eventsPostCmd.Flags().StringVar(&eventPostAlert, "alert-type", "", "Alert type (error, warning, info, success)")
	eventsPostCmd.Flags().StringVar(&eventPostTags, "tags", "", "Comma-separated tags (e.g. env:prod,service:api)")
	eventsPostCmd.Flags().StringVar(&eventPostTpl, "template", "", "Event template name (looks in ~/.config/datadog-cli/templates/events/<name>.yaml)")
	eventsPostCmd.Flags().StringSliceVar(&eventPostSet, "set", nil, "Template variable (repeatable: --set service=api --set env=prod)")

	eventsCmd.AddCommand(eventsPostCmd)
	rootCmd.AddCommand(eventsCmd)
}
