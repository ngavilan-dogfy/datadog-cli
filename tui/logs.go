package tui

import (
	"encoding/json"
	"fmt"
	"math"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/ngavilan-dogfy/datadog-cli/datadog"
	"github.com/ngavilan-dogfy/datadog-cli/viz"

	tea "github.com/charmbracelet/bubbletea"
)

// logsView is a log explorer: a query, a histogram of volume by status,
// the matching logs and one log's detail — with a live tail.
type logsView struct {
	ctx     *appCtx
	query   string
	draft   string
	editing bool
	logs    []datadog.LogData
	after   string // cursor for the next page
	hist    *widgetData
	err     error
	loading bool
	more    bool
	live    bool
	gen     int // query generation, so stale answers are dropped
	cur     int
	scroll  int
	detail  bool
	dscroll int
	w, h    int
}

type logsMsg struct {
	gen    int
	logs   []datadog.LogData
	after  string
	append bool
	err    error
}

type logsHistMsg struct {
	gen int
	d   *widgetData
}

type logsLiveMsg struct {
	gen  int
	logs []datadog.LogData
}

type logsTickMsg struct{ gen int }

func newLogs(ctx *appCtx, q string) screen {
	if q == "" {
		q = "status:error"
	}
	return &logsView{ctx: ctx, query: q, loading: true}
}

func (v *logsView) Title() string    { return "Logs" }
func (v *logsView) Typing() bool     { return v.editing }
func (v *logsView) SetSize(w, h int) { v.w, v.h = w, h }

func (v *logsView) Init() tea.Cmd { return v.run() }

// run starts a new search: the list and the histogram.
func (v *logsView) run() tea.Cmd {
	v.gen++
	v.loading, v.err, v.cur, v.scroll, v.after = true, nil, 0, 0, ""
	api, q, gen, tr, cols := v.ctx.api, v.query, v.gen, v.ctx.tr, max(20, v.w-8)
	from, to := tr.bounds()
	fetch := func() tea.Msg {
		resp, err := api.SearchLogsCursor(q, strconv.FormatInt(from, 10), strconv.FormatInt(to, 10), 100, "")
		if err != nil {
			return logsMsg{gen: gen, err: err}
		}
		return logsMsg{gen: gen, logs: resp.Data, after: resp.Meta.Page.After}
	}
	hist := func() tea.Msg {
		def := map[string]any{"requests": []any{map[string]any{
			"display_type": "bars",
			"queries": []any{map[string]any{
				"data_source": "logs", "name": "a", "indexes": []any{"*"},
				"compute": map[string]any{"aggregation": "count"}, "search": map[string]any{"query": q},
				"group_by": []any{map[string]any{"facet": "status", "limit": 6, "sort": map[string]any{"aggregation": "count", "order": "desc"}}},
			}},
			"formulas": []any{map[string]any{"formula": "a"}},
		}}}
		return logsHistMsg{gen: gen, d: limited(func() *widgetData { return fetchTimeseries(api, def, nil, from, to, cols) })}
	}
	return tea.Batch(fetch, hist)
}

func (v *logsView) loadMore() tea.Cmd {
	if v.after == "" || v.more {
		return nil
	}
	v.more = true
	api, q, gen, after := v.ctx.api, v.query, v.gen, v.after
	from, to := v.ctx.tr.bounds()
	return func() tea.Msg {
		resp, err := api.SearchLogsCursor(q, strconv.FormatInt(from, 10), strconv.FormatInt(to, 10), 100, after)
		if err != nil {
			return logsMsg{gen: gen, err: err, append: true}
		}
		return logsMsg{gen: gen, logs: resp.Data, after: resp.Meta.Page.After, append: true}
	}
}

func (v *logsView) liveTick() tea.Cmd {
	gen := v.gen
	return tickFn(3*time.Second, func(time.Time) tea.Msg { return logsTickMsg{gen} })
}

// poll fetches what arrived since the newest log shown.
func (v *logsView) poll() tea.Cmd {
	api, q, gen := v.ctx.api, v.query, v.gen
	since := now().Add(-2 * time.Minute)
	if len(v.logs) > 0 {
		if t, err := time.Parse(time.RFC3339Nano, v.logs[0].Attributes.Timestamp); err == nil {
			since = t.Add(time.Millisecond)
		}
	}
	return func() tea.Msg {
		resp, err := api.SearchLogs(q, strconv.FormatInt(since.UnixMilli(), 10), "now", 100)
		if err != nil {
			return logsLiveMsg{gen: gen}
		}
		return logsLiveMsg{gen: gen, logs: resp.Data}
	}
}

