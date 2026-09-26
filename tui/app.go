// Package tui is 'datadog ui': dashboards, monitors, logs and metrics in
// the terminal.
package tui

import (
	"os/exec"
	"runtime"
	"strings"
	"time"

	"github.com/ngavilan-dogfy/datadog-cli/datadog"
	"github.com/ngavilan-dogfy/datadog-cli/internal/uiprefs"
	"github.com/ngavilan-dogfy/datadog-cli/viz"

	tea "github.com/charmbracelet/bubbletea"
)

// screen is one page of a tab: a list, a dashboard, a monitor…
type screen interface {
	Init() tea.Cmd
	Update(tea.Msg) (screen, tea.Cmd)
	View() string // exactly the size given by SetSize
	SetSize(w, h int)
	Title() string
	Hints() []hint
}

// modal floats over the screens (pickers, help, the command palette).
type modal interface {
	Update(tea.Msg) (modal, tea.Cmd)
	View(w, h int) string // a box; the app centers it
}

type hint struct{ key, label string }

// appCtx is what screens share.
type appCtx struct {
	api     API
	site    string
	style   viz.Style
	tr      timeRange
	spin    int
	update  string // newer release available, if any
	repos   string // folder with git checkouts (for investigations)
	profile string
	demo    bool // made-up data: no links, no hand-offs

	// shared lists, for the command palette
	dashboards []datadog.DashboardSummary
	monitors   []datadog.Monitor
}

// Options configures the UI at start.
type Options struct {
	Site        string
	Profile     string
	Dashboard   string // open this dashboard (id) right away
	Tab         string // now, dashboards, monitors, logs, metrics
	UpdateNotes string // newer release available
	Demo        bool   // the API is the demo org (datadog ui --demo)
}

type tab struct {
	name  string
	stack []screen
}

// Model is the root Bubble Tea model.
type Model struct {
	ctx    *appCtx
	tabs   []*tab
	active int
	modal  modal
	w, h   int
	toast  *toastMsg
	opts   Options
}

type (
	spinMsg         struct{}
	resizedMsg      struct{} // after the terminal size changes
	rangeChangedMsg struct{}
	tvarsChangedMsg struct{}
	pushMsg         struct{ s screen }
	popMsg          struct{}
	closeModalMsg   struct{}
	openModalMsg    struct{ m modal }
	switchTabMsg    struct{ i int }
	clearToastMsg   struct{ at time.Time }
	toastKind       int
)

const (
	toastInfo toastKind = iota
	toastOK
	toastErr
)

type toastMsg struct {
	text string
	kind toastKind
	at   time.Time
}

func toast(text string, kind toastKind) tea.Cmd {
	return func() tea.Msg { return toastMsg{text: text, kind: kind} }
}

func push(s screen) tea.Cmd     { return func() tea.Msg { return pushMsg{s} } }
func pop() tea.Msg              { return popMsg{} }
func closeModal() tea.Msg       { return closeModalMsg{} }
func openModal(m modal) tea.Cmd { return func() tea.Msg { return openModalMsg{m} } }
func switchTab(i int) tea.Cmd   { return func() tea.Msg { return switchTabMsg{i} } }
func spinTick() tea.Cmd {
	return spinFn(150*time.Millisecond, func(time.Time) tea.Msg { return spinMsg{} })
}

// Timers go through these so tests can run without waiting: periodic
// refreshes and debounces (tickFn) and the spinner (spinFn).
var (
	tickFn = tea.Tick
	spinFn = tea.Tick
)

// openURL opens a page in the browser; swappable in tests.
var openURL = func(u string) tea.Cmd {
	return func() tea.Msg {
		if u == "" {
			return toastMsg{text: "Demo data: there's nothing to open in Datadog", kind: toastInfo}
		}
		var err error
		switch runtime.GOOS {
		case "darwin":
			err = exec.Command("open", u).Start()
		case "linux":
			err = exec.Command("xdg-open", u).Start()
		default:
			err = exec.Command("rundll32", "url.dll,FileProtocolHandler", u).Start()
		}
		if err != nil {
			return toastMsg{text: "Couldn't open the browser: " + err.Error(), kind: toastErr}
		}
		return toastMsg{text: "Opened in your browser", kind: toastInfo}
	}
}

