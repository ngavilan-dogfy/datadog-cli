package tui

import (
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/ngavilan-dogfy/datadog-cli/datadog"
	"github.com/ngavilan-dogfy/datadog-cli/viz"

	tea "github.com/charmbracelet/bubbletea"
)

// home ("Now") answers "is anything wrong right now?" in one screen: what's
// alerting, incidents, SLOs at risk, which services log errors, and what
// changed recently. Everything loads in parallel; enter opens the monitor.

type home struct {
	ctx     *appCtx
	data    *homeData
	loading bool
	cur     int
	w, h    int
}

type homeData struct {
	at        time.Time
	mons      []datadog.Monitor
	muted     map[int64][]string
	monErr    error
	incidents []datadog.IncidentData
	incErr    error
	slos      []datadog.SLO
	sloErr    error
	errsBySvc []scalarRow
	errTotal  float64
	logErr    error
	events    []datadog.Event
	evErr     error
}

type homeMsg struct{ d *homeData }
type homeTickMsg struct{}

func newHome(ctx *appCtx) screen { return &home{ctx: ctx, loading: true} }

func (h *home) Title() string     { return "Now" }
func (h *home) SetSize(w, hh int) { h.w, h.h = w, hh }

func (h *home) Init() tea.Cmd {
	return tea.Batch(h.load(), tickFn(60*time.Second, func(time.Time) tea.Msg { return homeTickMsg{} }))
}

func (h *home) load() tea.Cmd {
	api := h.ctx.api
	return func() tea.Msg {
		d := &homeData{at: now()}
		var wg sync.WaitGroup
		run := func(fn func()) {
			wg.Add(1)
			go func() { defer wg.Done(); fn() }()
		}
		run(func() {
			d.mons, d.monErr = api.ListMonitors("", 1000)
			d.muted = activeDowntimes(api)
		})
		run(func() { d.incidents, d.incErr = api.ListIncidents() })
		run(func() { d.slos, d.sloErr = api.ListSLOs("") })
		run(func() {
			res, err := api.AggregateLogs("status:error", "now-15m", "now", []string{"service"}, 8)
			if err != nil {
				d.logErr = err
				return
			}
			for _, b := range res.Data.Buckets {
				n := 0.0
				for _, v := range b.Computes {
					n = num(v)
				}
				d.errTotal += n
				d.errsBySvc = append(d.errsBySvc, scalarRow{label: b.By["service"], value: n})
			}
			sort.SliceStable(d.errsBySvc, func(i, j int) bool { return d.errsBySvc[i].value > d.errsBySvc[j].value })
		})
		run(func() {
			end := now()
			d.events, d.evErr = api.ListEvents(end.Add(-6*time.Hour).Unix(), end.Unix(), "")
		})
		wg.Wait()
		return homeMsg{d}
	}
}

// attention lists monitors that aren't OK, worst first.
func (h *home) attention() []datadog.Monitor {
	if h.data == nil {
		return nil
	}
	var out []datadog.Monitor
	for _, m := range h.data.mons {
		if stateRank(m.OverallState) < 3 {
			out = append(out, m)
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if stateRank(out[i].OverallState) != stateRank(out[j].OverallState) {
			return stateRank(out[i].OverallState) < stateRank(out[j].OverallState)
		}
		return byPriority(out[i], out[j])
	})
	return out
}

