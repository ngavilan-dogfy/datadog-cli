package tui

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/ngavilan-dogfy/datadog-cli/datadog"
	"github.com/ngavilan-dogfy/datadog-cli/viz"

	tea "github.com/charmbracelet/bubbletea"
)

// ─── shared ──────────────────────────────────────────────────────

type monitorsMsg struct {
	mons  []datadog.Monitor
	muted map[int64][]string // monitor id → active downtime ids
	err   error
}

func fetchMonitors(api API) tea.Cmd {
	return func() tea.Msg {
		mons, err := api.ListMonitors("", 1000)
		if err != nil {
			return monitorsMsg{err: err}
		}
		return monitorsMsg{mons: mons, muted: activeDowntimes(api)}
	}
}

// activeDowntimes maps monitors to the downtimes silencing them now.
func activeDowntimes(api API) map[int64][]string {
	out := map[int64][]string{}
	dts, err := api.ListDowntimes()
	if err != nil {
		return out
	}
	for _, d := range dts {
		a := d.Attributes
		if a.Canceled != nil || (a.Status != "" && a.Status != "active") {
			continue
		}
		if a.MonitorIdentifier != nil && a.MonitorIdentifier.MonitorID != nil {
			id := *a.MonitorIdentifier.MonitorID
			out[id] = append(out[id], d.ID)
		}
	}
	return out
}

func priorityLabel(p *int) string {
	if p == nil || *p <= 0 {
		return "  "
	}
	c := th.muted
	switch *p {
	case 1:
		c = th.red
	case 2:
		c = th.yellow
	}
	return fg(c).Render(fmt.Sprintf("P%d", *p))
}

func monitorKind(t string) string {
	switch t {
	case "query alert", "metric alert":
		return "metric"
	case "log alert":
		return "logs"
	case "trace-analytics alert", "apm alert":
		return "apm"
	case "event-v2 alert", "event alert":
		return "events"
	case "rum alert":
		return "rum"
	case "service check":
		return "check"
	case "synthetics alert":
		return "synthetics"
	case "slo alert":
		return "slo"
	}
	return strings.TrimSuffix(t, " alert")
}

// tagValue finds key:value among tags.
func tagValue(tags []string, key string) string {
	for _, t := range tags {
		if strings.HasPrefix(t, key+":") {
			return t[len(key)+1:]
		}
	}
	return ""
}

// serviceOf guesses a monitor's service from its tags or query.
func serviceOf(m datadog.Monitor) string {
	if s := tagValue(m.Tags, "service"); s != "" {
		return s
	}
	if i := strings.Index(m.Query, "service:"); i >= 0 {
		s := m.Query[i+len("service:"):]
		if j := strings.IndexAny(s, ",} \")"); j >= 0 {
			s = s[:j]
		}
		return s
	}
	return ""
}

// ─── mute ────────────────────────────────────────────────────────

type mutedMsg struct {
	id   int64
	what string
	err  error
}

func newMutePicker(ctx *appCtx, m datadog.Monitor) modal {
	tomorrow9 := time.Date(now().Year(), now().Month(), now().Day()+1, 9, 0, 0, 0, time.Local)
	items := []pickItem{
		{label: "For 1 hour", value: "1h"},
		{label: "For 4 hours", value: "4h"},
		{label: "Until tomorrow 09:00", right: sMuted().Render(tomorrow9.Format("Mon 15:04")), value: "t9"},
		{label: "For 1 day", value: "24h"},
		{label: "For 1 week", value: "168h"},
		{label: "Until I unmute it", value: "forever"},
	}
	api, id, name := ctx.api, m.ID, m.Name
	p := newPickList("Mute «"+trunc(name, 40)+"»", items, false, func(v string) tea.Cmd {
		return func() tea.Msg {
			var end time.Time
			what := "until you unmute it"
			switch v {
			case "t9":
				end, what = tomorrow9, "until tomorrow 09:00"
			case "forever":
			default:
				d, _ := time.ParseDuration(v)
				end, what = now().Add(d), "for "+viz.FormatSpan(d)
			}
			_, err := api.ScheduleDowntime("*", "Muted from datadog ui", now(), end, &id)
			return mutedMsg{id: id, what: "Muted " + what, err: err}
		}
	})
	p.width = 56
	return p
}