// New builds the UI. Call Run to start it.
func New(api API, opts Options) *Model {
	ctx := &appCtx{api: api, site: opts.Site, profile: opts.Profile, tr: timeRange{span: time.Hour},
		style: uiprefs.VizStyle(), update: opts.UpdateNotes, demo: opts.Demo}
	m := &Model{ctx: ctx, opts: opts}
	m.tabs = []*tab{
		{name: "Now", stack: []screen{newHome(ctx)}},
		{name: "Dashboards", stack: []screen{newDashList(ctx)}},
		{name: "Monitors", stack: []screen{newMonitors(ctx)}},
		{name: "Logs", stack: []screen{newLogs(ctx, "")}},
		{name: "Metrics", stack: []screen{newMetrics(ctx, "")}},
	}
	switch opts.Tab {
	case "dashboards":
		m.active = 1
	case "monitors":
		m.active = 2
	case "logs":
		m.active = 3
	case "metrics":
		m.active = 4
	}
	if opts.Dashboard != "" {
		m.active = 1
		m.tabs[1].stack = append(m.tabs[1].stack, newDashView(ctx, opts.Dashboard, opts.Dashboard))
	}
	return m
}

// Run starts the UI in the alternate screen.
func Run(m *Model) error {
	initTheme()
	p := tea.NewProgram(m, tea.WithAltScreen(), tea.WithMouseCellMotion())
	_, err := p.Run()
	return err
}

func (m *Model) Init() tea.Cmd {
	cmds := []tea.Cmd{spinTick()}
	for _, t := range m.tabs {
		for _, s := range t.stack {
			cmds = append(cmds, s.Init())
		}
	}
	return tea.Batch(cmds...)
}

func (m *Model) top() screen {
	t := m.tabs[m.active]
	return t.stack[len(t.stack)-1]
}

func (m *Model) bodySize() (int, int) { return m.w, max(1, m.h-2) }

func (m *Model) resizeAll() {
	w, h := m.bodySize()
	for _, t := range m.tabs {
		for _, s := range t.stack {
			s.SetSize(w, h)
		}
	}
}

func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.w, m.h = msg.Width, msg.Height
		m.resizeAll()
		// Data asked for before the size was known (or for a much smaller
		// window) is too coarse: screens refetch what needs it.
		return m, m.broadcast(resizedMsg{})
	case spinMsg:
		m.ctx.spin++
		cmd := m.broadcast(msg)
		return m, tea.Batch(spinTick(), cmd)
	case toastMsg:
		msg.at = now()
		m.toast = &msg
		at := msg.at
		return m, tickFn(4*time.Second, func(time.Time) tea.Msg { return clearToastMsg{at} })
	case clearToastMsg:
		if m.toast != nil && m.toast.at.Equal(msg.at) {
			m.toast = nil
		}
		return m, nil
	case pushMsg:
		t := m.tabs[m.active]
		t.stack = append(t.stack, msg.s)
		w, h := m.bodySize()
		msg.s.SetSize(w, h)
		return m, msg.s.Init()
	case popMsg:
		t := m.tabs[m.active]
		if len(t.stack) > 1 {
			t.stack = t.stack[:len(t.stack)-1]
		}
		return m, nil
	case openModalMsg:
		m.modal = msg.m
		return m, nil
	case closeModalMsg:
		m.modal = nil
		return m, nil
	case switchTabMsg:
		m.active = msg.i
		return m, nil
	case rangeChangedMsg, tvarsChangedMsg:
		return m, m.broadcast(msg)
	case tea.KeyMsg:
		if msg.String() == "ctrl+c" {
			return m, tea.Quit
		}
		if m.modal != nil {
			var cmd tea.Cmd
			m.modal, cmd = m.modal.Update(msg)
			return m, cmd
		}
		if cmd, ok := m.globalKey(msg); ok {
			return m, cmd
		}
		t := m.tabs[m.active]
		s, cmd := t.stack[len(t.stack)-1].Update(msg)
		t.stack[len(t.stack)-1] = s
		return m, cmd
	case tea.MouseMsg:
		t := m.tabs[m.active]
		s, cmd := t.stack[len(t.stack)-1].Update(msg)
		t.stack[len(t.stack)-1] = s
		return m, cmd
	}
	// Async results go to every screen: each one ignores what isn't its own.
	if m.modal != nil {
		var cmd tea.Cmd
		m.modal, cmd = m.modal.Update(msg)
		return m, tea.Batch(cmd, m.broadcast(msg))
	}
	return m, m.broadcast(msg)
}

func (m *Model) broadcast(msg tea.Msg) tea.Cmd {
	var cmds []tea.Cmd
	for _, t := range m.tabs {
		for i, s := range t.stack {
			ns, cmd := s.Update(msg)
			t.stack[i] = ns
			cmds = append(cmds, cmd)
		}
	}
	return tea.Batch(cmds...)
}

// typing reports whether the top screen has a focused text input, so
// single-letter shortcuts go to it instead.
func (m *Model) typing() bool {
	if t, ok := m.top().(interface{ Typing() bool }); ok {
		return t.Typing()
	}
	return false
}

