package cmd

import (
	"fmt"
	"math"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/ngavilan-dogfy/datadog-cli/datadog"
	"github.com/ngavilan-dogfy/datadog-cli/internal/series"
	"github.com/ngavilan-dogfy/datadog-cli/viz"

	"github.com/spf13/cobra"
)

var (
	describeWin     windowFlags
	describeJSON    bool
	describeMD      bool
	describeTop     int
	describeCompare string
)

type describedSeries struct {
	Scope   string          `json:"scope"`
	Text    string          `json:"text"`
	Summary series.Summary  `json:"summary"`
	Compare *comparedSeries `json:"compare,omitempty"`
	score   float64
}

type comparedSeries struct {
	Offset     string  `json:"offset"`
	MeanBefore float64 `json:"mean_before"`
	MeanNow    float64 `json:"mean_now"`
	Change     string  `json:"change"`
}

type describeResult struct {
	Query  string            `json:"query"`
	From   time.Time         `json:"from"`
	To     time.Time         `json:"to"`
	Unit   string            `json:"unit,omitempty"`
	Per    string            `json:"per,omitempty"` // what one point of a count covers (1m, 5m…)
	Series []describedSeries `json:"series"`
	More   int               `json:"more_series,omitempty"`
	Hint   string            `json:"hint,omitempty"` // why there's no data, when we can tell
}

var metricsDescribeCmd = &cobra.Command{
	Use:   "describe <query>",
	Short: "Describe what a metric did: levels, changes, peaks, gaps",
	Long: `Query a metric and describe each series in one line instead of printing
its points: the usual level, when it changed and by how much, peaks and
spikes, gaps in the data and where it ends. Made for reading a chart
without seeing it (people in a hurry, and AI agents).

Series with something notable come first; --top limits how many.
--compare puts each series next to the same window earlier (1d, 7d…).

Output:
  Terminal: one line per series · --md: markdown · --json: the numbers too

Examples:
  datadog metrics describe "p95:trace.http.request.duration{service:checkout}" --since 6h
  datadog metrics describe "sum:trace.http.request.errors{env:prod} by {service}.as_count()" --top 5
  datadog metrics describe "avg:system.cpu.user{*} by {host}" --since 1d --compare 7d
  datadog metrics describe "avg:postgresql.connections{*}" --from 2026-05-19T04:00:00Z --to 2026-05-19T06:00:00Z --json`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		from, to, err := describeWin.resolve()
		if err != nil {
			return err
		}
		res, err := describeMetric(args[0], from, to, describeCompare, describeTop)
		if err != nil {
			return err
		}
		switch {
		case describeJSON:
			return printJSON(res)
		case describeMD:
			fmt.Print(res.markdown())
			return nil
		}
		fmt.Print(res.text())
		return nil
	},
}

// describeMetric queries a metric over a window and describes each series.
func describeMetric(query string, from, to time.Time, compare string, top int) (*describeResult, error) {
	resp, err := client.QueryMetrics(query, from.Unix(), to.Unix())
	if err != nil {
		return nil, err
	}
	res := &describeResult{Query: query, From: from, To: to}
	var unit viz.Unit
	for _, s := range resp.Series {
		if len(s.Unit) > 0 && s.Unit[0].Name != "" {
			u := s.Unit[0]
			unit = viz.Unit{Family: u.Family, Name: u.Name, Short: u.ShortName, Scale: u.ScaleFactor}
			res.Unit = u.Name
			break
		}
	}
	f := series.Formatter{Value: func(v float64) string { return viz.FormatText(v, unit) }, Time: clockFor(from, to)}
	counts := reCountQuery.MatchString(query)

	var before map[string]float64
	if compare != "" {
		d, err := parseDuration(compare)
		if err != nil {
			return nil, fmt.Errorf("--compare: %w", err)
		}
		if prev, err := client.QueryMetrics(query, from.Add(-d).Unix(), to.Add(-d).Unix()); err == nil {
			before = map[string]float64{}
			for _, s := range prev.Series {
				before[seriesScope(s)] = series.Describe(seriesPoints(s, counts)).Mean
			}
		}
	}
	for _, s := range resp.Series {
		if counts && s.Interval > 0 && res.Per == "" {
			res.Per = fmtDuration(time.Duration(s.Interval) * time.Second)
		}
		sum := series.Describe(seriesPoints(s, counts))
		d := describedSeries{Scope: seriesScope(s), Summary: sum, Text: sum.Text(f)}
		d.score = notability(sum)
		if before != nil {
			if b, ok := before[d.Scope]; ok && !math.IsNaN(b) {
				d.Compare = &comparedSeries{Offset: compare, MeanBefore: b, MeanNow: sum.Mean, Change: changeText(b, sum.Mean)}
			}
		}
		res.Series = append(res.Series, d)
	}
	sort.SliceStable(res.Series, func(i, j int) bool {
		if res.Series[i].score != res.Series[j].score {
			return res.Series[i].score > res.Series[j].score
		}
		return res.Series[i].Summary.Mean > res.Series[j].Summary.Mean
	})
	if top > 0 && len(res.Series) > top {
		res.More = len(res.Series) - top
		res.Series = res.Series[:top]
	}
	if len(res.Series) == 0 {
		res.Hint = noDataHint(query)
	}
	return res, nil
}

