package tui

import (
	"fmt"
	"math"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/ngavilan-dogfy/datadog-cli/datadog"
	"github.com/ngavilan-dogfy/datadog-cli/viz"
)

// widgetData is what a widget shows, fetched for one time range and one
// set of template variable values.
type widgetData struct {
	key     string // time range + variables it belongs to
	at      time.Time
	err     error
	series  []viz.Series
	unit    viz.Unit
	kind    viz.Kind
	rows    []scalarRow // query_value (one row), toplist, change, hostmap
	logs    []datadog.LogData
	mons    []datadog.MonitorSearchHit
	monSum  map[string]int
	spark   []float64 // query_value background
	partial string    // non-fatal problem (one request of several failed)
	cols    int       // the width it was fetched for
}

// tooCoarse reports data fetched for far fewer columns than it's drawn in
// now (asked before the terminal size was known, say).
func tooCoarse(fetchedCols, wantCols int) bool { return fetchedCols*4 < wantCols*3 }

type scalarRow struct {
	label string
	value float64
	prev  float64 // change widgets: the compared period
	unit  viz.Unit
}

// timeRange is the window every widget queries.
type timeRange struct {
	span time.Duration
	end  time.Time // zero = live (now)
}

func (r timeRange) bounds() (from, to int64) {
	end := r.end
	if end.IsZero() {
		end = now()
	}
	return end.Add(-r.span).UnixMilli(), end.UnixMilli()
}

func (r timeRange) label() string {
	if r.end.IsZero() {
		return "Past " + viz.FormatSpan(r.span)
	}
	return r.end.Add(-r.span).Local().Format("Jan 2 15:04") + " – " + r.end.Local().Format("15:04")
}

var ranges = []time.Duration{15 * time.Minute, time.Hour, 4 * time.Hour, 24 * time.Hour, 2 * 24 * time.Hour, 7 * 24 * time.Hour, 30 * 24 * time.Hour}

// niceIntervals are the rollups asked of Datadog: one point per chart
// column, rounded so the API accepts it.
var niceIntervals = []int64{10, 20, 30, 60, 120, 300, 600, 900, 1800, 3600, 7200, 14400, 21600, 43200, 86400}

func intervalFor(from, to int64, cols int) int64 {
	want := (to - from) / 1000 / int64(max(1, cols))
	for _, s := range niceIntervals {
		if s >= want {
			return s * 1000
		}
	}
	return niceIntervals[len(niceIntervals)-1] * 1000
}

// requestSpec is one widget request, ready for the query API.
type requestSpec struct {
	queries  []map[string]any
	formulas []map[string]any
	display  string // line, area, bars
	palette  string
	aggr     string // scalar aggregator for legacy queries
	conds    []condFormat
}

type condFormat struct {
	comparator string
	value      float64
	palette    string
}

// requests reads a widget's requests (formula style or legacy "q"), with
// template variables applied.
func widgetRequests(def map[string]any, tvars []tvar) []requestSpec {
	var raw []any
	switch r := def["requests"].(type) {
	case []any:
		raw = r
	case map[string]any: // hostmap: {"fill": {...}, "size": {...}}
		for _, k := range []string{"fill", "size"} {
			if v, ok := r[k]; ok {
				raw = append(raw, v)
			}
		}
	}
	var out []requestSpec
	for _, rr := range raw {
		r := obj(applyTVars(rr, tvars))
		if r == nil {
			continue
		}
		spec := requestSpec{display: str(r["display_type"])}
		if st := obj(r["style"]); st != nil {
			spec.palette = str(st["palette"])
		}
		for _, cf := range list(r["conditional_formats"]) {
			c := obj(cf)
			spec.conds = append(spec.conds, condFormat{comparator: str(c["comparator"]), value: num(c["value"]), palette: str(c["palette"])})
		}
		if qs := list(r["queries"]); len(qs) > 0 {
			for _, q := range qs {
				spec.queries = append(spec.queries, obj(q))
			}
			for _, f := range list(r["formulas"]) {
				spec.formulas = append(spec.formulas, obj(f))
			}
		} else if q := str(r["q"]); q != "" {
			spec.aggr = str(r["aggregator"])
			spec.queries = []map[string]any{{"data_source": "metrics", "name": "q1", "query": q}}
			spec.formulas = []map[string]any{{"formula": "q1"}}
		}
		if len(spec.queries) > 0 {
			out = append(out, spec)
		}
	}
	return out
}

