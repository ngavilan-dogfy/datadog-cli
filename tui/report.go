package tui

import (
	"fmt"
	"math"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/ngavilan-dogfy/datadog-cli/internal/logpattern"
	"github.com/ngavilan-dogfy/datadog-cli/internal/series"
	"github.com/ngavilan-dogfy/datadog-cli/viz"
)

// DashboardReport is a dashboard read the way a person looks at it: what
// every widget shows over a window, in words and numbers, and what's wrong
// with it. For `datadog dashboards read` — and for agents, who can't see
// charts.
type DashboardReport struct {
	ID          string         `json:"id,omitempty"`
	Title       string         `json:"title"`
	Description string         `json:"description,omitempty"`
	URL         string         `json:"url,omitempty"`
	From        time.Time      `json:"from"`
	To          time.Time      `json:"to"`
	Variables   []ReportVar    `json:"template_variables,omitempty"`
	Widgets     []WidgetReport `json:"widgets"`
	Problems    []string       `json:"problems,omitempty"` // about the dashboard as a whole
}

// ReportVar is a template variable and the value the report used.
type ReportVar struct {
	Name      string   `json:"name"`
	Tag       string   `json:"tag,omitempty"`
	Value     string   `json:"value"`
	Available []string `json:"available,omitempty"`
}

// WidgetReport is one widget: what it queries and what that showed.
type WidgetReport struct {
	Path       string          `json:"path"` // in the dashboard JSON: widgets[2].definition.widgets[0]
	Group      string          `json:"group,omitempty"`
	Type       string          `json:"type"`
	Title      string          `json:"title"`
	Queries    []string        `json:"queries,omitempty"`
	Unit       string          `json:"unit,omitempty"`
	Series     []SeriesReport  `json:"series,omitempty"`
	MoreSeries int             `json:"more_series,omitempty"`
	Values     []ValueReport   `json:"values,omitempty"`
	MoreValues int             `json:"more_values,omitempty"`
	Monitors   *MonitorsReport `json:"monitors,omitempty"`
	LogsRead   int             `json:"logs_read,omitempty"`
	Patterns   []PatternReport `json:"log_patterns,omitempty"`
	Text       string          `json:"text,omitempty"` // notes
	Error      string          `json:"error,omitempty"`
	Partial    string          `json:"partial,omitempty"`
	Skipped    string          `json:"skipped,omitempty"` // why it wasn't read
	Problems   []string        `json:"problems,omitempty"`
	Hints      []string        `json:"hints,omitempty"` // could be better
}

// SeriesReport is one line of a chart, described.
type SeriesReport struct {
	Name    string         `json:"name"`
	Text    string         `json:"text"`
	Summary series.Summary `json:"summary"`
}

// ValueReport is a number a widget shows: a query value, a toplist row, a
// change.
type ValueReport struct {
	Label  string   `json:"label,omitempty"`
	Value  float64  `json:"value"`
	Text   string   `json:"text"` // formatted, with its unit
	Before *float64 `json:"before,omitempty"`
	Change string   `json:"change,omitempty"`
	State  string   `json:"state,omitempty"` // from conditional formats: good, warning, bad
}

// MonitorsReport sums up a monitor summary widget.
type MonitorsReport struct {
	Counts   map[string]int `json:"counts"`
	Alerting []string       `json:"alerting,omitempty"` // "Alert: name"
}

// PatternReport is a log pattern in a log stream widget.
type PatternReport struct {
	Pattern string `json:"pattern"`
	Count   int    `json:"count"`
	Status  string `json:"status,omitempty"`
}

// ReadOptions says what to read a dashboard over.
type ReadOptions struct {
	Span   time.Duration
	End    time.Time         // zero = now
	Vars   map[string]string // template variable values by name (without $)
	Points int               // points per chart line (default 120)
	Top    int               // lines / rows kept per widget (default 8)
}

// readable widget types; the rest are reported as skipped.
var readTypes = map[string]bool{
	"timeseries": true, "query_value": true, "toplist": true, "hostmap": true, "sunburst": true, "treemap": true,
	"geomap": true, "query_table": true, "change": true, "list_stream": true, "log_stream": true,
	"manage_status": true, "monitor_summary": true, "heatmap": true,
}