func (h *home) Update(msg tea.Msg) (screen, tea.Cmd) {
	switch msg := msg.(type) {
	case homeMsg:
		h.data, h.loading = msg.d, false
		if msg.d.mons != nil {
			h.ctx.monitors = msg.d.mons
		}
	case homeTickMsg:
		return h, tea.Batch(h.load(), tickFn(60*time.Second, func(time.Time) tea.Msg { return homeTickMsg{} }))
	case mutedMsg:
		if msg.err == nil {
			return h, h.load()
		}
	case investigatedMsg:
		return h, investigatedToast(msg)
	case tea.KeyMsg:
		att := h.attention()
		switch msg.String() {
		case "down", "j":
			h.cur = min(len(att)-1, h.cur+1)
		case "up", "k":
			h.cur = max(0, h.cur-1)
		case "enter", "l":
			if h.cur < len(att) {
				return h, push(newMonitorView(h.ctx, att[h.cur].ID))
			}
		case "m":
			if h.cur < len(att) {
				return h, openModal(newMutePicker(h.ctx, att[h.cur]))
			}
		case "C":
			if h.cur < len(att) {
				return h, cmdInvestigate(h.ctx, monitorInvestigation(h.ctx.site, factsOf(att[h.cur], nil)))
			}
		case "r":
			h.loading = true
			return h, h.load()
		case "e":
			return h, switchTab(3)
		}
	}
	return h, nil
}

func (h *home) View() string {
	if h.data == nil {
		return frame("\n  "+sMuted().Render(spinnerFrames[h.ctx.spin%len(spinnerFrames)]+" Checking what's going on…"), h.w, h.h)
	}
	d := h.data
	counts := map[string]int{}
	for _, m := range d.mons {
		counts[monitorState(m.OverallState)]++
	}
	activeInc := 0
	for _, i := range d.incidents {
		if strings.ToLower(i.Attributes.Status) != "resolved" {
			activeInc++
		}
	}
	atRisk := 0
	for _, s := range d.slos {
		for _, st := range s.OverallStatus {
			if st.Status == "WARNING" || st.Status == "BREACHED" {
				atRisk++
				break
			}
		}
	}

	// Tiles: the numbers that matter, colored only when they're bad.
	tile := func(label string, n float64, bad lipglossColor, err error) []string {
		text := viz.Format(n, viz.Unit{})
		c := lipglossColor(th.green)
		if n > 0 {
			c = bad
		}
		if err != nil {
			text, c = "?", th.muted
		}
		big, _ := viz.BigText(text, c)
		return append([]string{sMuted().Render(label)}, big...)
	}
	tiles := [][]string{
		tile("Alerting", float64(counts["Alert"]), th.red, d.monErr),
		tile("Warning", float64(counts["Warn"]), th.yellow, d.monErr),
		tile("No data", float64(counts["No Data"]), th.magenta, d.monErr),
		tile("Incidents", float64(activeInc), th.red, d.incErr),
		tile("SLOs at risk", float64(atRisk), th.yellow, d.sloErr),
		tile("Error logs · 15m", d.errTotal, th.red, d.logErr),
	}
	tileW := max(14, (h.w-2)/len(tiles))
	var out []string
	for r := 0; r < 4; r++ {
		line := " "
		for _, t := range tiles {
			line += fit(" "+t[r], tileW)
		}
		out = append(out, fit(line, h.w))
	}
	status := sMuted().Render("updated " + age(d.at))
	if h.loading {
		status = sMuted().Render(spinnerFrames[h.ctx.spin%len(spinnerFrames)] + " refreshing")
	}
	out = append(out, spread("", status+" ", h.w), fit(sFaint().Render(strings.Repeat(gRule, h.w)), h.w))

	// Two columns: what needs attention · errors by service.
	leftW := h.w * 3 / 5
	rightW := h.w - leftW - 2
	var left, right []string
	att := h.attention()
	left = append(left, " "+sBold().Render("Needs attention")+sMuted().Render(fmt.Sprintf("  %d", len(att))))
	if len(att) == 0 && d.monErr == nil {
		left = append(left, "   "+fg(th.green).Render("● Every monitor is OK"))
	}
	if d.monErr != nil {
		left = append(left, "   "+fg(th.red).Render("monitors: "+trunc(d.monErr.Error(), leftW-14)))
	}
	listRows := max(3, h.h-len(out)-10)
	if h.cur >= len(att) {
		h.cur = max(0, len(att)-1)
	}
	start := max(0, h.cur-listRows+1)
	for i := start; i < len(att) && i < start+listRows; i++ {
		m := att[i]
		mute := ""
		if len(d.muted[m.ID]) > 0 {
			mute = sMuted().Render(" muted")
		}
		row := "   " + stateIcon(m.OverallState) + " " + priorityLabel(m.Priority) + " " + trunc(m.Name, leftW-12-sw(mute)) + mute
		if i == h.cur {
			row = paintBg(fg(th.accent).Render(gSel)+fit(row[1:], leftW-1), th.sel)
		}
		left = append(left, fit(row, leftW))
	}
	right = append(right, sBold().Render("Errors by service")+sMuted().Render(" · 15m"))
	switch {
	case d.logErr != nil:
		right = append(right, fg(th.red).Render(trunc(d.logErr.Error(), rightW)))
	case len(d.errsBySvc) == 0:
		right = append(right, fg(th.green).Render("● No error logs"))
	default:
		maxV := d.errsBySvc[0].value
		for _, r := range d.errsBySvc {
			label := r.label
			if label == "" {
				label = "(no service)"
			}
			right = append(right, fit(label, 16)+" "+viz.HBar(r.value, maxV, max(4, rightW-26), th.red)+" "+fitRight(viz.Format(r.value, viz.Unit{}), 6))
		}
	}
	for i := 0; i < max(len(left), len(right)); i++ {
		l, r := "", ""
		if i < len(left) {
			l = left[i]
		}
		if i < len(right) {
			r = right[i]
		}
		out = append(out, fit(l, leftW)+"  "+fit(r, rightW))
	}

	// Recent events: deploys, config changes, alerts — repeats folded into
	// one line ("23 × gcp_bigquery_table … was updated").
	if room := h.h - len(out) - 2; room > 2 && len(d.events) > 0 {
		out = append(out, "", " "+sBold().Render("Recent events")+sMuted().Render(" · 6h"))
		for i, g := range groupEvents(d.events) {
			if i == room-1 {
				break
			}
			c := th.muted
			switch g.alertType {
			case "error":
				c = th.red
			case "warning":
				c = th.yellow
			case "success":
				c = th.green
			}
			title := g.title
			if g.count > 1 {
				title = sMuted().Render(fmt.Sprintf("%d × ", g.count)) + g.pattern
			}
			out = append(out, "   "+sMuted().Render(fitRight(age(time.Unix(g.latest, 0)), 4))+" "+fg(c).Render("●")+" "+
				trunc(title, h.w-30)+" "+sMuted().Render(trunc(g.source, 16)))
		}
	}
	return frame(strings.Join(out, "\n"), h.w, h.h)
}