func cmdUnmute(api API, id int64, downtimes []string) tea.Cmd {
	return func() tea.Msg {
		if len(downtimes) == 0 {
			return mutedMsg{id: id, err: fmt.Errorf("no active downtime silences this monitor")}
		}
		for _, d := range downtimes {
			if err := api.CancelDowntime(d); err != nil {
				return mutedMsg{id: id, err: err}
			}
		}
		return mutedMsg{id: id, what: "Unmuted"}
	}
}

// ─── the list ────────────────────────────────────────────────────

type monitorList struct {
	ctx     *appCtx
	mons    []datadog.Monitor
	muted   map[int64][]string
	err     error
	loading bool
	at      time.Time

	rows    []monRow
	cur     int
	scroll  int
	folded  map[string]bool
	filter  string
	editing bool
	w, h    int
}

type monRow struct {
	group string // header row when mon == nil
	count int
	mon   *datadog.Monitor
}

type monTickMsg struct{}

func newMonitors(ctx *appCtx) screen {
	return &monitorList{ctx: ctx, loading: true, folded: map[string]bool{"OK": true}, muted: map[int64][]string{}}
}

func (l *monitorList) Title() string    { return "Monitors" }
func (l *monitorList) Typing() bool     { return l.editing }
func (l *monitorList) SetSize(w, h int) { l.w, l.h = w, h }

func (l *monitorList) Init() tea.Cmd {
	return tea.Batch(fetchMonitors(l.ctx.api), tickFn(60*time.Second, func(time.Time) tea.Msg { return monTickMsg{} }))
}

func (l *monitorList) rebuild() {
	terms := queryTerms(l.filter)
	groups := map[string][]*datadog.Monitor{}
	for i := range l.mons {
		m := &l.mons[i]
		if len(terms) > 0 {
			hay := fold(m.Name + " " + strings.Join(m.Tags, " ") + " " + m.Type + " " + monitorState(m.OverallState))
			ok := true
			for _, t := range terms {
				if !strings.Contains(hay, t) {
					ok = false
				}
			}
			if !ok {
				continue
			}
		}
		st := monitorState(m.OverallState)
		groups[st] = append(groups[st], m)
	}
	var names []string
	for g := range groups {
		names = append(names, g)
	}
	sort.Slice(names, func(i, j int) bool {
		ri, rj := stateRank(names[i]), stateRank(names[j])
		if ri != rj {
			return ri < rj
		}
		return names[i] < names[j]
	})
	var cur *datadog.Monitor
	curGroup := ""
	if l.cur < len(l.rows) {
		cur, curGroup = l.rows[l.cur].mon, l.rows[l.cur].group
	}
	l.rows = l.rows[:0]
	for _, g := range names {
		ms := groups[g]
		sort.SliceStable(ms, func(i, j int) bool {
			pi, pj := 9, 9
			if ms[i].Priority != nil && *ms[i].Priority > 0 {
				pi = *ms[i].Priority
			}
			if ms[j].Priority != nil && *ms[j].Priority > 0 {
				pj = *ms[j].Priority
			}
			if pi != pj {
				return pi < pj
			}
			return ms[i].Name < ms[j].Name
		})
		l.rows = append(l.rows, monRow{group: g, count: len(ms)})
		if l.folded[g] && len(terms) == 0 {
			continue
		}
		for _, m := range ms {
			l.rows = append(l.rows, monRow{mon: m})
		}
	}
	l.cur = 0
	for i, r := range l.rows {
		if (cur != nil && r.mon != nil && r.mon.ID == cur.ID) || (cur == nil && r.mon == nil && r.group == curGroup && curGroup != "") {
			l.cur = i
			break
		}
	}
	// Start on the first monitor that needs attention.
	if cur == nil && curGroup == "" && len(l.rows) > 1 {
		l.cur = 1
	}
}

func (l *monitorList) selected() *datadog.Monitor {
	if l.cur < len(l.rows) {
		return l.rows[l.cur].mon
	}
	return nil
}

