package tui

import (
	"fmt"
	"sort"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

// dashView shows one dashboard: widgets laid out like in Datadog, data
// fetched for what's on screen, a focused widget you can zoom into.
type dashView struct {
	ctx   *appCtx
	id    string
	title string
	dash  *dashboard
	err   error

	data    map[*widget]*widgetData
	loading map[*widget]bool
	focus   int // index into visible widgets (reading order)
	scroll  int
	height  int // canvas height
	w, h    int
	density int // terminal rows per grid unit
	zoomed  *widget
	cursor  int
	lastRef time.Time
	cache   map[*widget]cachedRender
}

type cachedRender struct {
	key   string
	lines []string
}

type dashLoadedMsg struct {
	id   string
	dash *dashboard
	err  error
}

type widgetDataMsg struct {
	id string
	w  *widget
	d  *widgetData
}

type dashTickMsg struct{ id string }

func newDashView(ctx *appCtx, id, title string) *dashView {
	return &dashView{ctx: ctx, id: id, title: title, data: map[*widget]*widgetData{}, loading: map[*widget]bool{},
		cache: map[*widget]cachedRender{}, density: 4, cursor: -1}
}

func (v *dashView) Title() string { return v.title }

func (v *dashView) Init() tea.Cmd {
	api, id := v.ctx.api, v.id
	return tea.Batch(func() tea.Msg {
		m, err := api.GetDashboard(id)
		if err != nil {
			return dashLoadedMsg{id: id, err: err}
		}
		return dashLoadedMsg{id: id, dash: parseDashboard(m)}
	}, v.tick())
}

func (v *dashView) tick() tea.Cmd {
	id := v.id
	return tickFn(60*time.Second, func(time.Time) tea.Msg { return dashTickMsg{id: id} })
}

func (v *dashView) SetSize(w, h int) {
	if w != v.w {
		v.cache = map[*widget]cachedRender{}
	}
	v.w, v.h = w, h
	v.relayout()
}

func (v *dashView) bodyHeight() int { return max(1, v.h-2) } // header + tvars line

func (v *dashView) relayout() {
	if v.dash == nil || v.w <= 0 {
		return
	}
	v.height = layoutWidgets(v.dash.Widgets, 1, 0, v.w-2, v.density)
	v.cache = map[*widget]cachedRender{}
	v.clampScroll()
}

func (v *dashView) widgets() []*widget {
	if v.dash == nil {
		return nil
	}
	return flatten(v.dash.Widgets)
}

func (v *dashView) focused() *widget {
	ws := v.widgets()
	if v.focus >= 0 && v.focus < len(ws) {
		return ws[v.focus]
	}
	return nil
}

// rangeKey identifies the data a widget shows: time range + variables.
func (v *dashView) rangeKey() string {
	var b strings.Builder
	b.WriteString(v.ctx.tr.label())
	if v.dash != nil {
		for _, t := range v.dash.TVars {
			b.WriteString("|" + t.Name + "=" + t.Value)
		}
	}
	return b.String()
}

// fetchVisible asks for data of widgets on (or near) screen that don't
// have current data.
func (v *dashView) fetchVisible(force bool) tea.Cmd {
	if v.dash == nil {
		return nil
	}
	top, bottom := v.scroll-v.bodyHeight()/2, v.scroll+v.bodyHeight()*3/2
	key := v.rangeKey()
	var cmds []tea.Cmd
	for _, w := range v.widgets() {
		if w.Type == "group" || w.y+w.h < top || w.y > bottom {
			continue
		}
		if v.loading[w] {
			continue
		}
		if d := v.data[w]; d != nil && d.key == key && !force && !tooCoarse(d.cols, v.wantCols(w)) {
			continue
		}
		if !needsData(w.Type) {
			continue
		}
		v.loading[w] = true
		cmds = append(cmds, v.fetch(w, key))
	}
	return tea.Batch(cmds...)
}

func needsData(t string) bool {
	switch t {
	case "timeseries", "query_value", "toplist", "change", "hostmap", "list_stream", "log_stream",
		"manage_status", "monitor_summary", "sunburst", "treemap", "geomap", "query_table":
		return true
	}
	return false
}

// wantCols is how many columns a widget's data is drawn in.
func (v *dashView) wantCols(w *widget) int {
	if v.zoomed == w {
		return v.w
	}
	return max(10, w.w-8)
}

func (v *dashView) fetch(w *widget, key string) tea.Cmd {
	api, id := v.ctx.api, v.id
	tvars := append([]tvar(nil), v.dash.TVars...)
	tr := v.ctx.tr
	cols, rows := v.wantCols(w), max(3, w.h-1)
	if v.zoomed == w {
		rows = v.bodyHeight()
	}
	return func() tea.Msg {
		d := limited(func() *widgetData { return fetchWidget(api, w, tvars, tr, cols, rows) })
		if d == nil {
			d = &widgetData{}
		}
		d.key, d.at, d.cols = key, now(), cols
		return widgetDataMsg{id: id, w: w, d: d}
	}
}

func (v *dashView) Update(msg tea.Msg) (screen, tea.Cmd) {
	switch msg := msg.(type) {
	case dashLoadedMsg:
		if msg.id != v.id {
			return v, nil
		}
		v.dash, v.err = msg.dash, msg.err
		if v.dash != nil {
			if v.dash.Title != "" {
				v.title = v.dash.Title
			}
			v.relayout()
			v.lastRef = now()
			return v, v.fetchVisible(false)
		}
	case widgetDataMsg:
		if msg.id != v.id {
			return v, nil
		}
		delete(v.loading, msg.w)
		if msg.d.key == v.rangeKey() {
			v.data[msg.w] = msg.d
			delete(v.cache, msg.w)
			// Asked for before the terminal size was known: ask again.
			if needsData(msg.w.Type) && tooCoarse(msg.d.cols, v.wantCols(msg.w)) {
				v.loading[msg.w] = true
				return v, v.fetch(msg.w, msg.d.key)
			}
		}
	case dashTickMsg:
		if msg.id != v.id {
			return v, nil
		}
		cmds := []tea.Cmd{v.tick()}
		if v.ctx.tr.end.IsZero() && v.ctx.tr.span <= 2*24*time.Hour {
			v.lastRef = now()
			cmds = append(cmds, v.fetchVisible(true))
		}
		return v, tea.Batch(cmds...)
	case rangeChangedMsg, tvarsChangedMsg:
		v.cache = map[*widget]cachedRender{}
		return v, v.fetchVisible(false)
	case resizedMsg:
		return v, v.fetchVisible(false)
	case spinMsg:
		for w := range v.loading {
			delete(v.cache, w)
		}
	case tea.KeyMsg:
		return v.key(msg)
	case tea.MouseMsg:
		if msg.Action == tea.MouseActionPress {
			switch msg.Button {
			case tea.MouseButtonWheelDown:
				v.scrollBy(3)
				return v, v.fetchVisible(false)
			case tea.MouseButtonWheelUp:
				v.scrollBy(-3)
				return v, v.fetchVisible(false)
			}
		}
	}
	return v, nil
}

func (v *dashView) key(k tea.KeyMsg) (screen, tea.Cmd) {
	if v.zoomed != nil {
		switch k.String() {
		case "esc", "enter", "q", "z":
			v.zoomed, v.cursor = nil, -1
			v.cache = map[*widget]cachedRender{}
			return v, nil
		case "left", "h":
			v.cursor = max(0, v.cursor-1)
		case "right", "l":
			v.cursor = min(v.w-10, v.cursor+1)
		case "H", "shift+left":
			v.cursor = max(0, v.cursor-10)
		case "L", "shift+right":
			v.cursor = min(v.w-10, v.cursor+10)
		case "o":
			return v, openURL(v.widgetURL(v.zoomed))
		case "r":
			key := v.rangeKey()
			v.loading[v.zoomed] = true
			return v, v.fetch(v.zoomed, key)
		}
		return v, nil
	}
	ws := v.widgets()
	switch k.String() {
	case "down", "j":
		v.move(0, 1)
	case "up", "k":
		v.move(0, -1)
	case "right", "l":
		v.move(1, 0)
	case "left", "h":
		v.move(-1, 0)
	case "tab":
		if len(ws) > 0 {
			v.focus = (v.focus + 1) % len(ws)
		}
	case "shift+tab":
		if len(ws) > 0 {
			v.focus = (v.focus - 1 + len(ws)) % len(ws)
		}
	case "g", "home":
		v.focus, v.scroll = 0, 0
	case "G", "end":
		v.focus = max(0, len(ws)-1)
	case "ctrl+d", "pgdown", " ":
		v.scrollBy(v.bodyHeight() - 2)
		v.focusOnScreen()
		return v, v.fetchVisible(false)
	case "ctrl+u", "pgup":
		v.scrollBy(-(v.bodyHeight() - 2))
		v.focusOnScreen()
		return v, v.fetchVisible(false)
	case "enter", "z":
		w := v.focused()
		if w == nil {
			return v, nil
		}
		if w.Type == "group" {
			w.collapsed = !w.collapsed
			v.relayout()
			return v, v.fetchVisible(false)
		}
		v.zoomed, v.cursor = w, -1
		if w.Type == "timeseries" {
			v.cursor = v.w * 2 / 3
			key := v.rangeKey()
			v.loading[w] = true
			return v, v.fetch(w, key) // more points for the full width
		}
		return v, nil
	case "c":
		// Collapse or expand every group.
		collapse := false
		for _, w := range v.dash.Widgets {
			if w.Type == "group" && !w.collapsed {
				collapse = true
			}
		}
		for _, w := range v.dash.Widgets {
			if w.Type == "group" {
				w.collapsed = collapse
			}
		}
		v.focus = 0
		v.relayout()
		return v, v.fetchVisible(false)
	case "+", "=":
		v.density = min(8, v.density+1)
		v.relayout()
		return v, v.fetchVisible(false)
	case "-", "_":
		v.density = max(2, v.density-1)
		v.relayout()
		return v, v.fetchVisible(false)
	case "r":
		v.lastRef = now()
		return v, v.fetchVisible(true)
	case "o":
		return v, openURL(v.ctx.api.DashboardURL(v.id))
	case "O":
		if w := v.focused(); w != nil {
			return v, openURL(v.widgetURL(w))
		}
	case "v":
		if v.dash != nil && len(v.dash.TVars) > 0 {
			return v, openModal(newTVarPicker(v.ctx, v.dash))
		}
		return v, toast("This dashboard has no template variables", toastInfo)
	default:
		return v, nil
	}
	v.ensureVisible()
	return v, v.fetchVisible(false)
}

func (v *dashView) widgetURL(w *widget) string {
	u := v.ctx.api.DashboardURL(v.id)
	if w != nil && w.ID != 0 {
		u += fmt.Sprintf("?fullscreen_widget=%d", w.ID)
	}
	return u
}

// move shifts focus to the nearest widget in a direction.
func (v *dashView) move(dx, dy int) {
	ws := v.widgets()
	cur := v.focused()
	if cur == nil {
		return
	}
	cx, cy := cur.x+cur.w/2, cur.y
	best, bestD := -1, 1<<30
	for i, w := range ws {
		if w == cur {
			continue
		}
		wx, wy := w.x+w.w/2, w.y
		switch {
		case dy > 0 && !(w.y > cur.y):
			continue
		case dy < 0 && !(w.y < cur.y):
			continue
		case dx > 0 && !(w.x > cur.x && overlaps(w.y, w.h, cur.y, cur.h)):
			continue
		case dx < 0 && !(w.x < cur.x && overlaps(w.y, w.h, cur.y, cur.h)):
			continue
		}
		d := abs(wx-cx) + abs(wy-cy)*3
		if dy != 0 {
			d = abs(wy-cy)*4 + abs(wx-cx)
		}
		if d < bestD {
			best, bestD = i, d
		}
	}
	if best >= 0 {
		v.focus = best
	}
}

func overlaps(y1, h1, y2, h2 int) bool { return y1 < y2+max(1, h2) && y2 < y1+max(1, h1) }

func abs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}