func (v *logsView) Update(msg tea.Msg) (screen, tea.Cmd) {
	switch msg := msg.(type) {
	case logsMsg:
		if msg.gen != v.gen {
			return v, nil
		}
		v.loading, v.more = false, false
		if msg.err != nil {
			v.err = msg.err
			return v, nil
		}
		if msg.append {
			v.logs = append(v.logs, msg.logs...)
		} else {
			v.logs = msg.logs
		}
		v.after = msg.after
	case logsHistMsg:
		if msg.gen == v.gen {
			v.hist = msg.d
			colorByStatus(v.hist)
		}
	case logsTickMsg:
		if msg.gen == v.gen && v.live {
			return v, tea.Batch(v.poll(), v.liveTick())
		}
	case logsLiveMsg:
		if msg.gen != v.gen || len(msg.logs) == 0 {
			return v, nil
		}
		seen := map[string]bool{}
		for _, l := range v.logs {
			seen[l.ID] = true
		}
		var fresh []datadog.LogData
		for _, l := range msg.logs {
			if !seen[l.ID] {
				fresh = append(fresh, l)
			}
		}
		if len(fresh) == 0 {
			return v, nil
		}
		v.logs = append(fresh, v.logs...)
		if v.cur > 0 || v.detail {
			v.cur += len(fresh) // keep the selected log selected
		}
		if len(v.logs) > 2000 {
			v.logs = v.logs[:2000]
		}
	case rangeChangedMsg:
		return v, v.run()
	case investigatedMsg:
		return v, investigatedToast(msg)
	case tea.KeyMsg:
		return v.key(msg)
	}
	return v, nil
}

func (v *logsView) key(k tea.KeyMsg) (screen, tea.Cmd) {
	if v.editing {
		switch k.String() {
		case "esc":
			v.editing = false
		case "enter":
			v.editing = false
			v.query = strings.TrimSpace(v.draft)
			if v.query == "" {
				v.query = "*"
			}
			return v, v.run()
		case "backspace":
			if r := []rune(v.draft); len(r) > 0 {
				v.draft = string(r[:len(r)-1])
			}
		case "ctrl+u":
			v.draft = ""
		case "ctrl+w":
			d := strings.TrimRight(v.draft, " ")
			if i := strings.LastIndexByte(d, ' '); i >= 0 {
				v.draft = d[:i+1]
			} else {
				v.draft = ""
			}
		default:
			if k.Type == tea.KeyRunes || k.String() == " " {
				v.draft += string(k.Runes)
				if k.String() == " " && len(k.Runes) == 0 {
					v.draft += " "
				}
			}
		}
		return v, nil
	}
	switch k.String() {
	case "/", "e":
		v.editing, v.draft = true, v.query
	case "down", "j":
		if v.detail {
			v.dscroll++
			return v, nil
		}
		v.cur = min(len(v.logs)-1, v.cur+1)
		if v.cur >= len(v.logs)-10 {
			return v, v.loadMore()
		}
	case "up", "k":
		if v.detail {
			v.dscroll = max(0, v.dscroll-1)
			return v, nil
		}
		v.cur = max(0, v.cur-1)
	case "J", "n":
		if v.detail {
			v.cur, v.dscroll = min(len(v.logs)-1, v.cur+1), 0
			return v, nil
		}
		return v, v.loadMore()
	case "K", "p":
		if v.detail {
			v.cur, v.dscroll = max(0, v.cur-1), 0
		}
	case "g", "home":
		v.cur = 0
	case "G", "end":
		v.cur = max(0, len(v.logs)-1)
		return v, v.loadMore()
	case "enter", "l":
		v.detail, v.dscroll = !v.detail, 0
	case "esc":
		if v.detail {
			v.detail = false
		}
	case "L":
		v.live = !v.live
		if v.live {
			return v, tea.Batch(v.poll(), v.liveTick(), toast("Live tail on — new logs every 3 s", toastInfo))
		}
		return v, toast("Live tail paused", toastInfo)
	case "s":
		if l := v.selected(); l != nil && l.Attributes.Service != "" {
			v.query = addFilter(v.query, "service:"+l.Attributes.Service)
			return v, v.run()
		}
	case "S":
		if l := v.selected(); l != nil && l.Attributes.Status != "" {
			v.query = addFilter(v.query, "status:"+l.Attributes.Status)
			return v, v.run()
		}
	case "r":
		return v, v.run()
	case "o":
		from, to := v.ctx.tr.bounds()
		return v, openURL(v.ctx.api.BrowseURL("/logs?query=" + url.QueryEscape(v.query) +
			"&from_ts=" + strconv.FormatInt(from, 10) + "&to_ts=" + strconv.FormatInt(to, 10) + "&live=false"))
	case "C":
		if l := v.selected(); l != nil {
			a := l.Attributes
			when := a.Timestamp
			if t, err := time.Parse(time.RFC3339Nano, a.Timestamp); err == nil {
				when = t.Local().Format("2006-01-02 15:04:05")
			}
			var attrs []string
			for _, kv := range flattenAttrs(a.Attributes) {
				if len(attrs) == 12 {
					break
				}
				attrs = append(attrs, kv[0]+"="+kv[1])
			}
			return v, cmdInvestigate(v.ctx, logInvestigation(v.ctx.site, a.Service, a.Status, when, a.Message, attrs))
		}
	}
	return v, nil
}

