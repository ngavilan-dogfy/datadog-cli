package tui

import (
	"fmt"
	"math"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/ngavilan-dogfy/datadog-cli/viz"

	tea "github.com/charmbracelet/bubbletea"
)

// metricsView explores one metric query: type a metric (names complete as
// you type), see it across the time range, read values under a cursor,
// change the aggregation or the grouping with a key.
type metricsView struct {
	ctx     *appCtx
	query   string
	draft   string
	editing bool
	sugg    []string
	suggCur int
	suggGen int
	data    *widgetData
	loading bool
	gen     int
	cursor  int
	w, h    int
}

type metricsDataMsg struct {
	gen int
	d   *widgetData
}

type metricSuggestMsg struct {
	gen   int
	names []string
}

func newMetrics(ctx *appCtx, q string) screen {
	v := &metricsView{ctx: ctx, query: q, cursor: -1}
	if q == "" {
		v.editing = true
	}
	return v
}

func (v *metricsView) Title() string    { return "Metrics" }
func (v *metricsView) Typing() bool     { return v.editing }
func (v *metricsView) SetSize(w, h int) { v.w, v.h = w, h }

func (v *metricsView) Init() tea.Cmd {
	if v.query == "" {
		return nil
	}
	return v.run()
}

func (v *metricsView) run() tea.Cmd {
	v.gen++
	v.loading = true
	api, q, gen, tr := v.ctx.api, v.query, v.gen, v.ctx.tr
	return func() tea.Msg {
		from, to := tr.bounds()
		d := &widgetData{}
		res, err := api.QueryMetrics(q, from/1000, to/1000)
		if err != nil {
			d.err = err
			return metricsDataMsg{gen, d}
		}
		pal := viz.Palette()
		for i, s := range res.Series {
			pts := make([]viz.Point, 0, len(s.Pointlist))
			for _, p := range s.Pointlist {
				if len(p) == 2 {
					pts = append(pts, viz.Point{T: int64(p[0]), V: p[1]})
				}
			}
			name := s.Scope
			if name == "" || name == "*" {
				name = s.Expression
			}
			d.series = append(d.series, viz.Series{Name: name, Color: pal[i%len(pal)], Points: pts})
			if d.unit == (viz.Unit{}) && len(s.Unit) > 0 && s.Unit[0].Name != "" {
				u := s.Unit[0]
				d.unit = viz.Unit{Family: u.Family, Name: u.Name, Short: u.ShortName, Scale: u.ScaleFactor}
			}
		}
		return metricsDataMsg{gen, d}
	}
}

// suggest completes the metric name being typed.
func (v *metricsView) suggest() tea.Cmd {
	word := metricWord(v.draft)
	if len(word) < 2 {
		v.sugg = nil
		return nil
	}
	v.suggGen++
	api, gen := v.ctx.api, v.suggGen
	return tickFn(250*time.Millisecond, func(time.Time) tea.Msg {
		names, _ := api.SearchMetrics(word)
		return metricSuggestMsg{gen, names}
	})
}

var reMetricWord = regexp.MustCompile(`([a-zA-Z0-9_.]+)$`)

// metricWord is the metric name at the end of the draft ("avg:sys" → "sys").
func metricWord(s string) string {
	if strings.ContainsAny(s, "{(") && !strings.HasSuffix(s, ":") {
		if i := strings.LastIndexAny(s, "{}()"); i >= 0 && s[i] != ')' {
			return "" // typing a scope or a function argument
		}
	}
	m := reMetricWord.FindStringSubmatch(s)
	if m == nil {
		return ""
	}
	return m[1]
}

// completeWith puts a metric name in place of the word being typed, adding
// avg: and {*} when the query had neither.
func completeWith(draft, name string) string {
	word := metricWord(draft)
	q := strings.TrimSuffix(draft, word) + name
	if !strings.Contains(q, ":") {
		q = "avg:" + q
	}
	if !strings.Contains(q, "{") {
		q += "{*}"
	}
	return q
}

var reAggr = regexp.MustCompile(`^(avg|sum|min|max):`)

// cycleAggregation rotates the space aggregator: avg → sum → min → max.
func cycleAggregation(q string) string {
	order := []string{"avg", "sum", "min", "max"}
	m := reAggr.FindStringSubmatch(q)
	if m == nil {
		return "sum:" + q
	}
	for i, a := range order {
		if a == m[1] {
			return order[(i+1)%len(order)] + ":" + q[len(m[0]):]
		}
	}
	return q
}