func (v *dashView) scrollBy(n int) {
	v.scroll += n
	v.clampScroll()
}

func (v *dashView) clampScroll() {
	v.scroll = max(0, min(v.scroll, v.height-v.bodyHeight()))
}

// ensureVisible scrolls so the focused widget's title (and as much of it
// as fits) is on screen.
func (v *dashView) ensureVisible() {
	w := v.focused()
	if w == nil {
		return
	}
	bh := v.bodyHeight()
	if w.y < v.scroll {
		v.scroll = w.y
	} else if w.y+min(w.h, bh) > v.scroll+bh {
		v.scroll = w.y + min(w.h, bh) - bh
	}
	v.clampScroll()
}

// focusOnScreen moves the focus to the first widget visible after a page
// scroll.
func (v *dashView) focusOnScreen() {
	for i, w := range v.widgets() {
		if w.y >= v.scroll {
			v.focus = i
			return
		}
	}
}

func (v *dashView) render(w *widget, width, height int, focused bool) []string {
	d := v.data[w]
	key := fmt.Sprintf("%d|%d|%v|%v|%d|%v", width, height, focused, v.loading[w], v.ctx.spin, v.ctx.style)
	if d != nil {
		key += "|" + d.at.String()
	}
	if w.Type == "group" {
		key += fmt.Sprint(w.collapsed)
	}
	if c, ok := v.cache[w]; ok && c.key == key {
		return c.lines
	}
	lines := renderWidget(w, d, v.loading[w], width, height, renderOpts{focused: focused, style: v.ctx.style, spin: v.ctx.spin, cursor: -1})
	v.cache[w] = cachedRender{key: key, lines: lines}
	return lines
}