// addFilter adds a term to a query unless it's already there.
func addFilter(q, term string) string {
	if strings.Contains(" "+q+" ", " "+term+" ") {
		return q
	}
	if q == "" || q == "*" {
		return term
	}
	return q + " " + term
}

func (v *logsView) selected() *datadog.LogData {
	if v.cur >= 0 && v.cur < len(v.logs) {
		return &v.logs[v.cur]
	}
	return nil
}

// colorByStatus paints histogram layers with the status colors.
func colorByStatus(d *widgetData) {
	if d == nil {
		return
	}
	order := map[string]int{"error": 0, "critical": 0, "emergency": 0, "alert": 0, "warn": 1, "warning": 1, "notice": 2, "info": 3, "ok": 4, "debug": 5}
	for i := range d.series {
		st := strings.TrimPrefix(d.series[i].Name, "status:")
		d.series[i].Name = st
		d.series[i].Color = logStatusColor(st)
	}
	sort.SliceStable(d.series, func(i, j int) bool { return order[d.series[i].Name] < order[d.series[j].Name] })
}

func (v *logsView) View() string {
	var out []string
	q := v.query
	if v.editing {
		q = v.draft + sAccent().Render("▏")
	}
	label := sBold().Render("Logs") + "  " + sAccent().Render("› ") + q
	right := ""
	switch {
	case v.live:
		right = fg(th.green).Render("● live") + "  "
	}
	if v.loading {
		right += sMuted().Render(spinnerFrames[v.ctx.spin%len(spinnerFrames)]+" searching") + " "
	} else if v.err == nil {
		n := fmt.Sprintf("%d", len(v.logs))
		if v.after != "" {
			n += "+"
		}
		right += sMuted().Render(n+" logs") + " "
	}
	out = append(out, spread(" "+label, right, v.w))

	// Volume by status.
	histH := 6
	if v.h < 24 {
		histH = 4
	}
	switch {
	case v.hist == nil:
		out = append(out, blank(histH)...)
	case v.hist.err != nil:
		out = append(out, "  "+sMuted().Render("histogram: "+trunc(v.hist.err.Error(), v.w-16)))
		out = append(out, blank(histH-1)...)
	default:
		from, to := v.ctx.tr.bounds()
		c := viz.Chart{Width: v.w - 3, Height: histH, Series: v.hist.series, Kind: viz.Bars, From: from, To: to,
			Cursor: -1, Axis: th.muted}
		for _, l := range c.Render() {
			out = append(out, "  "+l)
		}
	}
	out = append(out, fit(sFaint().Render(strings.Repeat(gRule, v.w)), v.w))

	if v.err != nil {
		out = append(out, "", "  "+fg(th.red).Render("Search failed: ")+v.err.Error(),
			"  "+sMuted().Render("Check the query syntax — press / to edit it."))
		return frame(strings.Join(out, "\n"), v.w, v.h)
	}
	if !v.loading && len(v.logs) == 0 {
		out = append(out, "", "  "+sMuted().Render("No logs match in "+strings.ToLower(v.ctx.tr.label())+"."),
			"  "+sMuted().Render("Widen the time range with t, or edit the query with /."))
		return frame(strings.Join(out, "\n"), v.w, v.h)
	}
	listH := v.h - len(out)
	var detail []string
	if v.detail {
		listH = max(5, listH*2/5)
		detail = v.detailLines(v.h - len(out) - listH - 1)
	}
	if v.cur < v.scroll {
		v.scroll = v.cur
	}
	if v.cur >= v.scroll+listH {
		v.scroll = v.cur - listH + 1
	}
	span := v.ctx.tr.span
	for i := v.scroll; i < len(v.logs) && i < v.scroll+listH; i++ {
		row := logRow(v.logs[i], v.w, span)
		if i == v.cur {
			row = paintBg(fg(th.accent).Render(gSel)+fit(row[1:], v.w-1), th.sel)
		}
		out = append(out, fit(row, v.w))
	}
	if v.detail {
		for len(out) < v.h-len(detail)-1 {
			out = append(out, "")
		}
		out = append(out, fit(sFaint().Render(strings.Repeat(gRule, v.w)), v.w))
		out = append(out, detail...)
	}
	return frame(strings.Join(out, "\n"), v.w, v.h)
}

func blank(n int) []string { return make([]string, max(0, n)) }

