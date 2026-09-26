package cmd

import (
	"encoding/json"
	"fmt"
	"math"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/ngavilan-dogfy/datadog-cli/datadog"
	"github.com/ngavilan-dogfy/datadog-cli/internal/logpattern"
	"github.com/ngavilan-dogfy/datadog-cli/internal/series"
	"github.com/ngavilan-dogfy/datadog-cli/ui"
	"github.com/ngavilan-dogfy/datadog-cli/viz"

	"github.com/charmbracelet/lipgloss"
	"github.com/spf13/cobra"
)

var (
	patWin     windowFlags
	patJSON    bool
	patMD      bool
	patTop     int
	patSample  int
	patCompare string
)

type logPattern struct {
	Pattern  string    `json:"pattern"`
	Service  string    `json:"service"`
	Status   string    `json:"status"`
	Estimate int       `json:"count"`   // estimated over all matching logs
	Sampled  int       `json:"sampled"` // in the sample
	Share    float64   `json:"share"`   // of all matching logs, %
	First    time.Time `json:"first_seen"`
	Last     time.Time `json:"last_seen"`
	Example  string    `json:"example"`
	Before   *int      `json:"before,omitempty"` // estimated in the --compare window
	New      bool      `json:"new,omitempty"`    // absent in the --compare window
}

type patternsReport struct {
	Query       string       `json:"query"`
	From        time.Time    `json:"from"`
	To          time.Time    `json:"to"`
	Total       int          `json:"total"` // logs matching the query
	Sampled     int          `json:"sampled"`
	Volume      string       `json:"volume"` // how the count moved over the window
	VolumeStep  string       `json:"volume_step"`
	Compare     string       `json:"compare,omitempty"`
	TotalBefore int          `json:"total_before,omitempty"`
	Patterns    []logPattern `json:"patterns"`
	More        int          `json:"more_patterns,omitempty"`
	volume      []series.Point
}

var logsPatternsCmd = &cobra.Command{
	Use:   "patterns <query>",
	Short: "Group logs into patterns with counts: what's being logged, at a glance",
	Long: `Fold the logs matching a query into patterns — the message with its
variable parts (ids, numbers, emails, times, names) replaced by placeholders —
with an estimated count, share, first/last seen and one real example.

It reads a sample spread over the window (--sample) and scales the counts to
every matching log, so "10,000 errors" becomes the 5 things that are failing.
--compare puts each pattern next to the same window earlier and marks the
new ones: what started failing.

Output:
  Terminal: a table · --md: markdown · --json: patterns with numbers

Examples:
  datadog logs patterns "status:error"
  datadog logs patterns "service:api status:(error OR warn)" --since 4h
  datadog logs patterns "status:error" --since 1h --compare 1d      # what's new vs yesterday
  datadog logs patterns "service:checkout" --md --top 10`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		from, to, err := patWin.resolve()
		if err != nil {
			return err
		}
		rep, err := logPatterns(args[0], from, to, patSample, patCompare)
		if err != nil {
			return err
		}
		if patTop > 0 && len(rep.Patterns) > patTop {
			rep.More = len(rep.Patterns) - patTop
			rep.Patterns = rep.Patterns[:patTop]
		}
		switch {
		case patJSON:
			return printJSON(rep)
		case patMD:
			fmt.Print(rep.markdown())
			return nil
		}
		fmt.Print(rep.text(isTTY(), termWidth(120)))
		return nil
	},
}

// logSample is a sample of the logs matching a query in a window, with how
// many logs each one stands for (so counts scale back to all of them), and
// the volume per interval. Datadog allows ~3 log searches and 2 aggregations
// per 10s, so it reads at most 2 pages per window.
type logSample struct {
	logs   []datadog.LogData
	weight []float64 // per log: how many matching logs it stands for
	total  int
	volume []series.Point // logs per step, zeros included
	step   time.Duration
}

// volumeStep splits a window into ~60 buckets of whole minutes or hours.
func volumeStep(window time.Duration) time.Duration {
	d := time.Duration(math.Max(1, math.Round(window.Minutes()/60))) * time.Minute
	if d >= time.Hour {
		d = time.Duration(math.Round(d.Hours())) * time.Hour
	}
	return d
}