func (v *dashView) View() string {
	if v.err != nil {
		return frame("\n  "+fg(th.red).Render("Couldn't load the dashboard: ")+v.err.Error(), v.w, v.h)
	}
	if v.dash == nil {
		return frame("\n  "+sMuted().Render(spinnerFrames[v.ctx.spin%len(spinnerFrames)]+" Loading "+v.title+"…"), v.w, v.h)
	}
	header := v.headerLine()
	if v.zoomed != nil {
		return header + "\n" + v.zoomView()
	}
	bh := v.bodyHeight()
	type seg struct {
		x     int
		lines []string
		y     int
	}
	var segs []seg
	fw := v.focused()
	for _, w := range v.widgets() {
		if w.y+w.h <= v.scroll || w.y >= v.scroll+bh {
			continue
		}
		h := w.h
		if w.Type == "group" {
			h = 1
		}
		if w.y+h <= v.scroll {
			continue
		}
		segs = append(segs, seg{x: w.x, y: w.y, lines: v.render(w, w.w, h, w == fw)})
	}
	sort.SliceStable(segs, func(i, j int) bool { return segs[i].x < segs[j].x })
	var out []string
	for r := 0; r < bh; r++ {
		row := v.scroll + r
		var b strings.Builder
		col := 0
		for _, s := range segs {
			i := row - s.y
			if i < 0 || i >= len(s.lines) {
				continue
			}
			if s.x > col {
				b.WriteString(strings.Repeat(" ", s.x-col))
				col = s.x
			}
			if s.x < col {
				continue // overlapping (shouldn't happen): skip
			}
			b.WriteString(s.lines[i])
			col += sw(s.lines[i])
		}
		out = append(out, fit(b.String(), v.w))
	}
	return header + "\n" + strings.Join(out, "\n")
}

