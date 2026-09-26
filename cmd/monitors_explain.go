package cmd

import (
	"fmt"
	"math"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/ngavilan-dogfy/datadog-cli/ui"

	"github.com/spf13/cobra"
)

var (
	explainWin  windowFlags
	explainJSON bool
	explainMD   bool
)

// monitorExplained is a monitor and what its data did lately.
type monitorExplained struct {
	ID         int64             `json:"id"`
	Name       string            `json:"name"`
	Type       string            `json:"type"`
	State      string            `json:"state"`
	URL        string            `json:"url,omitempty"`
	Query      string            `json:"query"`
	Evaluates  string            `json:"evaluates,omitempty"` // "avg over last_5m"
	Data       string            `json:"data_query,omitempty"`
	Comparator string            `json:"comparator,omitempty"`
	Critical   *float64          `json:"critical,omitempty"`
	Warning    *float64          `json:"warning,omitempty"`
	Notifies   []string          `json:"notifies,omitempty"`
	Muted      bool              `json:"muted,omitempty"`
	Groups     []string          `json:"groups_not_ok,omitempty"`
	Series     []describedSeries `json:"series,omitempty"`
	Crossings  []string          `json:"crossings,omitempty"`                  // when the data was past the threshold
	Evaluated  bool              `json:"evaluated_like_the_monitor,omitempty"` // series rolled up over its window
	Logs       *patternsReport   `json:"logs,omitempty"`
	Problems   []string          `json:"problems,omitempty"`
	From       time.Time         `json:"from"`
	To         time.Time         `json:"to"`
}

var monitorsExplainCmd = &cobra.Command{
	Use:   "explain <monitor-id | link>",
	Short: "What a monitor watches, its state, who it wakes, and what its data did",
	Long: `Explain a monitor in plain terms: what it evaluates (the data query, the
window, the thresholds), its state and the groups that aren't OK, who it
notifies — and what its data did over the window: each series described, and
when it was past the thresholds. For log monitors, the patterns of the logs
it counts.

Answers "why did this fire?" and "would this have fired?" — and for agents,
the whole monitor in one read.

Examples:
  datadog monitors explain 12345
  datadog monitors explain https://app.datadoghq.eu/monitors/12345 --since 1d
  datadog monitors explain 12345 --md`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		arg := args[0]
		if l, ok := parseDDLink(arg); ok && l.Kind == "monitor" {
			arg = l.ID
		}
		id, err := strconv.ParseInt(strings.TrimSpace(arg), 10, 64)
		if err != nil {
			return fmt.Errorf("%q isn't a monitor id", args[0])
		}
		from, to, err := explainWin.resolve()
		if err != nil {
			return err
		}
		ex, err := explainMonitor(id, from, to)
		if err != nil {
			return err
		}
		switch {
		case explainJSON:
			return printJSON(ex)
		case explainMD:
			fmt.Print(ex.markdown())
			return nil
		}
		fmt.Print(ex.text(isTTY()))
		return nil
	},
}

var (
	reMetricMonitor = regexp.MustCompile(`^\s*(\w+)\((last_\w+)\):(.+?)\s*(>=|<=|>|<|==|!=)\s*(-?[\d.eE+]+)\s*$`)
	reLogMonitor    = regexp.MustCompile(`^\s*logs\("((?:[^"\\]|\\.)*)"\)`)
	reHandle        = regexp.MustCompile(`@[\w.\-+/]+(?:@[\w.\-]+)?`)
)