var reByClause = regexp.MustCompile(`\s+by\s+\{[^}]*\}`)

// setGroupBy replaces (or removes, for "") the "by {tag}" clause.
func setGroupBy(q, tag string) string {
	q = reByClause.ReplaceAllString(q, "")
	if tag == "" {
		return q
	}
	// Before .as_count()/.rollup() and friends.
	if i := strings.Index(q, "}."); i >= 0 {
		return q[:i+1] + " by {" + tag + "}" + q[i+1:]
	}
	return q + " by {" + tag + "}"
}

func (v *metricsView) Update(msg tea.Msg) (screen, tea.Cmd) {
	switch msg := msg.(type) {
	case metricsDataMsg:
		if msg.gen == v.gen {
			v.data, v.loading = msg.d, false
			if v.cursor < 0 && v.data.err == nil {
				v.cursor = max(0, v.w*3/4)
			}
		}
	case metricSuggestMsg:
		if msg.gen == v.suggGen && v.editing {
			v.sugg, v.suggCur = msg.names, 0
			if len(v.sugg) > 8 {
				v.sugg = v.sugg[:8]
			}
		}
	case rangeChangedMsg:
		if v.query != "" {
			return v, v.run()
		}
	case tea.KeyMsg:
		if v.editing {
			return v.editKey(msg)
		}
		switch msg.String() {
		case "/", "e", "enter":
			v.editing, v.draft, v.sugg = true, v.query, nil
		case "left", "h":
			v.cursor = max(0, v.cursor-1)
		case "right", "l":
			v.cursor = min(v.w, v.cursor+1)
		case "H":
			v.cursor = max(0, v.cursor-10)
		case "L":
			v.cursor = min(v.w, v.cursor+10)
		case "a":
			if v.query != "" {
				v.query = cycleAggregation(v.query)
				return v, v.run()
			}
		case "b":
			if v.query != "" {
				return v, openModal(newGroupByPicker(v))
			}
		case "r":
			if v.query != "" {
				return v, v.run()
			}
		case "o":
			if v.query != "" {
				return v, openURL(v.ctx.api.BrowseURL("/metric/explorer?exp_metric=" + url.QueryEscape(v.query)))
			}
		}
	}
	return v, nil
}

func (v *metricsView) editKey(k tea.KeyMsg) (screen, tea.Cmd) {
	switch k.String() {
	case "esc":
		if v.query != "" {
			v.editing, v.sugg = false, nil
		}
		return v, nil
	case "enter":
		if len(v.sugg) > 0 && metricWord(v.draft) != "" && !strings.Contains(v.draft, "{") {
			v.draft = completeWith(v.draft, v.sugg[v.suggCur])
		}
		v.query = strings.TrimSpace(v.draft)
		if v.query == "" {
			return v, nil
		}
		v.editing, v.sugg = false, nil
		return v, v.run()
	case "tab":
		if len(v.sugg) > 0 {
			v.draft = completeWith(v.draft, v.sugg[v.suggCur])
			v.sugg = nil
		}
		return v, nil
	case "down", "ctrl+n":
		v.suggCur = min(len(v.sugg)-1, v.suggCur+1)
		return v, nil
	case "up", "ctrl+p":
		v.suggCur = max(0, v.suggCur-1)
		return v, nil
	case "backspace":
		if r := []rune(v.draft); len(r) > 0 {
			v.draft = string(r[:len(r)-1])
		}
	case "ctrl+u":
		v.draft = ""
	default:
		if k.Type == tea.KeyRunes {
			v.draft += string(k.Runes)
		} else if k.String() == " " {
			v.draft += " "
		} else {
			return v, nil
		}
	}
	return v, v.suggest()
}

func newGroupByPicker(v *metricsView) modal {
	var items []pickItem
	for _, t := range []string{"host", "service", "env", "availability-zone", "region", "device", "container_name", "kube_namespace", "pod_name"} {
		items = append(items, pickItem{label: t, value: t})
	}
	items = append([]pickItem{{label: "(no grouping)", value: "-"}}, items...)
	return newPickList("Group by — pick or type a tag", items, true, func(tag string) tea.Cmd {
		if tag == "-" {
			tag = ""
		}
		v.query = setGroupBy(v.query, tag)
		return v.run()
	})
}