func (v *dashView) headerLine() string {
	left := ""
	var chips []string
	if v.dash != nil {
		for _, t := range v.dash.TVars {
			val := t.Value
			if val == "" {
				val = "*"
			}
			chips = append(chips, sMuted().Render("$"+t.Name+" ")+fg(th.cyan).Render(val))
		}
	}
	switch {
	case len(chips) > 0:
		left = " " + strings.Join(chips, sMuted().Render("  "))
	case v.dash != nil && v.dash.Description != "":
		left = " " + sMuted().Render(strings.Join(strings.Fields(v.dash.Description), " "))
	}
	right := ""
	if n := len(v.loading); n > 0 {
		right = sMuted().Render(fmt.Sprintf("%s %d loading  ", spinnerFrames[v.ctx.spin%len(spinnerFrames)], n))
	} else if !v.lastRef.IsZero() {
		right = sMuted().Render("updated " + age(v.lastRef) + "  ")
	}
	if v.height > v.bodyHeight() {
		right += sMuted().Render(fmt.Sprintf("%d%% ", 100*min(v.scroll+v.bodyHeight(), v.height)/max(1, v.height)))
	}
	if right != "" {
		right = "   " + right
	}
	return spread(left, right, v.w) + "\n" + fit(sFaint().Render(strings.Repeat(gRule, v.w)), v.w)
}

func (v *dashView) zoomView() string {
	w := v.zoomed
	bh := v.bodyHeight()
	queryLines := zoomQueries(w, v.dash.TVars, v.w)
	chartH := max(6, bh-len(queryLines))
	lines := renderWidget(w, v.data[w], v.loading[w], v.w-2, chartH,
		renderOpts{focused: true, style: v.ctx.style, spin: v.ctx.spin, zoom: true, cursor: v.cursor})
	var out []string
	for _, l := range lines {
		out = append(out, " "+l)
	}
	out = append(out, queryLines...)
	return frame(strings.Join(out, "\n"), v.w, bh)
}

// zoomQueries lists the widget's queries under the zoomed chart.
func zoomQueries(w *widget, tvars []tvar, width int) []string {
	specs := widgetRequests(w.Def, tvars)
	var out []string
	for _, s := range specs {
		for _, q := range s.queries {
			text := str(q["query"])
			if text == "" {
				if search := obj(q["search"]); search != nil {
					text = str(q["data_source"]) + " · " + str(search["query"])
				}
			}
			if text == "" {
				continue
			}
			out = append(out, "  "+sMuted().Render(fit(str(q["name"])+"  ", 9))+trunc(text, width-13))
		}
		for _, f := range s.formulas {
			if fs := str(f["formula"]); fs != "" && len(s.formulas) > 1 || strings.ContainsAny(fs, "+-*/(") {
				out = append(out, "  "+sMuted().Render(fit("ƒ  ", 9))+trunc(fs, width-13))
			}
		}
	}
	if len(out) > 5 {
		out = append(out[:4], sMuted().Render(fmt.Sprintf("  … %d more", len(out)-4)))
	}
	if len(out) > 0 {
		out = append([]string{""}, out...)
	}
	return out
}

func (v *dashView) Hints() []hint {
	if v.zoomed != nil {
		return []hint{{"←→", "cursor"}, {"H/L", "jump"}, {"o", "open"}, {"r", "refresh"}, {"esc", "back"}}
	}
	return []hint{{"hjkl", "move"}, {"⏎", "zoom"}, {"v", "variables"}, {"c", "fold all"}, {"+/-", "size"}, {"o", "open"}, {"esc", "back"}}
}