func (l *monitorList) Update(msg tea.Msg) (screen, tea.Cmd) {
	switch msg := msg.(type) {
	case monitorsMsg:
		l.loading = false
		if msg.err != nil {
			l.err = msg.err
			return l, nil
		}
		l.err, l.mons, l.muted, l.at = nil, msg.mons, msg.muted, now()
		l.ctx.monitors = msg.mons
		l.rebuild()
	case monTickMsg:
		return l, tea.Batch(fetchMonitors(l.ctx.api), tickFn(60*time.Second, func(time.Time) tea.Msg { return monTickMsg{} }))
	case mutedMsg:
		if msg.err != nil {
			return l, toast("Couldn't change the mute: "+msg.err.Error(), toastErr)
		}
		return l, tea.Batch(toast(msg.what, toastOK), fetchMonitors(l.ctx.api))
	case investigatedMsg:
		return l, investigatedToast(msg)
	case tea.KeyMsg:
		if l.editing {
			switch msg.String() {
			case "esc":
				l.editing, l.filter = false, ""
			case "enter":
				l.editing = false
			case "backspace":
				if r := []rune(l.filter); len(r) > 0 {
					l.filter = string(r[:len(r)-1])
				}
			default:
				if isPrintable(msg.String()) || msg.String() == " " {
					l.filter += msg.String()
				}
			}
			l.rebuild()
			return l, nil
		}
		switch msg.String() {
		case "/":
			l.editing = true
		case "down", "j":
			l.cur = min(len(l.rows)-1, l.cur+1)
		case "up", "k":
			l.cur = max(0, l.cur-1)
		case "g", "home":
			l.cur = 0
		case "G", "end":
			l.cur = max(0, len(l.rows)-1)
		case "enter", "l", "right":
			if l.cur >= len(l.rows) {
				return l, nil
			}
			if r := l.rows[l.cur]; r.mon == nil {
				l.folded[r.group] = !l.folded[r.group]
				l.rebuild()
				return l, nil
			}
			return l, push(newMonitorView(l.ctx, l.rows[l.cur].mon.ID))
		case "m":
			if m := l.selected(); m != nil {
				return l, openModal(newMutePicker(l.ctx, *m))
			}
		case "u":
			if m := l.selected(); m != nil {
				return l, cmdUnmute(l.ctx.api, m.ID, l.muted[m.ID])
			}
		case "o":
			if m := l.selected(); m != nil {
				return l, openURL(l.ctx.api.BrowseURL(fmt.Sprintf("/monitors/%d", m.ID)))
			}
			return l, openURL(l.ctx.api.BrowseURL("/monitors/manage"))
		case "C":
			if m := l.selected(); m != nil {
				return l, cmdInvestigate(l.ctx, monitorInvestigation(l.ctx.site, factsOf(*m, nil)))
			}
		case "r":
			l.loading = true
			return l, fetchMonitors(l.ctx.api)
		}
	}
	return l, nil
}

func factsOf(m datadog.Monitor, groups []datadog.MonitorGroup) monitorFacts {
	f := monitorFacts{id: m.ID, name: m.Name, state: monitorState(m.OverallState), query: m.Query, service: serviceOf(m)}
	var th []string
	for _, k := range []string{"critical", "warning"} {
		if v, ok := m.Options.Thresholds[k]; ok && v != nil {
			th = append(th, fmt.Sprintf("%s %v", k, v))
		}
	}
	f.thresholds = strings.Join(th, ", ")
	for _, g := range groups {
		if st := monitorState(g.Status); st != "OK" {
			f.groups = append(f.groups, g.Name+" ("+st+")")
		}
	}
	return f
}