func explainMonitor(id int64, from, to time.Time) (*monitorExplained, error) {
	m, err := client.GetMonitor(id)
	if err != nil {
		return nil, err
	}
	ex := &monitorExplained{ID: m.ID, Name: m.Name, Type: m.Type, State: m.OverallState, Query: m.Query,
		URL: client.BrowseURL(fmt.Sprintf("/monitors/%d", m.ID)), From: from, To: to, Muted: len(m.MatchingDowntimes) > 0}
	seen := map[string]bool{}
	for _, h := range reHandle.FindAllString(m.Message, -1) {
		h = strings.TrimRight(h, ".,;:")
		if !seen[h] && !strings.HasPrefix(h, "@is_") {
			seen[h] = true
			ex.Notifies = append(ex.Notifies, h)
		}
	}
	if len(ex.Notifies) == 0 {
		ex.Problems = append(ex.Problems, "notifies no one: its message has no @handle")
	}
	threshold := func(k string) *float64 {
		switch v := m.Options.Thresholds[k].(type) {
		case float64:
			return &v
		case string:
			if f, err := strconv.ParseFloat(v, 64); err == nil {
				return &f
			}
		}
		return nil
	}
	ex.Critical, ex.Warning = threshold("critical"), threshold("warning")
	if groups, err := client.GetMonitorGroups(id); err == nil {
		for _, g := range groups {
			if st := strings.ToLower(g.Status); st != "ok" && st != "" {
				ex.Groups = append(ex.Groups, g.Status+": "+g.Name)
			}
		}
		sort.Strings(ex.Groups)
	}

	switch {
	case reMetricMonitor.MatchString(m.Query):
		p := reMetricMonitor.FindStringSubmatch(m.Query)
		ex.Evaluates = p[1] + " over " + p[2]
		ex.Data, ex.Comparator = strings.TrimSpace(p[3]), p[4]
		if ex.Critical == nil {
			if v, err := strconv.ParseFloat(p[5], 64); err == nil {
				ex.Critical = &v
			}
		}
		// Read the data the way the monitor does: each value aggregated
		// over the monitor's window.
		data := ex.Data
		if q, ok := monitorWindowQuery(p[1], p[2], ex.Data); ok {
			data, ex.Evaluated = q, true
		}
		res, err := describeMetric(data, from, to, "", 10)
		if err != nil {
			ex.Problems = append(ex.Problems, "its query failed: "+err.Error())
			break
		}
		ex.Series = res.Series
		if len(res.Series) == 0 {
			ex.Problems = append(ex.Problems, "its query has no data in the window")
			if res.Hint != "" {
				ex.Problems = append(ex.Problems, "→ "+res.Hint)
			}
		}
		ex.Crossings = crossings(ex, from, to)
	case reLogMonitor.MatchString(m.Query):
		q := reLogMonitor.FindStringSubmatch(m.Query)[1]
		q = strings.ReplaceAll(q, `\"`, `"`)
		ex.Data = q
		if rest := strings.SplitN(m.Query, ">", 2); len(rest) == 2 {
			ex.Comparator = ">"
		}
		if rep, err := logPatterns(q, from, to, 1000, ""); err == nil {
			if len(rep.Patterns) > 5 {
				rep.More = len(rep.Patterns) - 5
				rep.Patterns = rep.Patterns[:5]
			}
			ex.Logs = rep
		} else {
			ex.Problems = append(ex.Problems, "its logs couldn't be read: "+err.Error())
		}
	default:
		ex.Evaluates = "a " + m.Type + " (its data isn't read here)"
	}
	return ex, nil
}

var (
	reWindow     = regexp.MustCompile(`^last_(\d+)([smhdw])$`)
	reMetricTerm = regexp.MustCompile(`\w+:[\w.]+\{[^}]*\}(?:\s*by\s*\{[^}]*\})?(?:\.as_count\(\)|\.as_rate\(\))?`)
)

// monitorWindowQuery rolls every metric term of a monitor's query up over
// the monitor's window with its aggregation, so each point is a value the
// monitor would have compared with its threshold.
func monitorWindowQuery(agg, window, data string) (string, bool) {
	m := reWindow.FindStringSubmatch(window)
	if m == nil || strings.Contains(data, ".rollup(") {
		return "", false
	}
	switch agg {
	case "sum", "avg", "min", "max":
	default:
		return "", false
	}
	n, _ := strconv.Atoi(m[1])
	secs := n * map[string]int{"s": 1, "m": 60, "h": 3600, "d": 86400, "w": 604800}[m[2]]
	if secs < 60 {
		return "", false
	}
	out := reMetricTerm.ReplaceAllStringFunc(data, func(term string) string {
		return fmt.Sprintf("%s.rollup(%s, %d)", term, agg, secs)
	})
	return out, out != data
}

// crossings says when each series was past the critical threshold.
func crossings(ex *monitorExplained, from, to time.Time) []string {
	if ex.Critical == nil || ex.Comparator == "" {
		return nil
	}
	past := func(v float64) bool {
		switch ex.Comparator {
		case ">":
			return v > *ex.Critical
		case ">=":
			return v >= *ex.Critical
		case "<":
			return v < *ex.Critical
		case "<=":
			return v <= *ex.Critical
		}
		return false
	}
	clock := clockFor(from, to)
	var out []string
	for _, s := range ex.Series {
		sum := s.Summary
		if sum.Points == 0 {
			continue
		}
		// From the summary alone: where min/max sit against the threshold.
		switch {
		case (ex.Comparator == ">" || ex.Comparator == ">=") && past(sum.Max):
			out = append(out, fmt.Sprintf("%s: above %s — peak %s at %s", s.Scope, fmtNum(*ex.Critical), fmtNum(sum.Max), clock(sum.MaxAt)))
		case (ex.Comparator == "<" || ex.Comparator == "<=") && past(sum.Min):
			out = append(out, fmt.Sprintf("%s: below %s — low %s at %s", s.Scope, fmtNum(*ex.Critical), fmtNum(sum.Min), clock(sum.MinAt)))
		}
	}
	if len(out) == 0 && len(ex.Series) > 0 {
		up := ex.Comparator == ">" || ex.Comparator == ">="
		closest := ex.Series[0].Summary.Max
		if !up {
			closest = ex.Series[0].Summary.Min
		}
		for _, s := range ex.Series {
			if up && s.Summary.Max > closest {
				closest = s.Summary.Max
			} else if !up && s.Summary.Min < closest {
				closest = s.Summary.Min
			}
		}
		margin := ""
		if *ex.Critical != 0 {
			margin = fmt.Sprintf(" (closest: %s, %.0f%% of it)", fmtNum(closest), math.Abs(closest / *ex.Critical * 100))
		}
		out = append(out, "never past the critical threshold in the window"+margin)
	}
	if !ex.Evaluated && len(out) > 0 {
		out = append(out, "(by the raw points: the monitor evaluates "+ex.Evaluates+", so this is approximate)")
	}
	return out
}

