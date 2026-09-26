package tui

import (
	"fmt"
	"math"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/ngavilan-dogfy/datadog-cli/viz"

	"github.com/charmbracelet/lipgloss"
)

type renderOpts struct {
	focused bool
	style   viz.Style
	spin    int
	zoom    bool
	cursor  int // crosshair column when zoomed, -1 for none
}

// renderWidget draws a widget into exactly width × height cells.
func renderWidget(w *widget, d *widgetData, loading bool, width, height int, o renderOpts) []string {
	if width < 1 || height < 1 {
		return nil
	}
	if w.Type == "group" {
		return []string{groupTitle(w, width, o.focused)}
	}
	if (w.Type == "note" || w.Type == "free_text") && w.Title == "" {
		// Notes have no title in Datadog: all the room goes to the text, with
		// the focus bar down the side.
		bar := sFaint().Render(" ")
		if o.focused {
			bar = sAccent().Render(gBar)
		}
		out := make([]string, height)
		content := noteLines(str(w.Def["content"])+str(w.Def["text"]), width-1, height)
		for i := range out {
			line := ""
			if i < len(content) {
				line = content[i]
			}
			out[i] = bar + fit(line, width-1)
		}
		return out
	}
	lines := []string{widgetTitle(w, d, loading, width, o)}
	body := height - 1
	if body <= 0 {
		return lines
	}
	var content []string
	switch {
	case d == nil && loading:
		content = centered(sMuted().Render(spinnerFrames[o.spin%len(spinnerFrames)]+" loading"), width, body)
	case d != nil && d.err != nil:
		content = errorBox(d.err.Error(), width, body)
	default:
		content = widgetBody(w, d, width, body, o)
	}
	for i := 0; i < body; i++ {
		line := ""
		if i < len(content) {
			line = content[i]
		}
		lines = append(lines, fit(line, width))
	}
	return lines
}

func widgetTitle(w *widget, d *widgetData, loading bool, width int, o renderOpts) string {
	bar := sFaint().Render(gBar)
	title := w.Title
	if title == "" {
		title = strings.ReplaceAll(w.Type, "_", " ")
	}
	st := lipgloss.NewStyle()
	if o.focused {
		bar = sAccent().Render(gBar)
		st = st.Bold(true).Foreground(th.accent)
	}
	right := ""
	switch {
	case loading:
		right = sMuted().Render(spinnerFrames[o.spin%len(spinnerFrames)])
	case d != nil && d.err != nil:
		right = fg(th.red).Render("!")
	case d != nil && d.partial != "":
		right = fg(th.yellow).Render("!")
	}
	if right != "" {
		right = " " + right
	}
	return spread(bar+" "+st.Render(trunc(title, max(1, width-2-sw(right)))), right, width)
}

func groupTitle(w *widget, width int, focused bool) string {
	arrow := gOpen
	extra := ""
	if w.collapsed {
		arrow = gClosed
		extra = sMuted().Render(fmt.Sprintf("  %d widgets", countWidgets(w.Children)))
	}
	st := lipgloss.NewStyle().Bold(true)
	if focused {
		st = st.Foreground(th.accent)
	}
	title := w.Title
	if title == "" {
		title = "Group"
	}
	line := " " + fg(th.accent).Render(arrow) + " " + st.Render(trunc(title, max(1, width-6-sw(extra)))) + extra
	return paintBg(fit(line, width), th.surface)
}

func countWidgets(ws []*widget) int {
	n := 0
	for _, w := range ws {
		n++
		if w.Type == "group" {
			n += countWidgets(w.Children)
		}
	}
	return n
}

func centered(s string, width, height int) []string {
	out := make([]string, height)
	pad := max(0, (width-sw(s))/2)
	out[height/2] = strings.Repeat(" ", pad) + s
	return out
}

func errorBox(msg string, width, height int) []string {
	var out []string
	for _, l := range wrapPlain(msg, max(10, width-4)) {
		out = append(out, "  "+fg(th.red).Render(l))
		if len(out) >= height {
			break
		}
	}
	return out
}