// seriesName names a timeseries line the way Datadog's legend does: the
// formula's alias, else its group tags, else the query itself.
func seriesName(spec requestSpec, s datadog.TimeseriesSeries) string {
	alias := ""
	formula := ""
	if s.QueryIndex < len(spec.formulas) {
		alias = str(spec.formulas[s.QueryIndex]["alias"])
		formula = str(spec.formulas[s.QueryIndex]["formula"])
	}
	tags := strings.Join(s.GroupTags, ",")
	if tags == "*" {
		tags = ""
	}
	switch {
	case tags != "" && alias != "" && len(spec.formulas) > 1:
		return alias + " " + tags
	case tags != "":
		return tags
	case alias != "":
		return alias
	}
	for _, q := range spec.queries {
		if str(q["name"]) == formula {
			if text := str(q["query"]); text != "" {
				return text
			}
			if search := obj(q["search"]); search != nil {
				return str(q["data_source"]) + ": " + str(search["query"])
			}
		}
	}
	if formula != "" {
		return formula
	}
	return "series"
}

func vizUnit(u *datadog.QueryUnit) viz.Unit {
	if u == nil {
		return viz.Unit{}
	}
	return viz.Unit{Family: u.Family, Name: u.Name, Short: u.ShortName, Scale: u.ScaleFactor}
}

func kindOf(display string) viz.Kind {
	switch display {
	case "area":
		return viz.Area
	case "bars":
		return viz.Bars
	}
	return viz.Line
}

// fetchTimeseries runs every request of a timeseries-like widget.
func fetchTimeseries(api API, def map[string]any, tvars []tvar, from, to int64, cols int) *widgetData {
	d := &widgetData{}
	specs := widgetRequests(def, tvars)
	if len(specs) == 0 {
		d.err = fmt.Errorf("no queries in this widget")
		return d
	}
	d.kind = kindOf(specs[0].display)
	pal := viz.Palette()
	var failures []string
	for _, spec := range specs {
		res, err := api.QueryTimeseries(datadog.FormulaRequest{From: from, To: to, Interval: intervalFor(from, to, cols), Queries: spec.queries, Formulas: spec.formulas})
		if err != nil {
			failures = append(failures, err.Error())
			continue
		}
		for _, s := range res.Series {
			pts := make([]viz.Point, 0, len(res.Times))
			for i, t := range res.Times {
				v := math.NaN()
				if i < len(s.Values) && s.Values[i] != nil {
					v = *s.Values[i]
				}
				pts = append(pts, viz.Point{T: t, V: v})
			}
			if d.unit == (viz.Unit{}) && s.Unit != nil {
				d.unit = vizUnit(s.Unit)
			}
			d.series = append(d.series, viz.Series{
				Name:   seriesName(spec, s),
				Color:  paletteColor(spec.palette, len(d.series), pal),
				Points: pts,
				Unit:   vizUnit(s.Unit),
			})
		}
	}
	if len(failures) > 0 {
		if len(d.series) == 0 {
			d.err = fmt.Errorf("%s", failures[0])
		} else {
			d.partial = failures[0]
		}
	}
	return d
}

// paletteColor maps Datadog's palettes onto the terminal's ANSI colors.
func paletteColor(palette string, i int, pal []lipglossColor) lipglossColor {
	switch palette {
	case "warm", "orange":
		return pickFrom(i, th.yellow, th.red, th.magenta)
	case "cool":
		return pickFrom(i, th.blue, th.cyan, th.green)
	case "purple":
		return pickFrom(i, th.magenta, th.blue)
	case "red":
		return th.red
	case "green":
		return th.green
	case "grey", "gray":
		return th.muted
	}
	return pal[i%len(pal)]
}