func (v *metricsView) View() string {
	var out []string
	q := v.query
	if v.editing {
		q = v.draft + sAccent().Render("▏")
	}
	right := ""
	if v.loading {
		right = sMuted().Render(spinnerFrames[v.ctx.spin%len(spinnerFrames)]+" querying") + " "
	}
	out = append(out, spread(" "+sBold().Render("Metrics")+"  "+sAccent().Render("› ")+q, right, v.w))
	if v.editing && len(v.sugg) > 0 {
		for i, s := range v.sugg {
			row := "   " + s
			if i == v.suggCur {
				row = paintBg(fg(th.accent).Render("  "+gSel)+fit(s, v.w-4), th.sel)
			}
			out = append(out, fit(row, v.w))
		}
		out = append(out, "   "+sMuted().Render("tab completes · ⏎ runs · ↑↓ choose"))
	}
	out = append(out, "")
	switch {
	case v.query == "" || (v.editing && v.data == nil):
		out = append(out, v.help()...)
		return frame(strings.Join(out, "\n"), v.w, v.h)
	case v.data == nil:
		return frame(strings.Join(out, "\n"), v.w, v.h)
	case v.data.err != nil:
		out = append(out, "  "+fg(th.red).Render("Query failed: ")+v.data.err.Error(), "",
			"  "+sMuted().Render("Syntax: avg:metric.name{tag:value} by {tag} — press / to edit."))
		return frame(strings.Join(out, "\n"), v.w, v.h)
	case len(v.data.series) == 0:
		out = append(out, "  "+sMuted().Render("No data for this query in "+strings.ToLower(v.ctx.tr.label())+"."))
		return frame(strings.Join(out, "\n"), v.w, v.h)
	}
	legendRows := min(len(v.data.series), max(3, (v.h-len(out))/3))
	chartH := max(6, v.h-len(out)-legendRows-2)
	c := viz.Chart{Width: v.w - 3, Height: chartH, Series: v.data.series, Style: v.ctx.style, Unit: v.data.unit,
		Cursor: v.cursor, Axis: th.muted, CursorBG: th.sel}
	c.From, c.To = v.ctx.tr.bounds()
	if v.cursor >= c.PlotWidth() {
		v.cursor = c.PlotWidth() - 1
		c.Cursor = v.cursor
	}
	for _, l := range c.Render() {
		out = append(out, "  "+l)
	}
	t, vals := c.At(v.cursor)
	out = append(out, "  "+sMuted().Render("at "+time.UnixMilli(t).Local().Format("Jan 2 15:04")+"   min / avg / max over the range"))
	nameW := min(60, max(20, v.w-50))
	for i, s := range v.data.series {
		if i == legendRows {
			out = append(out, sMuted().Render(fmt.Sprintf("  … %d more series", len(v.data.series)-i)))
			break
		}
		lo, hi, sum, n := math.Inf(1), math.Inf(-1), 0.0, 0
		for _, p := range s.Points {
			if !math.IsNaN(p.V) {
				lo, hi, sum, n = math.Min(lo, p.V), math.Max(hi, p.V), sum+p.V, n+1
			}
		}
		stats := "–"
		if n > 0 {
			stats = viz.Format(lo, v.data.unit) + " / " + viz.Format(sum/float64(n), v.data.unit) + " / " + viz.Format(hi, v.data.unit)
		}
		at := "–"
		if i < len(vals) {
			at = viz.Format(vals[i], v.data.unit)
		}
		out = append(out, "  "+fg(s.Color).Render("■")+" "+fit(s.Name, nameW)+" "+fitRight(at, 9)+"   "+sMuted().Render(stats))
	}
	return frame(strings.Join(out, "\n"), v.w, v.h)
}

func (v *metricsView) help() []string {
	lines := []string{
		"  Type a metric name — suggestions appear as you type, tab completes.",
		"",
		"  " + sBold().Render("Examples"),
		"    " + fg(th.cyan).Render("avg:system.cpu.user{*} by {host}"),
		"    " + fg(th.cyan).Render("sum:trace.http.request.hits{service:api}.as_count()"),
		"    " + fg(th.cyan).Render("p95:trace.http.request{env:prod}"),
		"",
		"  Then: " + sAccent().Render("a") + " avg/sum/min/max  " + sAccent().Render("b") + " group by  " +
			sAccent().Render("←→") + " read values  " + sAccent().Render("t") + " time range",
	}
	return lines
}

func (v *metricsView) Hints() []hint {
	if v.editing {
		return []hint{{"tab", "complete"}, {"⏎", "run"}, {"esc", "cancel"}}
	}
	return []hint{{"/", "query"}, {"←→", "cursor"}, {"a", "aggregation"}, {"b", "group by"}, {"o", "browser"}}
}