func widgetBody(w *widget, d *widgetData, width, height int, o renderOpts) []string {
	switch w.Type {
	case "note", "free_text":
		return noteLines(str(w.Def["content"])+str(w.Def["text"]), width, height)
	}
	if d == nil {
		switch w.Type {
		case "timeseries", "query_value", "toplist", "change", "list_stream", "log_stream", "manage_status", "hostmap":
			return nil
		}
		return centered(sMuted().Render(strings.ReplaceAll(w.Type, "_", " ")+" "+gDot+" o opens it in Datadog"), width, height)
	}
	switch w.Type {
	case "timeseries":
		return timeseriesLines(w, d, width, height, o)
	case "query_value":
		return queryValueLines(w, d, width, height)
	case "toplist", "sunburst", "treemap", "geomap", "query_table":
		return toplistLines(w, d, width, height)
	case "change":
		return changeLines(w, d, width, height)
	case "hostmap":
		return hostmapLines(d, width, height)
	case "list_stream", "log_stream":
		return logLines(d, width, height)
	case "manage_status", "monitor_summary":
		return monitorSummaryLines(d, width, height)
	}
	return centered(sMuted().Render("No data"), width, height)
}

// ─── timeseries ──────────────────────────────────────────────────

func timeseriesLines(w *widget, d *widgetData, width, height int, o renderOpts) []string {
	if len(d.series) == 0 {
		return centered(sMuted().Render("No data"), width, height)
	}
	legendRows := 0
	switch {
	case o.zoom:
		legendRows = min(len(d.series), max(1, height/3))
	case height >= 9 && len(d.series) > 1:
		legendRows = 1
	}
	chartH := height - legendRows
	mn, mx, loose := axisBounds(w.Def)
	c := viz.Chart{
		Width: width - 1, Height: chartH, Series: d.series, Kind: d.kind, Style: o.style,
		Unit: d.unit, Markers: widgetMarkers(w.Def), Min: mn, Max: mx, LooseZero: loose,
		Cursor: o.cursor, Axis: th.muted, CursorBG: th.sel,
	}
	if len(d.series) > 0 && len(d.series[0].Points) > 0 {
		pts := d.series[0].Points
		c.From, c.To = pts[0].T, pts[len(pts)-1].T
	}
	lines := make([]string, 0, height)
	for _, l := range c.Render() {
		lines = append(lines, " "+l)
	}
	if legendRows == 0 {
		return lines
	}
	var values []float64
	if o.zoom && o.cursor >= 0 {
		_, values = c.At(o.cursor)
	}
	return append(lines, legendLines(d, values, width, legendRows, o.zoom)...)
}

// legendLines lists series with their last value (or the value under the
// cursor when zoomed), biggest first.
func legendLines(d *widgetData, atCursor []float64, width, rows int, zoom bool) []string {
	type entry struct {
		name  string
		color lipglossColor
		v     float64
	}
	var es []entry
	for i, s := range d.series {
		v := lastValue(s.Points)
		if atCursor != nil && i < len(atCursor) {
			v = atCursor[i]
		}
		es = append(es, entry{s.Name, s.Color, v})
	}
	sort.SliceStable(es, func(i, j int) bool {
		a, b := es[i].v, es[j].v
		if math.IsNaN(a) {
			return false
		}
		if math.IsNaN(b) {
			return true
		}
		return a > b
	})
	var out []string
	if zoom {
		valW := 8
		for _, e := range es {
			if len(out) == rows-1 && len(es) > rows {
				out = append(out, sMuted().Render(fmt.Sprintf("  … %d more series", len(es)-len(out))))
				break
			}
			name := trunc(e.name, max(8, width-valW-6))
			out = append(out, "  "+fg(e.color).Render("■")+" "+fit(name, max(8, width-valW-6))+" "+fitRight(viz.Format(e.v, d.unit), valW))
		}
		return out
	}
	line := " "
	shown := 0
	for _, e := range es {
		item := fg(e.color).Render("■") + " " + trunc(e.name, 24) + " " + sMuted().Render(viz.Format(e.v, d.unit))
		if sw(line)+sw(item)+3 > width {
			break
		}
		line += item + "   "
		shown++
	}
	if shown < len(es) {
		line += sMuted().Render(fmt.Sprintf("+%d", len(es)-shown))
	}
	return []string{line}
}