func pickFrom(i int, cs ...lipglossColor) lipglossColor { return cs[i%len(cs)] }

// fetchScalar runs a scalar request and returns its rows (group label +
// the first formula's value).
func fetchScalar(api API, spec requestSpec, from, to int64) ([]scalarRow, error) {
	queries := spec.queries
	if spec.aggr != "" {
		queries = []map[string]any{copyWith(queries[0], "aggregator", spec.aggr)}
	}
	for i, q := range queries {
		if _, ok := q["aggregator"]; !ok && str(q["data_source"]) == "metrics" {
			queries[i] = copyWith(q, "aggregator", "avg")
		}
	}
	res, err := api.QueryScalar(datadog.FormulaRequest{From: from, To: to, Queries: queries, Formulas: spec.formulas})
	if err != nil {
		return nil, err
	}
	var groups []datadog.ScalarColumn
	var numbers *datadog.ScalarColumn
	for i, c := range res.Columns {
		switch c.Type {
		case "group":
			groups = append(groups, c)
		default:
			if numbers == nil {
				numbers = &res.Columns[i]
			}
		}
	}
	if numbers == nil {
		return nil, nil
	}
	rows := make([]scalarRow, 0, len(numbers.Values))
	for r, v := range numbers.Values {
		if v == nil {
			continue
		}
		var labels []string
		for _, g := range groups {
			if r < len(g.Groups) {
				labels = append(labels, strings.Join(g.Groups[r], ","))
			}
		}
		rows = append(rows, scalarRow{label: strings.Join(labels, " · "), value: *v, unit: vizUnit(numbers.Unit)})
	}
	return rows, nil
}

func copyWith(m map[string]any, k string, v any) map[string]any {
	out := make(map[string]any, len(m)+1)
	for kk, vv := range m {
		out[kk] = vv
	}
	out[k] = v
	return out
}

var compareShift = map[string]time.Duration{
	"hour_before": time.Hour, "day_before": 24 * time.Hour, "week_before": 7 * 24 * time.Hour, "month_before": 30 * 24 * time.Hour,
}

