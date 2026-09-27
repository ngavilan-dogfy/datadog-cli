package cmd

import (
	"fmt"
	"strings"
	"time"

	"github.com/ngavilan-dogfy/datadog-cli/datadog"
	"github.com/ngavilan-dogfy/datadog-cli/ui"

	"github.com/spf13/cobra"
)

var (
	evSearchWin   windowFlags
	evSearchLimit int
	evSearchJSON  bool
	evSearchPlain bool
)

var eventsSearchCmd = &cobra.Command{
	Use:   "search [query]",
	Short: "Search events: monitor transitions, deploys, resource changes, integrations",
	Long: `Search Datadog's events with the Events Explorer syntax, newest first.
Unlike 'datadog events', it sees monitor transitions (source:alert) with the
monitor behind them, and change events (a new Cloud Run revision, a config
change) with what changed.

Useful queries:
  source:alert                     every monitor transition
  source:alert service:checkout    one service's monitors
  @evt.category:change             deploys and resource changes
  service:checkout                 everything tagged with a service

Output:
  Terminal: a table · piped: TSV · --json: the events with their monitor or change

Examples:
  datadog events search "source:alert" --since 1d
  datadog events search "@evt.category:change" --since 6h
  datadog events search "service:checkout" --around "today 09:40" --window 1h --json`,
	Args: cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		from, to, err := evSearchWin.resolve()
		if err != nil {
			return err
		}
		q := "*"
		if len(args) == 1 && strings.TrimSpace(args[0]) != "" {
			q = args[0]
		}
		events, err := client.SearchEvents(q, from.UTC().Format(time.RFC3339), to.UTC().Format(time.RFC3339), evSearchLimit)
		if err != nil {
			return err
		}
		if evSearchJSON {
			if events == nil {
				events = []datadog.EventV2{}
			}
			return printJSON(events)
		}
		if !isTTY() || evSearchPlain {
			fmt.Println("TIMESTAMP\tSOURCE\tTITLE\tMONITOR\tTRANSITION")
			for _, e := range events {
				mon, tr := "", ""
				if e.Monitor != nil {
					mon, tr = fmt.Sprint(e.Monitor.ID), e.Monitor.FromState+"→"+e.Monitor.ToState
				}
				fmt.Printf("%s\t%s\t%s\t%s\t%s\n", e.Timestamp.UTC().Format(time.RFC3339), e.Source, strings.ReplaceAll(e.Title, "\t", " "), mon, tr)
			}
			return nil
		}
		fmt.Println(ui.Title.Render(fmt.Sprintf(" events · %s · %s", q, fmtWindow(from, to))))
		if len(events) == 0 {
			fmt.Println(ui.Dimmed.Render("  No events."))
			return nil
		}
		clock := clockFor(from, to)
		for _, e := range events {
			what := e.Title
			if e.Monitor != nil && e.Monitor.ToState != "" {
				what = ui.MonitorStateBadge(e.Monitor.ToState) + " " + e.Monitor.Name
			}
			fmt.Printf("  %s  %s  %s\n", ui.Dimmed.Render(clock(e.Timestamp.UnixMilli())), ui.Dimmed.Render(fmt.Sprintf("%-16s", truncRunes(e.Source, 16))), truncRunes(what, termWidth(120)-30))
		}
		if len(events) == evSearchLimit {
			fmt.Println(ui.Dimmed.Render(fmt.Sprintf("  … showing the newest %d (--limit for more)", evSearchLimit)))
		}
		return nil
	},
}

func init() {
	evSearchWin.register(eventsSearchCmd, 24*time.Hour)
	eventsSearchCmd.Flags().IntVarP(&evSearchLimit, "limit", "n", 100, "Most events to return")
	eventsSearchCmd.Flags().BoolVar(&evSearchJSON, "json", false, "Output as JSON")
	eventsSearchCmd.Flags().BoolVar(&evSearchPlain, "plain", false, "Force TSV output")
	eventsCmd.AddCommand(eventsSearchCmd)
}

// eventList is an events search for 'datadog read': the events of a link.
type eventList struct {
	Query  string            `json:"query"`
	From   time.Time         `json:"from"`
	To     time.Time         `json:"to"`
	Events []datadog.EventV2 `json:"events"`
}

func (l eventList) line(e datadog.EventV2, clock func(int64) string) string {
	what := e.Title
	if e.Monitor != nil && e.Monitor.ToState != "" {
		what = fmt.Sprintf("monitor %d %s → %s: %s", e.Monitor.ID, e.Monitor.FromState, e.Monitor.ToState, e.Monitor.Name)
	}
	src := e.Source
	if src == "" {
		src = e.Category
	}
	return clock(e.Timestamp.UnixMilli()) + "  " + src + "  " + oneLine(what, 160)
}

func (l eventList) text() string {
	var b strings.Builder
	fmt.Fprintf(&b, "Events · %s · %s · %d\n", l.Query, fmtWindow(l.From, l.To), len(l.Events))
	clock := clockFor(l.From, l.To)
	for _, e := range l.Events {
		b.WriteString("  " + l.line(e, clock) + "\n")
	}
	return b.String()
}

func (l eventList) markdown() string {
	var b strings.Builder
	fmt.Fprintf(&b, "**Events · %s** (%s, %d)\n\n", l.Query, fmtWindow(l.From, l.To), len(l.Events))
	clock := clockFor(l.From, l.To)
	for _, e := range l.Events {
		b.WriteString("- " + l.line(e, clock) + "\n")
	}
	return b.String()
}