func lastValue(pts []viz.Point) float64 {
	for i := len(pts) - 1; i >= 0; i-- {
		if !math.IsNaN(pts[i].V) {
			return pts[i].V
		}
	}
	return math.NaN()
}

// ─── query value ─────────────────────────────────────────────────

func queryValueLines(w *widget, d *widgetData, width, height int) []string {
	if len(d.rows) == 0 {
		return centered(sMuted().Render("No data"), width, height)
	}
	v := d.rows[0].value
	text, unitText := queryValueText(w.Def, v, d.rows[0].unit)
	color := lipglossColor(th.text)
	specs := widgetRequests(w.Def, nil)
	if len(specs) > 0 {
		if c, ok := condColor(specs[0].conds, v); ok {
			color = c
		}
	}
	out := make([]string, height)
	big, bw := viz.BigText(text, color)
	var subParts []string
	if unitText != "" {
		subParts = append(subParts, unitText)
	}
	if a := aggregatorOf(specs); a != "" {
		subParts = append(subParts, a)
	}
	sub := sMuted().Render(strings.Join(subParts, " "+gDot+" "))
	switch {
	case bw+2 <= width && height >= 4:
		top := max(0, (height-3-boolInt(len(d.spark) > 0))/2)
		for i, l := range big {
			if top+i < height {
				out[top+i] = strings.Repeat(" ", (width-bw)/2) + l
			}
		}
		if len(d.spark) > 0 && top+3 < height {
			out[top+3] = strings.Repeat(" ", 2) + viz.Sparkline(d.spark, width-4, th.faint)
		} else if sub != "" && top+3 < height {
			out[top+3] = strings.Repeat(" ", max(0, (width-sw(sub))/2)) + sub
		}
	default:
		s := lipgloss.NewStyle().Bold(true).Foreground(color).Render(text)
		if unitText != "" {
			s += " " + sMuted().Render(unitText)
		}
		mid := height / 2
		out[mid] = strings.Repeat(" ", max(0, (width-sw(s))/2)) + s
		if len(d.spark) > 0 && mid+1 < height {
			out[mid+1] = " " + viz.Sparkline(d.spark, width-2, th.faint)
		}
	}
	return out
}