func (h *home) Hints() []hint {
	return []hint{{"⏎", "open"}, {"m", "mute"}, {"C", "investigate"}, {"e", "error logs"}, {"r", "refresh"}}
}

type eventGroup struct {
	title, pattern, source, alertType string
	count                             int
	latest                            int64
}

// groupEvents folds events that differ only in their middle words, newest
// group first: integrations can post dozens of near-identical events.
func groupEvents(evs []datadog.Event) []eventGroup {
	var groups []*eventGroup
	byKey := map[string]*eventGroup{}
	sorted := append([]datadog.Event(nil), evs...)
	sort.SliceStable(sorted, func(i, j int) bool { return sorted[i].DateHappened > sorted[j].DateHappened })
	for _, e := range sorted {
		words := strings.Fields(e.Title)
		key, pattern := e.Source+"|"+e.Title, strings.Join(words, " ")
		if len(words) >= 4 {
			tail := strings.Join(words[len(words)-2:], " ")
			key = e.Source + "|" + words[0] + "|" + tail
			pattern = words[0] + " " + gEllipsis + " " + tail
		}
		if g, ok := byKey[key]; ok {
			g.count++
			continue
		}
		g := &eventGroup{title: strings.Join(words, " "), pattern: pattern, source: e.Source, alertType: e.AlertType, count: 1, latest: e.DateHappened}
		byKey[key] = g
		groups = append(groups, g)
	}
	out := make([]eventGroup, len(groups))
	for i, g := range groups {
		out[i] = *g
	}
	return out
}