func logRow(l datadog.LogData, width int, span time.Duration) string {
	a := l.Attributes
	ts := ""
	if t, err := time.Parse(time.RFC3339Nano, a.Timestamp); err == nil {
		if span > 24*time.Hour {
			ts = t.Local().Format("Jan 02 15:04:05")
		} else {
			ts = t.Local().Format("15:04:05.000")
		}
	}
	msg := strings.Join(strings.Fields(a.Message), " ")
	if msg == "" {
		if m, ok := a.Attributes["message"].(string); ok {
			msg = m
		}
	}
	svc := fit(a.Service, 14)
	host := ""
	if width > 150 {
		host = sMuted().Render(fit(a.Host, 22)) + " "
	}
	left := " " + sMuted().Render(ts) + " " + fg(logStatusColor(a.Status)).Render(fit(strings.ToUpper(orDash(a.Status)), 5)) +
		" " + fg(th.cyan).Render(svc) + " " + host
	return left + trunc(msg, max(1, width-sw(left)))
}

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

func (v *logsView) detailLines(h int) []string {
	l := v.selected()
	if l == nil || h <= 0 {
		return nil
	}
	a := l.Attributes
	var out []string
	ts := a.Timestamp
	if t, err := time.Parse(time.RFC3339Nano, a.Timestamp); err == nil {
		ts = t.Local().Format("Mon Jan 2 15:04:05.000") + " " + gDot + " " + ago(t)
	}
	out = append(out, " "+fg(logStatusColor(a.Status)).Bold(true).Render(strings.ToUpper(orDash(a.Status)))+"  "+
		fg(th.cyan).Render(a.Service)+"  "+sMuted().Render(a.Host)+"  "+sMuted().Render(ts))
	for _, line := range wrapPlain(a.Message, v.w-4) {
		out = append(out, "  "+line)
	}
	if len(a.Tags) > 0 {
		out = append(out, "", " "+sMuted().Render(trunc(strings.Join(a.Tags, "  "), v.w-2)))
	}
	kvs := flattenAttrs(a.Attributes)
	if len(kvs) > 0 {
		out = append(out, "")
		keyW := 0
		for _, kv := range kvs {
			keyW = max(keyW, sw(kv[0]))
		}
		keyW = min(keyW, 36)
		pad := strings.Repeat(" ", keyW+4)
		for _, kv := range kvs {
			lines := strings.Split(strings.TrimRight(kv[1], "\n"), "\n")
			if len(lines) > 16 { // stack traces: the top is what matters
				lines = append(lines[:15], sMuted().Render(fmt.Sprintf("… %d more lines", len(lines)-15)))
			}
			for i, l := range lines {
				l = strings.TrimRight(l, " \t")
				if i == 0 {
					out = append(out, "  "+sAccent().Render(fit(kv[0], keyW))+"  "+trunc(l, v.w-keyW-6))
				} else {
					out = append(out, pad+trunc(strings.TrimLeft(l, " \t"), v.w-keyW-6))
				}
			}
		}
	}
	start := min(v.dscroll, max(0, len(out)-h))
	out = out[start:]
	if len(out) > h {
		out = out[:h]
	}
	return out
}

// flattenAttrs turns nested attributes into sorted "a.b.c: value" pairs.
func flattenAttrs(m map[string]any) [][2]string {
	var out [][2]string
	var walk func(prefix string, v any)
	walk = func(prefix string, v any) {
		switch x := v.(type) {
		case map[string]any:
			keys := make([]string, 0, len(x))
			for k := range x {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			for _, k := range keys {
				p := k
				if prefix != "" {
					p = prefix + "." + k
				}
				walk(p, x[k])
			}
		case []any:
			b, _ := json.Marshal(x)
			out = append(out, [2]string{prefix, string(b)})
		case float64:
			if x == math.Trunc(x) && math.Abs(x) < 1e15 {
				out = append(out, [2]string{prefix, strconv.FormatInt(int64(x), 10)})
			} else {
				out = append(out, [2]string{prefix, strconv.FormatFloat(x, 'g', -1, 64)})
			}
		case nil:
			out = append(out, [2]string{prefix, "null"})
		default:
			out = append(out, [2]string{prefix, fmt.Sprint(x)})
		}
	}
	walk("", m)
	return out
}

func (v *logsView) Hints() []hint {
	if v.editing {
		return []hint{{"⏎", "search"}, {"esc", "cancel"}, {"ctrl+u", "clear"}}
	}
	if v.detail {
		return []hint{{"jk", "scroll"}, {"J/K", "next/prev log"}, {"C", "investigate"}, {"esc", "close"}}
	}
	return []hint{{"/", "query"}, {"⏎", "detail"}, {"L", "live"}, {"s", "only this service"}, {"n", "more"}, {"C", "investigate"}, {"o", "browser"}}
}