// ReadDashboard fetches what every widget of a dashboard (as Datadog's API
// returns it, or a JSON being written) shows, a few requests at a time.
func ReadDashboard(api API, raw map[string]any, opts ReadOptions) *DashboardReport {
	if opts.Span <= 0 {
		opts.Span = 4 * time.Hour
	}
	if opts.Points <= 0 {
		opts.Points = 120
	}
	if opts.Top <= 0 {
		opts.Top = 8
	}
	d := parseDashboard(raw)
	for i := range d.TVars {
		if v, ok := opts.Vars[d.TVars[i].Name]; ok {
			d.TVars[i].Value = v
		}
	}
	tr := timeRange{span: opts.Span, end: opts.End}
	fromMs, toMs := tr.bounds()
	rep := &DashboardReport{ID: d.ID, Title: d.Title, Description: d.Description, From: time.UnixMilli(fromMs), To: time.UnixMilli(toMs)}
	if d.ID != "" {
		rep.URL = api.DashboardURL(d.ID)
	}
	for _, t := range d.TVars {
		rep.Variables = append(rep.Variables, ReportVar{Name: t.Name, Tag: t.Prefix, Value: t.Value, Available: t.Available})
	}

	type item struct {
		w     *widget
		path  string
		group string
	}
	var items []item
	var walk func(ws []*widget, path, group string)
	walk = func(ws []*widget, path, group string) {
		for i, w := range ws {
			p := fmt.Sprintf("%s[%d]", path, i)
			items = append(items, item{w, p, group})
			if w.Type == "group" {
				walk(w.Children, p+".definition.widgets", w.Title)
			}
		}
	}
	walk(d.Widgets, "widgets", "")

	clock := func(ms int64) string { return time.UnixMilli(ms).Local().Format("15:04") }
	if opts.Span > 20*time.Hour {
		clock = func(ms int64) string { return time.UnixMilli(ms).Local().Format("Jan 2 15:04") }
	}
	out := make([]WidgetReport, len(items))
	var wg sync.WaitGroup
	for i, it := range items {
		out[i] = WidgetReport{Path: it.path, Group: it.group, Type: it.w.Type, Title: it.w.Title}
		wr := &out[i]
		switch {
		case it.w.Type == "group":
			continue
		case it.w.Type == "note" || it.w.Type == "free_text":
			text := str(it.w.Def["content"])
			if text == "" {
				text = str(it.w.Def["text"])
			}
			wr.Text = trunc(strings.Join(strings.Fields(text), " "), 400)
			continue
		case !readTypes[it.w.Type]:
			wr.Skipped = it.w.Type + " widgets aren't read yet"
			continue
		}
		for _, spec := range widgetRequests(it.w.Def, d.TVars) {
			wr.Queries = append(wr.Queries, specText(spec)...)
		}
		wg.Add(1)
		go func(w *widget) {
			defer wg.Done()
			data := limited(func() *widgetData {
				if w.Type == "heatmap" {
					return fetchTimeseries(api, w.Def, d.TVars, fromMs, toMs, opts.Points)
				}
				return fetchWidget(api, w, d.TVars, tr, opts.Points/2, 50)
			})
			fillReport(wr, w, data, opts, clock, toMs)
		}(it.w)
	}
	wg.Wait()
	rep.Widgets = out
	rep.Problems = dashboardProblems(d, rep)
	return rep
}

// specText is what a request queries, as text: its queries, and its formula
// when it's more than one query.
func specText(spec requestSpec) []string {
	var out []string
	for _, q := range spec.queries {
		text := str(q["query"])
		if text == "" {
			if s := obj(q["search"]); s != nil {
				text = str(q["data_source"]) + " search " + str(s["query"])
			} else if ds := str(q["data_source"]); ds != "" {
				text = ds + " query"
			}
		}
		if len(spec.queries) > 1 {
			text = str(q["name"]) + " = " + text
		}
		out = append(out, text)
	}
	for _, f := range spec.formulas {
		if formula := str(f["formula"]); formula != "" && len(spec.queries) > 1 && !reBareName.MatchString(formula) {
			out = append(out, "formula: "+formula)
		}
	}
	return out
}

var (
	reBareName  = regexp.MustCompile(`^\s*\w+\s*$`)
	reCountLike = regexp.MustCompile(`as_count\(\)|\.rollup\(sum|^count:|\bcount\(`)
)