// fetchWidget gets whatever data a widget type needs.
func fetchWidget(api API, w *widget, tvars []tvar, tr timeRange, cols, rows int) *widgetData {
	from, to := tr.bounds()
	switch w.Type {
	case "timeseries":
		return fetchTimeseries(api, w.Def, tvars, from, to, cols*2)
	case "query_value":
		d := &widgetData{}
		specs := widgetRequests(w.Def, tvars)
		if len(specs) == 0 {
			d.err = fmt.Errorf("no queries in this widget")
			return d
		}
		rs, err := fetchScalar(api, specs[0], from, to)
		d.err, d.rows = err, rs
		if bg := obj(w.Def["timeseries_background"]); bg != nil && err == nil {
			if ts := fetchTimeseries(api, map[string]any{"requests": []any{toAny(specs[0])}}, nil, from, to, cols); ts.err == nil && len(ts.series) > 0 {
				for _, p := range ts.series[0].Points {
					d.spark = append(d.spark, p.V)
				}
			}
		}
		return d
	case "toplist", "hostmap", "sunburst", "treemap", "geomap", "query_table":
		d := &widgetData{}
		specs := widgetRequests(w.Def, tvars)
		if len(specs) == 0 {
			d.err = fmt.Errorf("no queries in this widget")
			return d
		}
		d.rows, d.err = fetchScalar(api, specs[0], from, to)
		sort.SliceStable(d.rows, func(i, j int) bool { return d.rows[i].value > d.rows[j].value })
		return d
	case "change":
		d := &widgetData{}
		specs := widgetRequests(w.Def, tvars)
		if len(specs) == 0 {
			d.err = fmt.Errorf("no queries in this widget")
			return d
		}
		cur, err := fetchScalar(api, specs[0], from, to)
		if err != nil {
			d.err = err
			return d
		}
		shift := 24 * time.Hour
		if reqs := list(w.Def["requests"]); len(reqs) > 0 {
			if s, ok := compareShift[str(obj(reqs[0])["compare_to"])]; ok {
				shift = s
			}
		}
		prev, err := fetchScalar(api, specs[0], from-shift.Milliseconds(), to-shift.Milliseconds())
		byLabel := map[string]float64{}
		if err == nil {
			for _, r := range prev {
				byLabel[r.label] = r.value
			}
		}
		for i := range cur {
			cur[i].prev = math.NaN()
			if v, ok := byLabel[cur[i].label]; ok {
				cur[i].prev = v
			}
		}
		sort.SliceStable(cur, func(i, j int) bool { return cur[i].value > cur[j].value })
		d.rows = cur
		return d
	case "list_stream", "log_stream":
		d := &widgetData{}
		q := "*"
		if reqs := list(w.Def["requests"]); len(reqs) > 0 {
			r := obj(applyTVars(reqs[0], tvars))
			if qq := obj(r["query"]); qq != nil {
				if s := str(qq["query_string"]); s != "" {
					q = s
				}
				if ds := str(qq["data_source"]); ds != "" && ds != "logs_stream" && ds != "logs" {
					d.err = fmt.Errorf("%s streams aren't supported yet — o opens it in Datadog", ds)
					return d
				}
			}
		} else if s := str(w.Def["query"]); s != "" {
			q = substitute(s, tvars)
		}
		resp, err := api.SearchLogs(q, strconv.FormatInt(from, 10), strconv.FormatInt(to, 10), max(10, rows))
		if err != nil {
			d.err = err
			return d
		}
		d.logs = resp.Data
		return d
	case "manage_status", "monitor_summary":
		d := &widgetData{}
		q := substitute(str(w.Def["query"]), tvars)
		res, err := api.SearchMonitorsRich(q, 100)
		if err != nil {
			d.err = err
			return d
		}
		d.mons = res.Monitors
		d.monSum = map[string]int{}
		for _, m := range res.Monitors {
			d.monSum[monitorState(m.Status)]++
		}
		return d
	}
	return nil // notes, groups, images… need no data
}

func toAny(spec requestSpec) map[string]any {
	qs := make([]any, len(spec.queries))
	for i, q := range spec.queries {
		qs[i] = q
	}
	fs := make([]any, len(spec.formulas))
	for i, f := range spec.formulas {
		fs[i] = f
	}
	return map[string]any{"queries": qs, "formulas": fs}
}

// ─── markers & axis ──────────────────────────────────────────────

var reNumber = regexp.MustCompile(`-?\d+(\.\d+)?`)

// widgetMarkers reads "y = 90"-style markers (the first number of ranges).
func widgetMarkers(def map[string]any) []viz.Marker {
	var out []viz.Marker
	for _, m := range list(def["markers"]) {
		mm := obj(m)
		n := reNumber.FindString(str(mm["value"]))
		v, err := strconv.ParseFloat(n, 64)
		if err != nil {
			continue
		}
		out = append(out, viz.Marker{Value: v, Color: markerColor(str(mm["display_type"])), Label: str(mm["label"])})
	}
	return out
}

func markerColor(display string) lipglossColor {
	switch {
	case strings.HasPrefix(display, "error"):
		return th.red
	case strings.HasPrefix(display, "warning"):
		return th.yellow
	case strings.HasPrefix(display, "ok"):
		return th.green
	case strings.HasPrefix(display, "info"):
		return th.blue
	}
	return th.muted
}

// axisBounds reads yaxis.min/max ("auto" means none).
func axisBounds(def map[string]any) (*float64, *float64, bool) {
	y := obj(def["yaxis"])
	if y == nil {
		return nil, nil, false
	}
	parse := func(k string) *float64 {
		s := str(y[k])
		if s == "" || s == "auto" {
			return nil
		}
		v, err := strconv.ParseFloat(s, 64)
		if err != nil {
			return nil
		}
		return &v
	}
	loose := str(y["include_zero"]) == "false"
	return parse("min"), parse("max"), loose
}