func (l *monitorList) View() string {
	counts := map[string]int{}
	for _, m := range l.mons {
		counts[monitorState(m.OverallState)]++
	}
	var summary []string
	for _, s := range []string{"Alert", "Warn", "No Data", "OK"} {
		if counts[s] > 0 || s == "Alert" {
			summary = append(summary, stateIcon(s)+" "+fmt.Sprintf("%d %s", counts[s], s))
		}
	}
	muted := 0
	for _, ids := range l.muted {
		if len(ids) > 0 {
			muted++
		}
	}
	if muted > 0 {
		summary = append(summary, sMuted().Render(fmt.Sprintf("%d muted", muted)))
	}
	left := " " + sBold().Render("Monitors") + "   " + strings.Join(summary, "   ")
	filter := sMuted().Render("/ filter")
	if l.editing || l.filter != "" {
		filter = sAccent().Render("/ ") + l.filter
		if l.editing {
			filter += sAccent().Render("▏")
		}
	}
	out := []string{spread(left, filter+" ", l.w), ""}
	switch {
	case l.loading && len(l.mons) == 0:
		out = append(out, "  "+sMuted().Render(spinnerFrames[l.ctx.spin%len(spinnerFrames)]+" Loading monitors…"))
	case l.err != nil && len(l.mons) == 0:
		out = append(out, "  "+fg(th.red).Render("Couldn't load monitors: ")+l.err.Error())
	}
	listH := l.h - len(out)
	if l.cur < l.scroll {
		l.scroll = l.cur
	}
	if l.cur >= l.scroll+listH {
		l.scroll = l.cur - listH + 1
	}
	terms := queryTerms(l.filter)
	kindW, svcW := 8, min(18, max(8, l.w/10))
	for i := l.scroll; i < len(l.rows) && i < l.scroll+listH; i++ {
		r := l.rows[i]
		var row string
		if r.mon == nil {
			arrow := gOpen
			if l.folded[r.group] && len(terms) == 0 {
				arrow = gClosed
			}
			row = " " + sMuted().Render(arrow) + " " + fg(stateColor(r.group)).Bold(true).Render(r.group) + sMuted().Render(fmt.Sprintf("  %d", r.count))
		} else {
			m := r.mon
			mute := "     "
			if len(l.muted[m.ID]) > 0 {
				mute = sMuted().Render("muted")
			}
			nameW := max(10, l.w-kindW-svcW-24)
			row = "   " + stateIcon(m.OverallState) + " " + priorityLabel(m.Priority) + " " +
				fit(highlight(trunc(m.Name, nameW), terms, lipglossStyle(), fg(th.yellow).Bold(true)), nameW) + " " +
				mute + " " + sMuted().Render(fit(monitorKind(m.Type), kindW)) + " " + fg(th.cyan).Render(fit(serviceOf(*m), svcW))
		}
		if i == l.cur {
			row = paintBg(fg(th.accent).Render(gSel)+fit(row[1:], l.w-1), th.sel)
		}
		out = append(out, fit(row, l.w))
	}
	return frame(strings.Join(out, "\n"), l.w, l.h)
}

func (l *monitorList) Hints() []hint {
	return []hint{{"⏎", "open"}, {"/", "filter"}, {"m", "mute"}, {"u", "unmute"}, {"C", "investigate"}, {"o", "browser"}}
}

// ─── one monitor ─────────────────────────────────────────────────

type monitorView struct {
	ctx     *appCtx
	id      int64
	mon     *datadog.Monitor
	groups  []datadog.MonitorGroup
	muted   []string
	chart   *widgetData
	mc      *monitorChart
	err     error
	loading bool
	scroll  int
	w, h    int
}

type monitorDetailMsg struct {
	id     int64
	mon    *datadog.Monitor
	groups []datadog.MonitorGroup
	muted  []string
	chart  *widgetData
	err    error
}

func newMonitorView(ctx *appCtx, id int64) screen {
	return &monitorView{ctx: ctx, id: id, loading: true}
}

func (v *monitorView) Title() string {
	if v.mon != nil {
		return trunc(v.mon.Name, 40)
	}
	return fmt.Sprintf("Monitor %d", v.id)
}

func (v *monitorView) SetSize(w, h int) { v.w, v.h = w, h }

func (v *monitorView) Init() tea.Cmd { return v.load() }

func (v *monitorView) load() tea.Cmd {
	api, id, tr, w := v.ctx.api, v.id, v.ctx.tr, v.w
	return func() tea.Msg {
		m, err := api.GetMonitor(id)
		if err != nil {
			return monitorDetailMsg{id: id, err: err}
		}
		msg := monitorDetailMsg{id: id, mon: m}
		if groups, err := api.GetMonitorGroups(id); err == nil {
			msg.groups = groups
		}
		msg.muted = activeDowntimes(api)[id]
		if mc := parseMonitorQuery(m.Query, m.Options.Thresholds); mc != nil {
			msg.chart = monitorChartData(api, mc, tr, max(40, w))
		}
		return msg
	}
}

// monitorChartData fetches what the monitor watches over the time range.
func monitorChartData(api API, mc *monitorChart, tr timeRange, cols int) *widgetData {
	from, to := tr.bounds()
	if mc.events != nil {
		def := map[string]any{"requests": []any{map[string]any{
			"queries":      []any{mc.events},
			"formulas":     []any{map[string]any{"formula": "a"}},
			"display_type": "bars",
		}}}
		return fetchTimeseries(api, def, nil, from, to, cols)
	}
	d := &widgetData{}
	res, err := api.QueryMetrics(mc.metric, from/1000, to/1000)
	if err != nil {
		d.err = err
		return d
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
			name = s.DisplayName
		}
		d.series = append(d.series, viz.Series{Name: name, Color: pal[i%len(pal)], Points: pts})
		if i == 0 && len(s.Unit) > 0 && s.Unit[0].Name != "" {
			u := s.Unit[0]
			d.unit = viz.Unit{Family: u.Family, Name: u.Name, Short: u.ShortName, Scale: u.ScaleFactor}
		}
	}
	return d
}