// reCountQuery matches queries of counts, where no point means zero.
var reCountQuery = regexp.MustCompile(`as_count\(\)|\.rollup\(sum|^count:|\bcount\(`)

// seriesPoints are a series' points; for counts, the intervals Datadog
// leaves out (nothing happened) are zeros, not gaps.
func seriesPoints(s datadog.MetricsSeries, counts bool) []series.Point {
	pts := toPoints(s.Pointlist)
	if !counts || s.Interval <= 0 || len(pts) == 0 {
		return pts
	}
	step := int64(s.Interval) * 1000
	have := make(map[int64]float64, len(pts))
	for _, p := range pts {
		have[p.T] = p.V
	}
	start, end := s.Start, s.End
	if start <= 0 || start > pts[0].T {
		start = pts[0].T
	}
	if end < pts[len(pts)-1].T {
		end = pts[len(pts)-1].T
	}
	// Align on the points Datadog returned.
	start = pts[0].T - (pts[0].T-start)/step*step
	var out []series.Point
	for t := start; t <= end; t += step {
		v, ok := have[t]
		if !ok || math.IsNaN(v) {
			v = 0
		}
		out = append(out, series.Point{T: t, V: v})
	}
	return out
}

func toPoints(pl [][]float64) []series.Point {
	out := make([]series.Point, 0, len(pl))
	for _, p := range pl {
		if len(p) == 2 {
			out = append(out, series.Point{T: int64(p[0]), V: p[1]})
		}
	}
	return out
}

func seriesScope(s datadog.MetricsSeries) string {
	if s.Scope != "" && s.Scope != "*" {
		return s.Scope
	}
	if s.Expression != "" {
		return s.Expression
	}
	return s.Metric
}

// notability ranks series worth reading first: level changes, spikes,
// gaps, big trends.
func notability(s series.Summary) float64 {
	score := float64(len(s.Shifts))*3 + float64(len(s.Spikes))*2 + float64(len(s.Gaps))
	score += math.Min(3, math.Abs(s.Trend))
	for _, sh := range s.Shifts {
		if sh.From != 0 {
			score += math.Min(5, math.Abs(math.Log(math.Abs(sh.To/sh.From))))
		}
	}
	return score
}

func changeText(before, now float64) string {
	switch {
	case before == 0 && now == 0:
		return "no change"
	case before == 0:
		return "was 0"
	}
	r := now / before
	switch {
	case r >= 2:
		return fmt.Sprintf("×%.3g", r)
	case r > 0 && r <= 0.5:
		return fmt.Sprintf("÷%.3g", 1/r)
	}
	return fmt.Sprintf("%+.0f%%", (r-1)*100)
}

func (r *describeResult) header() string {
	h := r.Query + " · " + fmtWindow(r.From, r.To)
	if r.Per != "" {
		h += " · counts per " + r.Per
	}
	return h
}

func (r *describeResult) line(d describedSeries) string {
	s := d.Scope + ": " + d.Text
	if d.Compare != nil {
		s += fmt.Sprintf(" · vs %s before: %s", d.Compare.Offset, d.Compare.Change)
	}
	return s
}

func (r *describeResult) text() string {
	var b strings.Builder
	b.WriteString(r.header() + "\n")
	if len(r.Series) == 0 {
		b.WriteString("  no data\n")
		for _, h := range strings.Split(r.Hint, "\n") {
			if h != "" {
				b.WriteString("  → " + h + "\n")
			}
		}
	}
	for _, d := range r.Series {
		b.WriteString("  " + r.line(d) + "\n")
	}
	if r.More > 0 {
		fmt.Fprintf(&b, "  … and %d more series (--top to see them)\n", r.More)
	}
	return b.String()
}

func (r *describeResult) markdown() string {
	var b strings.Builder
	per := ""
	if r.Per != "" {
		per = ", counts per " + r.Per
	}
	fmt.Fprintf(&b, "**%s** (%s%s)\n\n", r.Query, fmtWindow(r.From, r.To), per)
	if len(r.Series) == 0 {
		b.WriteString("- no data\n")
		for _, h := range strings.Split(r.Hint, "\n") {
			if h != "" {
				b.WriteString("- → " + h + "\n")
			}
		}
	}
	for _, d := range r.Series {
		b.WriteString("- `" + d.Scope + "`: " + strings.TrimPrefix(r.line(d), d.Scope+": ") + "\n")
	}
	if r.More > 0 {
		fmt.Fprintf(&b, "- … and %d more series\n", r.More)
	}
	return b.String()
}

func init() {
	describeWin.register(metricsDescribeCmd, 4*time.Hour)
	metricsDescribeCmd.Flags().BoolVar(&describeJSON, "json", false, "Output as JSON (the summaries and their numbers)")
	metricsDescribeCmd.Flags().BoolVar(&describeMD, "md", false, "Output as markdown")
	metricsDescribeCmd.Flags().IntVar(&describeTop, "top", 10, "Most notable series to show (0 = all)")
	metricsDescribeCmd.Flags().StringVar(&describeCompare, "compare", "", "Also compare with the same window this long ago (1d, 7d)")
	metricsCmd.AddCommand(metricsDescribeCmd)
}