func sampleLogs(query string, from, to time.Time, n int) (*logSample, error) {
	s := &logSample{step: volumeStep(to.Sub(from))}
	fa, fb := from.UTC().Format(time.RFC3339), to.UTC().Format(time.RFC3339)
	pts, err := client.CountLogsSeries(query, fa, fb, fmtDuration(s.step))
	if err != nil {
		return nil, err
	}
	counts := map[int64]float64{}
	for _, p := range pts {
		s.total += int(p.Value)
		counts[p.Time.UnixMilli()] += p.Value
	}
	// The buckets' grid, anchored on one Datadog returned (empty ones aren't).
	anchor := from.Truncate(s.step)
	if len(pts) > 0 {
		anchor = pts[0].Time
		for anchor.After(from) {
			anchor = anchor.Add(-s.step)
		}
	}
	for t := anchor; t.Before(to); t = t.Add(s.step) {
		s.volume = append(s.volume, series.Point{T: t.UnixMilli(), V: counts[t.UnixMilli()]})
	}
	if s.total == 0 {
		return s, nil
	}
	read := func(a, b time.Time, total int) error {
		got, err := readLogs(query, a.UTC().Format(time.RFC3339), b.UTC().Format(time.RFC3339), min(total, max(1, n/2)))
		if err != nil {
			return err
		}
		w := 1.0
		if len(got) > 0 && total > len(got) {
			w = float64(total) / float64(len(got))
		}
		for range got {
			s.weight = append(s.weight, w)
		}
		s.logs = append(s.logs, got...)
		return nil
	}
	if s.total <= n {
		// Everything fits: exact counts.
		got, err := readLogs(query, fa, fb, s.total)
		if err != nil {
			return nil, err
		}
		for range got {
			s.weight = append(s.weight, 1)
		}
		s.logs = got
		return s, nil
	}
	// Half from each half of the window, split on a bucket boundary so each
	// half's total is exact.
	mid := anchor
	middle := from.Add(to.Sub(from) / 2)
	for mid.Add(s.step).Before(middle) || mid.Add(s.step).Equal(middle) {
		mid = mid.Add(s.step)
	}
	if !mid.After(from) {
		mid = middle
	}
	first := 0
	for t, c := range counts {
		if t < mid.UnixMilli() {
			first += int(c)
		}
	}
	for _, h := range []struct {
		a, b  time.Time
		total int
	}{{from, mid, first}, {mid, to, s.total - first}} {
		if h.total > 0 {
			if err := read(h.a, h.b, h.total); err != nil {
				return nil, err
			}
		}
	}
	return s, nil
}

// readLogs reads up to n logs, newest first, a page of up to 1000 at a time.
func readLogs(query, from, to string, n int) ([]datadog.LogData, error) {
	var got []datadog.LogData
	cursor := ""
	for len(got) < n {
		resp, err := client.SearchLogsCursor(query, from, to, min(1000, n-len(got)), cursor)
		if err != nil {
			return nil, err
		}
		got = append(got, resp.Data...)
		cursor = resp.Meta.Page.After
		if cursor == "" || len(resp.Data) == 0 {
			break
		}
	}
	return got, nil
}

func logKey(l datadog.LogData) string {
	return l.Attributes.Service + "\x00" + l.Attributes.Status
}

func logMessage(l datadog.LogData) string {
	if m := strings.TrimSpace(l.Attributes.Message); m != "" {
		// A JSON line Datadog didn't parse: its message field is the message.
		if strings.HasPrefix(m, "{") {
			var obj map[string]interface{}
			if json.Unmarshal([]byte(m), &obj) == nil {
				for _, p := range [][]string{{"msg"}, {"message"}, {"error", "message"}, {"err", "message"}, {"event"}} {
					if v := strAt(obj, p...); v != "" {
						return v
					}
				}
			}
			// Not valid JSON (raw newlines, cut short): pino/bunyan write msg
			// last, at the top level.
			for _, re := range []*regexp.Regexp{reJSONMsg, reJSONMessage} {
				if all := re.FindAllStringSubmatch(m, -1); len(all) > 0 {
					var v string
					if json.Unmarshal([]byte(`"`+all[len(all)-1][1]+`"`), &v) == nil && v != "" {
						return v
					}
				}
			}
		}
		return m
	}
	for _, p := range [][]string{{"error", "message"}, {"msg"}, {"message"}, {"event"}} {
		if v := strAt(l.Attributes.Attributes, p...); v != "" {
			return v
		}
	}
	return "(empty message)"
}