func (m *Model) globalKey(k tea.KeyMsg) (tea.Cmd, bool) {
	if m.typing() {
		return nil, false
	}
	switch k.String() {
	case "q":
		if len(m.tabs[m.active].stack) > 1 {
			return pop, true
		}
		return tea.Quit, true
	case "esc":
		if len(m.tabs[m.active].stack) > 1 {
			if z, ok := m.top().(*dashView); ok && z.zoomed != nil {
				return nil, false
			}
			return pop, true
		}
		return nil, true
	case "1", "2", "3", "4", "5":
		m.active = int(k.Runes[0] - '1')
		return nil, true
	case "tab":
		if _, ok := m.top().(*dashView); ok {
			return nil, false
		}
		m.active = (m.active + 1) % len(m.tabs)
		return nil, true
	case "shift+tab":
		if _, ok := m.top().(*dashView); ok {
			return nil, false
		}
		m.active = (m.active - 1 + len(m.tabs)) % len(m.tabs)
		return nil, true
	case "?":
		return openModal(newHelp(m.top().Hints())), true
	case ":", "ctrl+k":
		return openModal(newPalette(m.ctx)), true
	case "t":
		return openModal(newRangePicker(m.ctx)), true
	case "B":
		if m.ctx.style == viz.Braille {
			m.ctx.style = viz.Blocks
		} else {
			m.ctx.style = viz.Braille
		}
		name := map[viz.Style]string{viz.Braille: "braille", viz.Blocks: "blocks"}[m.ctx.style]
		_ = uiprefs.SetChartStyle(name)
		return tea.Batch(toast("Charts: "+name, toastInfo), func() tea.Msg { return rangeChangedMsg{} }), true
	}
	return nil, false
}

func (m *Model) View() string {
	if m.w == 0 || m.h == 0 {
		return ""
	}
	body := m.top().View()
	if m.modal != nil {
		body = overlay(body, m.modal.View(m.w, m.h-2), m.w, m.h-2)
	}
	return m.header() + "\n" + frame(body, m.w, m.h-2) + "\n" + m.footer()
}

func (m *Model) header() string {
	left := " " + pill("datadog", th.accent)
	if m.ctx.demo {
		left += " " + pill("DEMO", th.yellow)
	}
	for i, t := range m.tabs {
		label := string(rune('1'+i)) + " " + t.name
		if i == m.active {
			left += " " + fg(th.accent).Bold(true).Render(label)
		} else {
			left += " " + sMuted().Render(label)
		}
	}
	var crumbs []string
	for _, s := range m.tabs[m.active].stack[1:] {
		crumbs = append(crumbs, s.Title())
	}
	if len(crumbs) > 0 {
		left += sMuted().Render("  " + gArrow + " " + strings.Join(crumbs, " "+gArrow+" "))
	}
	right := fg(th.cyan).Render(m.ctx.tr.label()) + sMuted().Render("  "+m.ctx.site+" ")
	return paintBg(spread(left, right, m.w), th.surface)
}

func (m *Model) footer() string {
	var parts []string
	for _, h := range m.top().Hints() {
		parts = append(parts, sAccent().Render(h.key)+" "+sMuted().Render(h.label))
	}
	if !m.typing() { // while typing, these letters go to the input
		parts = append(parts, sAccent().Render("t")+" "+sMuted().Render("time"), sAccent().Render(":")+" "+sMuted().Render("jump"), sAccent().Render("?")+" "+sMuted().Render("help"))
	}
	left := " " + strings.Join(parts, "  ")
	right := ""
	switch {
	case m.toast != nil:
		c := th.muted
		switch m.toast.kind {
		case toastOK:
			c = th.green
		case toastErr:
			c = th.red
		}
		right = fg(c).Render(trunc(m.toast.text, m.w/2)) + " "
	case m.ctx.update != "":
		right = fg(th.yellow).Render(m.ctx.update+" available · datadog update") + " "
	}
	return spread(left, right, m.w)
}

// overlay centers a box over the body.
func overlay(body, box string, w, h int) string {
	bl := strings.Split(frame(body, w, h), "\n")
	boxLines := strings.Split(box, "\n")
	bw := 0
	for _, l := range boxLines {
		bw = max(bw, sw(l))
	}
	top := max(0, (h-len(boxLines))/3)
	left := max(0, (w-bw)/2)
	for i, l := range boxLines {
		y := top + i
		if y >= len(bl) {
			break
		}
		bl[y] = fit(strings.Repeat(" ", left)+fit(l, bw), w)
	}
	return strings.Join(bl, "\n")
}
