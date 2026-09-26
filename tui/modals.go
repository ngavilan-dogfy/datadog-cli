package tui

import (
	"strings"
	"time"

	"github.com/ngavilan-dogfy/datadog-cli/viz"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// box draws a modal: a titled panel with a thin border.
func box(title string, lines []string, width int) string {
	inner := make([]string, 0, len(lines)+2)
	inner = append(inner, " "+sBold().Render(title), "")
	for _, l := range lines {
		inner = append(inner, " "+fit(l, width-2))
	}
	return lipgloss.NewStyle().Border(lipgloss.NormalBorder()).BorderForeground(th.faint).
		Width(width).Render(strings.Join(inner, "\n"))
}

// ─── pick list (shared by the pickers) ───────────────────────────

type pickItem struct {
	label, right string
	value        string
}

type pickList struct {
	title   string
	items   []pickItem
	shown   []pickItem
	cur     int
	query   string
	typed   bool // allow a free-text value (Enter on the query itself)
	onPick  func(value string) tea.Cmd
	width   int
	maxRows int
}

func (p *pickList) filter() {
	terms := queryTerms(p.query)
	p.shown = p.shown[:0]
	for _, it := range p.items {
		hay := fold(it.label + " " + it.right + " " + it.value)
		ok := true
		for _, t := range terms {
			if !strings.Contains(hay, t) {
				ok = false
				break
			}
		}
		if ok {
			p.shown = append(p.shown, it)
		}
	}
	p.cur = max(0, min(p.cur, len(p.shown)-1))
}

func (p *pickList) Update(msg tea.Msg) (modal, tea.Cmd) {
	k, ok := msg.(tea.KeyMsg)
	if !ok {
		return p, nil
	}
	switch k.String() {
	case "esc":
		return nil, closeModal
	case "down", "ctrl+n", "ctrl+j":
		p.cur = min(len(p.shown)-1, p.cur+1)
	case "up", "ctrl+p", "ctrl+k":
		p.cur = max(0, p.cur-1)
	case "enter":
		value := ""
		switch {
		case p.cur < len(p.shown):
			value = p.shown[p.cur].value
		case p.typed && p.query != "":
			value = strings.TrimSpace(p.query)
		default:
			return p, nil
		}
		return nil, tea.Batch(closeModal, p.onPick(value))
	case "backspace":
		if r := []rune(p.query); len(r) > 0 {
			p.query = string(r[:len(r)-1])
			p.filter()
		}
	default:
		if isPrintable(k.String()) || k.String() == " " {
			p.query += k.String()
			p.filter()
		}
	}
	return p, nil
}

func (p *pickList) View(w, h int) string {
	width := min(p.width, w-4)
	rows := min(p.maxRows, max(3, h-8))
	lines := []string{sAccent().Render("› ") + p.query + sAccent().Render("▏"), ""}
	start := 0
	if p.cur >= rows {
		start = p.cur - rows + 1
	}
	for i := start; i < len(p.shown) && i < start+rows; i++ {
		it := p.shown[i]
		row := spread(" "+it.label, it.right+" ", width-2)
		if i == p.cur {
			row = paintBg(fit(fg(th.accent).Render(gSel)+row[1:], width-2), th.sel)
		}
		lines = append(lines, row)
	}
	if len(p.shown) == 0 {
		msg := "Nothing matches"
		if p.typed && p.query != "" {
			msg = "⏎ use \"" + p.query + "\""
		}
		lines = append(lines, sMuted().Render(" "+msg))
	}
	return box(p.title, lines, width)
}

func newPickList(title string, items []pickItem, typed bool, onPick func(string) tea.Cmd) *pickList {
	p := &pickList{title: title, items: items, typed: typed, onPick: onPick, width: 64, maxRows: 14}
	p.filter()
	return p
}

// ─── time range ──────────────────────────────────────────────────

func newRangePicker(ctx *appCtx) modal {
	var items []pickItem
	for _, r := range ranges {
		label := "Past " + viz.FormatSpan(r)
		right := ""
		if r == ctx.tr.span && ctx.tr.end.IsZero() {
			right = sAccent().Render("current")
		}
		items = append(items, pickItem{label: label, right: right, value: r.String()})
	}
	p := newPickList("Time range", items, true, func(v string) tea.Cmd {
		d, err := time.ParseDuration(v)
		if err != nil {
			d, err = parseSpan(v)
		}
		if err != nil || d <= 0 {
			return toast("Use a duration like 90m, 6h or 3d", toastErr)
		}
		ctx.tr = timeRange{span: d}
		return func() tea.Msg { return rangeChangedMsg{} }
	})
	for i, r := range ranges {
		if r == ctx.tr.span {
			p.cur = i
		}
	}
	return p
}

// parseSpan accepts 90m, 6h, 3d, 2w.
func parseSpan(s string) (time.Duration, error) {
	s = strings.TrimSpace(strings.TrimPrefix(strings.ToLower(s), "past "))
	if strings.HasSuffix(s, "d") || strings.HasSuffix(s, "w") {
		n := 0
		unit := s[len(s)-1]
		if _, err := fmtSscan(s[:len(s)-1], &n); err != nil {
			return 0, err
		}
		if unit == 'w' {
			return time.Duration(n) * 7 * 24 * time.Hour, nil
		}
		return time.Duration(n) * 24 * time.Hour, nil
	}
	return time.ParseDuration(s)
}

// ─── template variables ──────────────────────────────────────────

func newTVarPicker(ctx *appCtx, d *dashboard) modal {
	var items []pickItem
	for _, t := range d.TVars {
		items = append(items, pickItem{label: "$" + t.Name, right: fg(th.cyan).Render(t.Value), value: t.Name})
	}
	return newPickList("Template variables", items, false, func(name string) tea.Cmd {
		for i := range d.TVars {
			if d.TVars[i].Name == name {
				return openModal(newTVarValuePicker(d, i))
			}
		}
		return nil
	})
}

func newTVarValuePicker(d *dashboard, i int) modal {
	t := d.TVars[i]
	items := []pickItem{{label: "*", right: sMuted().Render("all"), value: "*"}}
	seen := map[string]bool{"*": true}
	for _, v := range append([]string{t.Value}, t.Available...) {
		if v != "" && !seen[v] {
			seen[v] = true
			items = append(items, pickItem{label: v, value: v})
		}
	}
	return newPickList("$"+t.Name+" — pick or type a value", items, true, func(v string) tea.Cmd {
		d.TVars[i].Value = v
		return func() tea.Msg { return tvarsChangedMsg{} }
	})
}

// ─── help ────────────────────────────────────────────────────────

type helpModal struct{ lines []string }

func newHelp(screenHints []hint) modal {
	var lines []string
	add := func(k, v string) { lines = append(lines, sAccent().Render(fit(k, 12))+" "+v) }
	for _, h := range screenHints {
		add(h.key, h.label)
	}
	lines = append(lines, "")
	add("1-5 tab", "switch tabs: Now · Dashboards · Monitors · Logs · Metrics")
	add("t", "time range for every tab")
	add(": ctrl+k", "jump to a dashboard, monitor or tab by name")
	add("B", "charts in braille or blocks (remembered)")
	add("C", "investigate with Claude Code (monitors, logs)")
	add("q esc", "back · quit")
	return &helpModal{lines: lines}
}

func (h *helpModal) Update(msg tea.Msg) (modal, tea.Cmd) {
	if _, ok := msg.(tea.KeyMsg); ok {
		return nil, closeModal
	}
	return h, nil
}

func (h *helpModal) View(w, _ int) string { return box("Keys", h.lines, min(76, w-4)) }

// ─── command palette ─────────────────────────────────────────────

func newPalette(ctx *appCtx) modal {
	items := []pickItem{
		{label: "Now", right: sMuted().Render("tab 1"), value: "tab:0"},
		{label: "Dashboards", right: sMuted().Render("tab 2"), value: "tab:1"},
		{label: "Monitors", right: sMuted().Render("tab 3"), value: "tab:2"},
		{label: "Logs", right: sMuted().Render("tab 4"), value: "tab:3"},
		{label: "Metrics", right: sMuted().Render("tab 5"), value: "tab:4"},
	}
	for _, r := range ranges {
		items = append(items, pickItem{label: "Time: past " + viz.FormatSpan(r), right: sMuted().Render("range"), value: "range:" + r.String()})
	}
	for _, d := range ctx.dashboards {
		items = append(items, pickItem{label: d.Title, right: sMuted().Render("dashboard"), value: "dash:" + d.ID + ":" + d.Title})
	}
	for _, m := range ctx.monitors {
		items = append(items, pickItem{label: stateIcon(m.OverallState) + " " + m.Name, right: sMuted().Render("monitor"), value: "mon:" + fmtInt(m.ID)})
	}
	p := newPickList("Jump to", items, false, func(v string) tea.Cmd {
		kind, rest, _ := strings.Cut(v, ":")
		switch kind {
		case "tab":
			return switchTab(int(rest[0] - '0'))
		case "range":
			d, _ := time.ParseDuration(rest)
			ctx.tr = timeRange{span: d}
			return func() tea.Msg { return rangeChangedMsg{} }
		case "dash":
			id, title, _ := strings.Cut(rest, ":")
			return tea.Sequence(switchTab(1), push(newDashView(ctx, id, title)))
		case "mon":
			return tea.Sequence(switchTab(2), push(newMonitorView(ctx, parseInt64(rest))))
		}
		return nil
	})
	p.width = 76
	return p
}