var (
	reJSONMsg     = regexp.MustCompile(`"msg"\s*:\s*"((?:[^"\\]|\\.)*)"`)
	reJSONMessage = regexp.MustCompile(`"message"\s*:\s*"((?:[^"\\]|\\.)*)"`)
)

func logTime(l datadog.LogData) int64 {
	t, _ := time.Parse(time.RFC3339Nano, l.Attributes.Timestamp)
	return t.UnixMilli()
}

func logPatterns(query string, from, to time.Time, n int, compare string) (*patternsReport, error) {
	now, err := sampleLogs(query, from, to, n)
	if err != nil {
		return nil, err
	}
	rep := &patternsReport{Query: query, From: from, To: to, Total: now.total, Sampled: len(now.logs), VolumeStep: fmtDuration(now.step), volume: now.volume}
	if now.total > 0 {
		f := series.Formatter{Value: func(v float64) string { return fmtCount(int(math.Round(v))) }, Time: clockFor(from, to)}
		rep.Volume = series.Describe(now.volume).Text(f)
	}
	c := logpattern.NewClusterer()
	est := map[*logpattern.Group]float64{}
	for i, l := range now.logs {
		g := c.Add(logKey(l), logMessage(l), logTime(l))
		est[g] += now.weight[i]
	}
	groups := c.Groups()

	var before map[*logpattern.Group]float64
	var base *logSample
	if compare != "" {
		d, err := parseDuration(compare)
		if err != nil {
			return nil, fmt.Errorf("--compare: %w", err)
		}
		base, err = sampleLogs(query, from.Add(-d), to.Add(-d), n)
		if err != nil {
			return nil, err
		}
		rep.Compare, rep.TotalBefore = compare, base.total
		before = map[*logpattern.Group]float64{}
		for i, l := range base.logs {
			if g := c.Match(logKey(l), logMessage(l)); g != nil {
				before[g] += base.weight[i]
			}
		}
	}
	for _, g := range groups {
		service, status, _ := strings.Cut(g.Key, "\x00")
		p := logPattern{
			Pattern: g.Template, Service: service, Status: status,
			Estimate: int(math.Round(est[g])), Sampled: g.Count,
			First: time.UnixMilli(g.First), Last: time.UnixMilli(g.Last),
			Example: oneLine(g.Example, 300),
		}
		if rep.Total > 0 {
			p.Share = est[g] / float64(rep.Total) * 100
		}
		if before != nil {
			b := int(math.Round(before[g]))
			p.Before = &b
			// Call it new only when it would have shown up in the earlier
			// sample at its current rate.
			if b == 0 && base.total > 0 {
				expected := est[g] * float64(len(base.logs)) / float64(max(1, rep.Total))
				p.New = expected >= 3 || len(base.logs) >= base.total
			}
		}
		rep.Patterns = append(rep.Patterns, p)
	}
	sort.SliceStable(rep.Patterns, func(i, j int) bool { return rep.Patterns[i].Estimate > rep.Patterns[j].Estimate })
	return rep, nil
}

func (p logPattern) vs() string {
	switch {
	case p.Before == nil:
		return ""
	case p.New:
		return "new"
	case *p.Before == 0:
		return "—"
	}
	return changeText(float64(*p.Before), float64(p.Estimate))
}

func (r *patternsReport) header() string {
	s := fmt.Sprintf("Log patterns · %s · %s · %s logs", r.Query, fmtWindow(r.From, r.To), fmtCount(r.Total))
	if r.Sampled < r.Total {
		s += fmt.Sprintf(" (sampled %s)", fmtCount(r.Sampled))
	}
	if r.Compare != "" {
		s += fmt.Sprintf(" · %s before: %s logs", r.Compare, fmtCount(r.TotalBefore))
	}
	return s
}

func fmtCount(n int) string {
	s := fmt.Sprintf("%d", n)
	for i := len(s) - 3; i > 0; i -= 3 {
		s = s[:i] + "," + s[i:]
	}
	return s
}