func fmtNum(v float64) string {
	return strconv.FormatFloat(v, 'g', 4, 64)
}

func (ex *monitorExplained) lines() []string {
	var l []string
	state := ex.State
	if ex.Muted {
		state += " (muted)"
	}
	l = append(l, fmt.Sprintf("state: %s", state))
	if ex.Evaluates != "" {
		l = append(l, "evaluates: "+ex.Evaluates)
	}
	if ex.Data != "" {
		l = append(l, "data: "+ex.Data)
	}
	var th []string
	if ex.Critical != nil {
		th = append(th, "critical "+ex.Comparator+" "+fmtNum(*ex.Critical))
	}
	if ex.Warning != nil {
		th = append(th, "warning "+ex.Comparator+" "+fmtNum(*ex.Warning))
	}
	if len(th) > 0 {
		l = append(l, "thresholds: "+strings.Join(th, ", "))
	}
	if len(ex.Notifies) > 0 {
		l = append(l, "notifies: "+strings.Join(ex.Notifies, " "))
	}
	for i, g := range ex.Groups {
		if i == 10 {
			l = append(l, fmt.Sprintf("… and %d more groups not OK", len(ex.Groups)-10))
			break
		}
		l = append(l, "group "+g)
	}
	return l
}

func (ex *monitorExplained) header() string {
	return fmt.Sprintf("Monitor %d · %s · %s", ex.ID, ex.Name, fmtWindow(ex.From, ex.To))
}

func (ex *monitorExplained) text(tty bool) string {
	var b strings.Builder
	paint := func(st func(...string) string, s string) string {
		if tty {
			return st(s)
		}
		return s
	}
	b.WriteString(paint(ui.Title.Render, ex.header()) + "\n")
	b.WriteString(paint(ui.Dimmed.Render, ex.URL) + "\n\n")
	for _, l := range ex.lines() {
		b.WriteString("  " + l + "\n")
	}
	if len(ex.Series) > 0 {
		b.WriteString("\n  What its data did:\n")
		for _, s := range ex.Series {
			b.WriteString("    " + s.Scope + ": " + s.Text + "\n")
		}
	}
	for _, c := range ex.Crossings {
		b.WriteString("  → " + c + "\n")
	}
	if ex.Logs != nil {
		b.WriteString("\n  The logs it counts:\n")
		for _, line := range strings.Split(strings.TrimRight(ex.Logs.text(false, 120), "\n"), "\n") {
			b.WriteString("  " + line + "\n")
		}
	}
	for _, p := range ex.Problems {
		b.WriteString("  " + paint(ui.ErrorStyle.Render, "✗ "+p) + "\n")
	}
	return b.String()
}

func (ex *monitorExplained) markdown() string {
	var b strings.Builder
	b.WriteString("### " + ex.header() + "\n\n" + ex.URL + "\n\n")
	for _, l := range ex.lines() {
		b.WriteString("- " + l + "\n")
	}
	if len(ex.Series) > 0 {
		b.WriteString("\nWhat its data did:\n\n")
		for _, s := range ex.Series {
			b.WriteString("- `" + s.Scope + "`: " + s.Text + "\n")
		}
	}
	for _, c := range ex.Crossings {
		b.WriteString("- → " + c + "\n")
	}
	if ex.Logs != nil {
		b.WriteString("\n" + ex.Logs.markdown())
	}
	for _, p := range ex.Problems {
		b.WriteString("- ✗ " + p + "\n")
	}
	return b.String()
}

func init() {
	explainWin.register(monitorsExplainCmd, 24*time.Hour)
	monitorsExplainCmd.Flags().BoolVar(&explainJSON, "json", false, "Output as JSON")
	monitorsExplainCmd.Flags().BoolVar(&explainMD, "md", false, "Output as markdown")
	monitorsCmd.AddCommand(monitorsExplainCmd)
}