// queryValueText formats a query value like Datadog: fixed decimals
// ("precision", 2 by default), the widget's custom unit, and k/M/G only
// when autoscale is on. The unit is returned apart: it's drawn small.
func queryValueText(def map[string]any, v float64, u viz.Unit) (string, string) {
	custom := str(def["custom_unit"])
	autoscale := true
	if a, ok := def["autoscale"].(bool); ok {
		autoscale = a
	}
	prec := 2
	if p, ok := def["precision"].(float64); ok {
		prec = int(p)
	}
	if custom == "" && u.Family != "" && autoscale {
		return viz.Format(v, u), "" // Datadog's unit, scaled (350ms, 1.5GiB)
	}
	num := strconv.FormatFloat(v, 'f', prec, 64)
	if autoscale {
		num = viz.Format(v, viz.Unit{})
	}
	unit := custom
	if unit == "" {
		unit = u.Short
	}
	return num, unit
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

func aggregatorOf(specs []requestSpec) string {
	if len(specs) == 0 {
		return ""
	}
	if specs[0].aggr != "" {
		return specs[0].aggr
	}
	for _, q := range specs[0].queries {
		if a := str(q["aggregator"]); a != "" {
			return a
		}
	}
	return ""
}

// condColor applies the first matching conditional format.
func condColor(conds []condFormat, v float64) (lipglossColor, bool) {
	for _, c := range conds {
		ok := false
		switch c.comparator {
		case ">":
			ok = v > c.value
		case ">=":
			ok = v >= c.value
		case "<":
			ok = v < c.value
		case "<=":
			ok = v <= c.value
		}
		if ok {
			p := c.palette
			switch {
			case strings.Contains(p, "red"):
				return th.red, true
			case strings.Contains(p, "yellow"):
				return th.yellow, true
			case strings.Contains(p, "green"):
				return th.green, true
			case strings.Contains(p, "gray"), strings.Contains(p, "grey"):
				return th.muted, true
			}
			return th.accent, true
		}
	}
	return nil, false
}

// ─── lists of values ─────────────────────────────────────────────

func toplistLines(w *widget, d *widgetData, width, height int) []string {
	if len(d.rows) == 0 {
		return centered(sMuted().Render("No data"), width, height)
	}
	maxV := 0.0
	for _, r := range d.rows {
		maxV = math.Max(maxV, math.Abs(r.value))
	}
	specs := widgetRequests(w.Def, nil)
	var conds []condFormat
	if len(specs) > 0 {
		conds = specs[0].conds
	}
	labelW := min(28, max(8, width*2/5))
	valW := 7
	barW := max(3, width-labelW-valW-4)
	out := []string{}
	for i, r := range d.rows {
		if i == height {
			break
		}
		c := lipglossColor(th.blue)
		if cc, ok := condColor(conds, r.value); ok {
			c = cc
		}
		label := r.label
		if label == "" {
			label = "(all)"
		}
		out = append(out, " "+fit(label, labelW)+" "+viz.HBar(math.Abs(r.value), maxV, barW, c)+" "+fitRight(viz.Format(r.value, r.unit), valW))
	}
	return out
}

func changeLines(w *widget, d *widgetData, width, height int) []string {
	if len(d.rows) == 0 {
		return centered(sMuted().Render("No data"), width, height)
	}
	increaseGood := true
	if reqs := list(w.Def["requests"]); len(reqs) > 0 {
		if v, ok := obj(reqs[0])["increase_good"].(bool); ok {
			increaseGood = v
		}
	}
	labelW := max(8, width-26)
	var out []string
	for i, r := range d.rows {
		if i == height {
			break
		}
		delta := ""
		if !math.IsNaN(r.prev) && r.prev != 0 {
			pct := (r.value - r.prev) / math.Abs(r.prev) * 100
			arrow, c := gUp, th.green
			if pct < 0 {
				arrow = gDown
			}
			if (pct > 0) != increaseGood {
				c = th.red
			}
			if math.Abs(pct) < 0.5 {
				c = th.muted
			}
			delta = fg(c).Render(fmt.Sprintf("%s%.0f%%", arrow, math.Abs(pct)))
		}
		out = append(out, " "+fit(r.label, labelW)+" "+fitRight(viz.Format(r.value, r.unit), 8)+"  "+fitRight(delta, 7))
	}
	return out
}

func hostmapLines(d *widgetData, width, height int) []string {
	if len(d.rows) == 0 {
		return centered(sMuted().Render("No data"), width, height)
	}
	lo, hi := math.Inf(1), math.Inf(-1)
	for _, r := range d.rows {
		lo, hi = math.Min(lo, r.value), math.Max(hi, r.value)
	}
	colors := []lipglossColor{th.green, th.green, th.yellow, th.yellow, th.red}
	var cells []string
	for _, r := range d.rows {
		i := 0
		if hi > lo {
			i = int((r.value - lo) / (hi - lo) * float64(len(colors)-1))
		}
		cells = append(cells, fg(colors[i]).Render("■"))
	}
	perRow := max(1, (width-2)/2)
	var out []string
	for i := 0; i < len(cells) && len(out) < height-1; i += perRow {
		out = append(out, " "+strings.Join(cells[i:min(len(cells), i+perRow)], " "))
	}
	return append(out, sMuted().Render(fmt.Sprintf(" %d hosts · %s – %s", len(d.rows), viz.Format(lo, d.rows[0].unit), viz.Format(hi, d.rows[0].unit))))
}

func logLines(d *widgetData, width, height int) []string {
	if len(d.logs) == 0 {
		return centered(sMuted().Render("No logs"), width, height)
	}
	var out []string
	for _, l := range d.logs {
		if len(out) == height {
			break
		}
		a := l.Attributes
		t := ""
		if ts, err := time.Parse(time.RFC3339Nano, a.Timestamp); err == nil {
			t = ts.Local().Format("15:04:05")
		}
		msg := strings.Join(strings.Fields(a.Message), " ")
		out = append(out, " "+sMuted().Render(t)+" "+fg(logStatusColor(a.Status)).Render("●")+" "+
			fg(th.cyan).Render(trunc(a.Service, 14))+" "+trunc(msg, max(1, width-sw(t)-sw(trunc(a.Service, 14))-6)))
	}
	return out
}

func monitorSummaryLines(d *widgetData, width, height int) []string {
	var parts []string
	for _, s := range []string{"Alert", "Warn", "No Data", "OK"} {
		if n := d.monSum[s]; n > 0 || s == "Alert" {
			parts = append(parts, stateIcon(s)+" "+fmt.Sprintf("%d %s", n, s))
		}
	}
	out := []string{" " + strings.Join(parts, "   ")}
	mons := append(d.mons[:0:0], d.mons...)
	sort.SliceStable(mons, func(i, j int) bool { return stateRank(mons[i].Status) < stateRank(mons[j].Status) })
	for _, m := range mons {
		if len(out) == height {
			break
		}
		out = append(out, " "+stateIcon(m.Status)+" "+trunc(m.Name, width-4))
	}
	return out
}

// ─── notes (markdown, lightly) ───────────────────────────────────

var (
	reMDImage  = regexp.MustCompile(`!\[[^\]]*\]\([^)]*\)`)
	reMDLink   = regexp.MustCompile(`\[([^\]]+)\]\([^)]*\)`)
	reMDBold   = regexp.MustCompile(`\*\*([^*]+)\*\*`)
	reMDItalic = regexp.MustCompile(`(^|\s)[_*]([^_*\n]+)[_*]`)
	reMDCode   = regexp.MustCompile("`([^`]+)`")
)

// noteLines renders markdown as readable text: headings bold, bullets as
// •, links as their text, tables kept (they're monospace already).
func noteLines(md string, width, height int) []string {
	var out []string
	for _, raw := range strings.Split(strings.ReplaceAll(md, "\r", ""), "\n") {
		line := strings.TrimRight(raw, " ")
		heading := false
		if t := strings.TrimLeft(line, "#"); t != line && strings.HasPrefix(t, " ") {
			line, heading = strings.TrimSpace(t), true
		}
		if t := strings.TrimSpace(line); strings.HasPrefix(t, "- ") || strings.HasPrefix(t, "* ") {
			indent := len(line) - len(strings.TrimLeft(line, " "))
			line = strings.Repeat(" ", indent) + gBullet + " " + strings.TrimSpace(t[2:])
		}
		line = reMDImage.ReplaceAllString(line, "")
		line = reMDLink.ReplaceAllString(line, "$1")
		line = reMDBold.ReplaceAllString(line, "$1")
		line = reMDItalic.ReplaceAllString(line, "$1$2")
		line = reMDCode.ReplaceAllString(line, "$1")
		if strings.HasPrefix(strings.TrimSpace(line), "|") {
			if strings.Trim(line, "|-: ") == "" {
				continue // table separator row
			}
			out = append(out, " "+trunc(line, width-2))
			continue
		}
		for _, l := range wrapPlain(line, max(10, width-2)) {
			if heading {
				l = sBold().Render(l)
			}
			out = append(out, " "+l)
		}
		if len(out) >= height {
			break
		}
	}
	for len(out) > 0 && strings.TrimSpace(out[0]) == "" {
		out = out[1:]
	}
	if len(out) > height {
		out = out[:height]
		out[height-1] = fit(out[height-1], max(1, width-2)) + sMuted().Render(gEllipsis)
	}
	return out
}