func (v *monitorView) Update(msg tea.Msg) (screen, tea.Cmd) {
	switch msg := msg.(type) {
	case monitorDetailMsg:
		if msg.id != v.id {
			return v, nil
		}
		v.loading = false
		v.err = msg.err
		if msg.mon != nil {
			v.mon, v.groups, v.muted, v.chart = msg.mon, msg.groups, msg.muted, msg.chart
			v.mc = parseMonitorQuery(msg.mon.Query, msg.mon.Options.Thresholds)
		}
	case rangeChangedMsg:
		v.loading = true
		return v, v.load()
	case mutedMsg:
		if msg.id == v.id && msg.err == nil {
			return v, v.load()
		}
	case investigatedMsg:
		return v, investigatedToast(msg)
	case tea.KeyMsg:
		switch msg.String() {
		case "down", "j":
			v.scroll++
		case "up", "k":
			v.scroll = max(0, v.scroll-1)
		case "r":
			v.loading = true
			return v, v.load()
		case "o":
			return v, openURL(v.ctx.api.BrowseURL(fmt.Sprintf("/monitors/%d", v.id)))
		case "m":
			if v.mon != nil {
				return v, openModal(newMutePicker(v.ctx, *v.mon))
			}
		case "u":
			return v, cmdUnmute(v.ctx.api, v.id, v.muted)
		case "C":
			if v.mon != nil {
				return v, cmdInvestigate(v.ctx, monitorInvestigation(v.ctx.site, factsOf(*v.mon, v.groups)))
			}
		}
	}
	return v, nil
}

func (v *monitorView) View() string {
	if v.mon == nil {
		switch {
		case v.err != nil:
			return frame("\n  "+fg(th.red).Render("Couldn't load the monitor: ")+v.err.Error(), v.w, v.h)
		default:
			return frame("\n  "+sMuted().Render(spinnerFrames[v.ctx.spin%len(spinnerFrames)]+" Loading monitor…"), v.w, v.h)
		}
	}
	m := v.mon
	st := monitorState(m.OverallState)
	var out []string
	title := " " + pill(st, stateColor(st)) + " " + sBold().Render(m.Name)
	var meta []string
	if p := priorityLabel(m.Priority); strings.TrimSpace(p) != "" {
		meta = append(meta, p)
	}
	meta = append(meta, sMuted().Render(monitorKind(m.Type)), sMuted().Render(fmt.Sprintf("#%d", m.ID)))
	if len(v.muted) > 0 {
		meta = append(meta, fg(th.yellow).Render("muted"))
	}
	if v.loading {
		meta = append(meta, sMuted().Render(spinnerFrames[v.ctx.spin%len(spinnerFrames)]))
	}
	out = append(out, spread(title, strings.Join(meta, " "+sMuted().Render(gDot)+" ")+" ", v.w))
	if len(m.Tags) > 0 {
		out = append(out, " "+sMuted().Render(trunc(strings.Join(m.Tags, "  "), v.w-2)))
	}
	out = append(out, "")

	// The chart of what it watches, thresholds included.
	chartH := max(8, min(18, v.h*2/5))
	switch {
	case v.mc == nil:
		out = append(out, "  "+sMuted().Render("This monitor type has no chart — o opens it in Datadog."))
	case v.chart == nil:
		out = append(out, "  "+sMuted().Render("Loading the chart…"))
	case v.chart.err != nil:
		out = append(out, "  "+fg(th.red).Render("Chart: ")+trunc(v.chart.err.Error(), v.w-12))
	default:
		var markers []viz.Marker
		if v.mc.critical != nil {
			markers = append(markers, viz.Marker{Value: *v.mc.critical, Color: th.red, Label: "critical"})
		}
		if v.mc.warning != nil {
			markers = append(markers, viz.Marker{Value: *v.mc.warning, Color: th.yellow, Label: "warning"})
		}
		c := viz.Chart{Width: v.w - 3, Height: chartH, Series: v.chart.series, Kind: v.chart.kind, Style: v.ctx.style,
			Unit: v.chart.unit, Markers: markers, FitMarkers: true, Cursor: -1, Axis: th.muted}
		c.From, c.To = v.ctx.tr.bounds()
		for _, l := range c.Render() {
			out = append(out, "  "+l)
		}
		legend := ""
		if v.mc.critical != nil {
			legend += fg(th.red).Render("╌ critical "+viz.Format(*v.mc.critical, v.chart.unit)) + "   "
		}
		if v.mc.warning != nil {
			legend += fg(th.yellow).Render("╌ warning "+viz.Format(*v.mc.warning, v.chart.unit)) + "   "
		}
		if v.mc.window != "" {
			legend += sMuted().Render("evaluated over " + strings.TrimPrefix(v.mc.window, "last_"))
		}
		out = append(out, "  "+legend)
	}
	out = append(out, "")

	// Details: groups on the right when wide, below otherwise.
	left := v.detailLines(min(v.w-4, max(40, v.w*3/5)))
	right := v.groupLines(max(30, v.w-len(left)-4))
	if v.w >= 120 && len(v.groups) > 1 {
		lw := v.w * 3 / 5
		for i := 0; i < max(len(left), len(right)); i++ {
			l, r := "", ""
			if i < len(left) {
				l = left[i]
			}
			if i < len(right) {
				r = right[i]
			}
			out = append(out, fit(l, lw)+"  "+r)
		}
	} else {
		out = append(out, left...)
		if len(v.groups) > 1 {
			out = append(out, "")
			out = append(out, right...)
		}
	}
	if v.scroll > 0 {
		head := min(3, len(out))
		body := out[head:]
		out = append(out[:head:head], body[min(v.scroll, max(0, len(body)-1)):]...)
	}
	return frame(strings.Join(out, "\n"), v.w, v.h)
}