func (r *patternsReport) text(tty bool, width int) string {
	var b strings.Builder
	title := r.header()
	if tty {
		title = ui.Title.Render(title)
	}
	b.WriteString(title + "\n")
	if len(r.Patterns) == 0 {
		b.WriteString("  no logs match\n")
		return b.String()
	}
	if r.Volume != "" {
		spark := ""
		if tty && len(r.volume) > 1 {
			vals := make([]float64, len(r.volume))
			for i, p := range r.volume {
				vals[i] = p.V
			}
			spark = viz.Sparkline(vals, min(40, len(vals)), ui.Secondary) + "  "
		}
		fmt.Fprintf(&b, "  logs per %s: %s%s\n\n", r.VolumeStep, spark, r.Volume)
	}
	approx := "COUNT"
	if r.Sampled < r.Total {
		approx = "≈COUNT"
	}
	vsHead := ""
	if r.Compare != "" {
		vsHead = fmt.Sprintf("  %-6s", "VS "+strings.ToUpper(r.Compare))
	}
	svcW := 7
	for _, p := range r.Patterns {
		svcW = max(svcW, min(24, len(p.Service)))
	}
	fixed := 2 + 8 + 2 + 6 + 2 + svcW + 2 + 5 + 2 + 6 + len(vsHead) + 2
	patW := max(30, width-fixed)
	fmt.Fprintf(&b, "  %8s  %6s%s  %-*s  %-5s  %-6s  %s\n", approx, "SHARE", vsHead, svcW, "SERVICE", "LEVEL", "LAST", "PATTERN")
	for _, p := range r.Patterns {
		vs := ""
		if r.Compare != "" {
			vs = fmt.Sprintf("  %-6s", p.vs())
			if tty && p.New {
				vs = "  " + ui.ErrorStyle.Render(fmt.Sprintf("%-6s", "new"))
			}
		}
		level := truncRunes(p.Status, 5)
		if tty {
			level = lipgloss.NewStyle().Foreground(ui.LogStatusColor(p.Status)).Render(fmt.Sprintf("%-5s", level))
		} else {
			level = fmt.Sprintf("%-5s", level)
		}
		fmt.Fprintf(&b, "  %8s  %5.1f%%%s  %-*s  %s  %-6s  %s\n", fmtCount(p.Estimate), p.Share, vs, svcW, truncRunes(p.Service, svcW), level, p.Last.Local().Format("15:04"), truncRunes(p.Pattern, patW))
	}
	if r.More > 0 {
		fmt.Fprintf(&b, "  … %d more patterns (--top 0 shows all)\n", r.More)
	}
	return b.String()
}

func (r *patternsReport) markdown() string {
	var b strings.Builder
	b.WriteString("**" + r.header() + "**\n\n")
	if len(r.Patterns) == 0 {
		b.WriteString("No logs match.\n")
		return b.String()
	}
	if r.Volume != "" {
		fmt.Fprintf(&b, "Logs per %s: %s\n\n", r.VolumeStep, r.Volume)
	}
	vs := ""
	if r.Compare != "" {
		vs = " vs " + r.Compare + " |"
	}
	b.WriteString("| count | share |" + vs + " service | level | last | pattern | example |\n|---:|---:|" + strings.Repeat("---|", strings.Count(vs, "|")) + "---|---|---|---|---|\n")
	esc := func(s string) string { return strings.ReplaceAll(s, "|", `\|`) }
	for _, p := range r.Patterns {
		v := ""
		if r.Compare != "" {
			v = " " + p.vs() + " |"
		}
		fmt.Fprintf(&b, "| %s | %.1f%% |%s %s | %s | %s | `%s` | %s |\n", fmtCount(p.Estimate), p.Share, v, p.Service, p.Status, p.Last.Local().Format("15:04"), esc(truncRunes(p.Pattern, 160)), esc(truncRunes(p.Example, 160)))
	}
	if r.More > 0 {
		fmt.Fprintf(&b, "\n… %d more patterns.\n", r.More)
	}
	return b.String()
}

func init() {
	patWin.register(logsPatternsCmd, time.Hour)
	logsPatternsCmd.Flags().BoolVar(&patJSON, "json", false, "Output as JSON")
	logsPatternsCmd.Flags().BoolVar(&patMD, "md", false, "Output as markdown")
	logsPatternsCmd.Flags().IntVar(&patTop, "top", 20, "Patterns to show (0 = all)")
	logsPatternsCmd.Flags().IntVar(&patSample, "sample", 2000, "Logs to read (spread over the window)")
	logsPatternsCmd.Flags().StringVar(&patCompare, "compare", "", "Compare with the same window this long ago (1d, 7d) and mark new patterns")
	logsCmd.AddCommand(logsPatternsCmd)
}