func fillReport(wr *WidgetReport, w *widget, data *widgetData, opts ReadOptions, clock func(int64) string, toMs int64) {
	if data == nil {
		wr.Skipped = w.Type + " widgets aren't read yet"
		return
	}
	if data.err != nil {
		wr.Error = data.err.Error()
		wr.Problems = append(wr.Problems, "query failed: "+firstLine(wr.Error))
		return
	}
	wr.Partial = data.partial
	wr.Unit = data.unit.Name
	format := viz.FormatText
	f := series.Formatter{Value: func(v float64) string { return format(v, data.unit) }, Time: clock}

	switch {
	case len(data.series) > 0 || w.Type == "timeseries" || w.Type == "heatmap":
		type described struct {
			rep  SeriesReport
			mean float64
		}
		var all []described
		flatZero := 0
		counts := false
		for _, q := range wr.Queries {
			counts = counts || reCountLike.MatchString(q)
		}
		for _, s := range data.series {
			pts := make([]series.Point, len(s.Points))
			for i, p := range s.Points {
				pts[i] = series.Point{T: p.T, V: p.V}
			}
			sf := f
			if s.Unit != (viz.Unit{}) {
				u := s.Unit
				sf.Value = func(v float64) string { return format(v, u) }
			}
			// A count's last bucket is still filling up: it isn't a drop.
			ongoing := ""
			if counts && len(pts) > 2 {
				last, step := pts[len(pts)-1], pts[len(pts)-1].T-pts[len(pts)-2].T
				if step > 0 && last.T+step > toMs+1000 && !math.IsNaN(last.V) {
					ongoing = "; ongoing bucket so far " + sf.Value(last.V)
					pts = pts[:len(pts)-1]
				}
			}
			sum := series.Describe(pts)
			if sum.Points == 0 {
				continue
			}
			if sum.Flat && sum.Max == 0 {
				flatZero++
			}
			all = append(all, described{SeriesReport{Name: s.Name, Text: sum.Text(sf) + ongoing, Summary: sum}, sum.Mean})
		}
		switch {
		case len(all) == 0:
			wr.Problems = append(wr.Problems, "no data in the window")
		case flatZero == len(all):
			wr.Problems = append(wr.Problems, "always 0 in the window")
		}
		if len(all) > 20 {
			wr.Problems = append(wr.Problems, fmt.Sprintf("%d lines in one chart: hard to read — keep the top ones (top()), group by less, or split it", len(all)))
		}
		if len(all) > opts.Top {
			sort.SliceStable(all, func(i, j int) bool { return all[i].mean > all[j].mean })
			wr.MoreSeries = len(all) - opts.Top
			all = all[:opts.Top]
		}
		for _, a := range all {
			wr.Series = append(wr.Series, a.rep)
		}
	case data.mons != nil || w.Type == "manage_status" || w.Type == "monitor_summary":
		wr.Monitors = &MonitorsReport{Counts: data.monSum}
		for _, m := range data.mons {
			if st := monitorState(m.Status); st == "Alert" || st == "Warn" || st == "No Data" {
				wr.Monitors.Alerting = append(wr.Monitors.Alerting, st+": "+m.Name)
			}
		}
		sort.Strings(wr.Monitors.Alerting)
		if len(data.mons) == 0 {
			wr.Problems = append(wr.Problems, "no monitors match its query")
		}
	case w.Type == "list_stream" || w.Type == "log_stream":
		wr.LogsRead = len(data.logs)
		c := logpattern.NewClusterer()
		for _, l := range data.logs {
			t, _ := time.Parse(time.RFC3339Nano, l.Attributes.Timestamp)
			c.Add(l.Attributes.Status, l.Attributes.Message, t.UnixMilli())
		}
		for i, g := range c.Groups() {
			if i == 5 {
				break
			}
			wr.Patterns = append(wr.Patterns, PatternReport{Pattern: trunc(g.Template, 200), Count: g.Count, Status: g.Key})
		}
		if len(data.logs) == 0 {
			wr.Problems = append(wr.Problems, "no logs in the window")
		}
	default:
		specs := widgetRequests(w.Def, nil)
		var conds []condFormat
		if len(specs) > 0 {
			conds = specs[0].conds
		}
		for i, r := range data.rows {
			if i == opts.Top {
				wr.MoreValues = len(data.rows) - opts.Top
				break
			}
			u := r.unit
			if u == (viz.Unit{}) {
				u = data.unit
			}
			v := ValueReport{Label: r.label, Value: r.value, Text: format(r.value, u), State: condState(conds, r.value)}
			if w.Type == "query_value" {
				// As the widget shows it: its precision and custom unit.
				num, unit := queryValueText(w.Def, r.value, u)
				v.Text = strings.TrimSpace(num + " " + unit)
			}
			if w.Type == "change" && !math.IsNaN(r.prev) {
				prev := r.prev
				v.Before = &prev
				v.Change = changeWords(prev, r.value)
			}
			wr.Values = append(wr.Values, v)
		}
		if len(data.rows) == 0 {
			wr.Problems = append(wr.Problems, "no data in the window")
		}
	}
	if wr.Title == "" && w.Type != "note" {
		wr.Problems = append(wr.Problems, "untitled: say what it shows")
	}
}