func (v *monitorView) detailLines(width int) []string {
	m := v.mon
	var out []string
	out = append(out, "  "+sBold().Render("Query"))
	for _, l := range wrapPlain(m.Query, width-4) {
		out = append(out, "    "+fg(th.cyan).Render(l))
	}
	out = append(out, "", "  "+sBold().Render("Message"))
	msg := cleanMonitorMessage(m.Message)
	if msg == "" {
		out = append(out, "    "+sMuted().Render("(none)"))
	}
	for _, l := range noteLines(msg, width-2, 40) {
		out = append(out, "   "+l)
	}
	return out
}

var reTemplateBlock = regexp.MustCompile(`\{\{\s*[#/^][^}]*\}\}`)

// cleanMonitorMessage hides template noise: {{#is_alert}} blocks and the
// like stay readable as plain text; @handles are kept (who gets paged).
func cleanMonitorMessage(s string) string {
	s = reTemplateBlock.ReplaceAllString(s, "")
	var lines []string
	for _, l := range strings.Split(s, "\n") {
		if strings.TrimSpace(l) != "" || (len(lines) > 0 && lines[len(lines)-1] != "") {
			lines = append(lines, strings.TrimRight(l, " "))
		}
	}
	return strings.TrimSpace(strings.Join(lines, "\n"))
}

func (v *monitorView) groupLines(width int) []string {
	if len(v.groups) <= 1 {
		return nil
	}
	gs := append([]datadog.MonitorGroup(nil), v.groups...)
	sort.SliceStable(gs, func(i, j int) bool {
		if stateRank(gs[i].Status) != stateRank(gs[j].Status) {
			return stateRank(gs[i].Status) < stateRank(gs[j].Status)
		}
		return gs[i].Name < gs[j].Name
	})
	out := []string{"  " + sBold().Render(fmt.Sprintf("Groups  %d", len(gs)))}
	for i, g := range gs {
		if i == 30 {
			out = append(out, sMuted().Render(fmt.Sprintf("    … %d more", len(gs)-30)))
			break
		}
		when := ""
		if g.LastTriggeredTS > 0 {
			when = sMuted().Render(age(time.Unix(g.LastTriggeredTS, 0)))
		}
		out = append(out, "  "+stateIcon(g.Status)+" "+fit(g.Name, max(10, width-12))+" "+fitRight(when, 5))
	}
	return out
}

func (v *monitorView) Hints() []hint {
	return []hint{{"m", "mute"}, {"u", "unmute"}, {"C", "investigate"}, {"o", "browser"}, {"r", "refresh"}, {"esc", "back"}}
}