// condState says which conditional format a value falls in: good, warning
// or bad, by the palette Datadog would paint it with.
func condState(conds []condFormat, v float64) string {
	for _, c := range conds {
		hit := false
		switch c.comparator {
		case ">":
			hit = v > c.value
		case ">=":
			hit = v >= c.value
		case "<":
			hit = v < c.value
		case "<=":
			hit = v <= c.value
		}
		if !hit {
			continue
		}
		switch p := c.palette; {
		case strings.Contains(p, "red"):
			return "bad"
		case strings.Contains(p, "yellow"):
			return "warning"
		case strings.Contains(p, "green"):
			return "good"
		}
		return c.palette
	}
	return ""
}

func changeWords(before, now float64) string {
	switch {
	case before == 0 && now == 0:
		return "no change"
	case before == 0:
		return "was 0"
	}
	return fmt.Sprintf("%+.0f%%", (now/before-1)*100)
}

// dashboardProblems looks at the dashboard as a whole.
func dashboardProblems(d *dashboard, rep *DashboardReport) []string {
	var out []string
	noData, failed, read := 0, 0, 0
	byQuery := map[string][]string{}
	for _, w := range rep.Widgets {
		if len(w.Queries) == 0 {
			continue
		}
		read++
		if w.Error != "" {
			failed++
		}
		for _, p := range w.Problems {
			if strings.HasPrefix(p, "no data") || strings.HasPrefix(p, "no logs") {
				noData++
			}
		}
		key := strings.Join(w.Queries, "\n")
		byQuery[key] = append(byQuery[key], w.Title)
	}
	if failed > 0 {
		out = append(out, fmt.Sprintf("%d of %d widgets fail to load", failed, read))
	}
	if noData > 0 {
		out = append(out, fmt.Sprintf("%d of %d widgets show no data", noData, read))
	}
	var dups []string
	for _, titles := range byQuery {
		if len(titles) > 1 {
			dups = append(dups, strings.Join(titles, " = "))
		}
	}
	sort.Strings(dups)
	for _, d := range dups {
		out = append(out, "widgets with the same queries: "+d)
	}
	// Filters every widget repeats are what template variables are for.
	if read >= 3 {
		for _, key := range []string{"env", "service"} {
			hasVar := false
			for _, t := range d.TVars {
				if t.Prefix == key {
					hasVar = true
				}
			}
			if hasVar {
				continue
			}
			counts := map[string]int{}
			for _, w := range rep.Widgets {
				seen := map[string]bool{}
				for _, q := range w.Queries {
					for _, m := range reScopeTag(key).FindAllStringSubmatch(q, -1) {
						if !seen[m[1]] {
							seen[m[1]] = true
							counts[m[1]]++
						}
					}
				}
			}
			for v, n := range counts {
				if n*2 >= read && n >= 3 {
					out = append(out, fmt.Sprintf("%d widgets hardcode %s:%s — a $%s template variable would let viewers switch", n, key, v, key))
				}
			}
		}
	}
	return out
}

var scopeTagRes = map[string]*regexp.Regexp{}

// reScopeTag matches key:value inside metric scopes and search queries.
func reScopeTag(key string) *regexp.Regexp {
	if re, ok := scopeTagRes[key]; ok {
		return re
	}
	re := regexp.MustCompile(`(?:^|[{,\s(])` + regexp.QuoteMeta(key) + `:([\w.\-/]+)`)
	scopeTagRes[key] = re
	return re
}
