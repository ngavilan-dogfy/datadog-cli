package tui

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/ngavilan-dogfy/datadog-cli/datadog"

	tea "github.com/charmbracelet/bubbletea"
)

// dashList lists the organization's dashboards with a filter-as-you-type.
type dashList struct {
	ctx     *appCtx
	all     []datadog.DashboardSummary
	shown   []datadog.DashboardSummary
	err     error
	loading bool
	cur     int
	scroll  int
	filter  string
	editing bool
	w, h    int
}

type dashListMsg struct {
	items []datadog.DashboardSummary
	err   error
}

func newDashList(ctx *appCtx) *dashList { return &dashList{ctx: ctx, loading: true} }

func (l *dashList) Title() string    { return "Dashboards" }
func (l *dashList) Typing() bool     { return l.editing }
func (l *dashList) SetSize(w, h int) { l.w, l.h = w, h }

func (l *dashList) Init() tea.Cmd {
	api := l.ctx.api
	return func() tea.Msg {
		items, err := api.ListDashboards()
		return dashListMsg{items, err}
	}
}

func (l *dashList) apply() {
	terms := queryTerms(l.filter)
	l.shown = l.shown[:0]
	for _, d := range l.all {
		hay := fold(d.Title + " " + d.ID + " " + d.AuthorHandle + " " + d.Description)
		ok := true
		for _, t := range terms {
			if !strings.Contains(hay, t) {
				ok = false
				break
			}
		}
		if ok {
			l.shown = append(l.shown, d)
		}
	}
	l.cur = max(0, min(l.cur, len(l.shown)-1))
}

func (l *dashList) Update(msg tea.Msg) (screen, tea.Cmd) {
	switch msg := msg.(type) {
	case dashListMsg:
		l.loading, l.err = false, msg.err
		l.all = msg.items
		l.ctx.dashboards = msg.items
		sort.SliceStable(l.all, func(i, j int) bool { return l.all[i].ModifiedAt > l.all[j].ModifiedAt })
		l.apply()
	case tea.KeyMsg:
		if l.editing {
			switch msg.String() {
			case "esc":
				l.editing, l.filter = false, ""
				l.apply()
			case "enter":
				l.editing = false
			case "backspace":
				if r := []rune(l.filter); len(r) > 0 {
					l.filter = string(r[:len(r)-1])
					l.apply()
				}
			case "down", "ctrl+n":
				l.cur = min(len(l.shown)-1, l.cur+1)
			case "up", "ctrl+p":
				l.cur = max(0, l.cur-1)
			default:
				if isPrintable(msg.String()) || msg.String() == " " {
					l.filter += msg.String()
					l.apply()
				}
			}
			return l, nil
		}
		switch msg.String() {
		case "/":
			l.editing = true
		case "down", "j":
			l.cur = min(len(l.shown)-1, l.cur+1)
		case "up", "k":
			l.cur = max(0, l.cur-1)
		case "g", "home":
			l.cur = 0
		case "G", "end":
			l.cur = max(0, len(l.shown)-1)
		case "enter", "l", "right":
			if l.cur < len(l.shown) {
				d := l.shown[l.cur]
				return l, push(newDashView(l.ctx, d.ID, d.Title))
			}
		case "o":
			if l.cur < len(l.shown) {
				return l, openURL(l.ctx.api.DashboardURL(l.shown[l.cur].ID))
			}
		case "r":
			l.loading = true
			return l, l.Init()
		}
	}
	return l, nil
}

func (l *dashList) View() string {
	var out []string
	title := " " + sBold().Render("Dashboards")
	if !l.loading {
		title += sMuted().Render(fmt.Sprintf("  %d", len(l.shown)))
		if len(l.shown) != len(l.all) {
			title += sMuted().Render(fmt.Sprintf(" of %d", len(l.all)))
		}
	}
	filter := sMuted().Render("/ filter")
	if l.editing || l.filter != "" {
		filter = sAccent().Render("/ ") + l.filter
		if l.editing {
			filter += sAccent().Render("▏")
		}
	}
	out = append(out, spread(title, filter+" ", l.w), "")
	switch {
	case l.loading:
		out = append(out, "  "+sMuted().Render(spinnerFrames[l.ctx.spin%len(spinnerFrames)]+" Loading dashboards…"))
	case l.err != nil:
		out = append(out, "  "+fg(th.red).Render("Couldn't load dashboards: ")+l.err.Error())
	case len(l.shown) == 0:
		out = append(out, "  "+sMuted().Render("No dashboards match."))
	}
	listH := l.h - len(out)
	if l.cur < l.scroll {
		l.scroll = l.cur
	}
	if l.cur >= l.scroll+listH {
		l.scroll = l.cur - listH + 1
	}
	terms := queryTerms(l.filter)
	idW := 13
	authorW := min(22, max(10, l.w/6))
	ageW := 7
	titleW := max(10, l.w-idW-authorW-ageW-8)
	for i := l.scroll; i < len(l.shown) && i < l.scroll+listH; i++ {
		d := l.shown[i]
		mod := ""
		if t, err := time.Parse(time.RFC3339Nano, d.ModifiedAt); err == nil {
			mod = age(t)
		}
		author := d.AuthorHandle
		if at := strings.IndexByte(author, '@'); at > 0 {
			author = author[:at]
		}
		row := " " + fit(highlight(trunc(d.Title, titleW), terms, sBold(), fg(th.yellow).Bold(true)), titleW) + "  " +
			sMuted().Render(fit(d.ID, idW)) + " " + sMuted().Render(fit(author, authorW)) + " " + sMuted().Render(fitRight(mod, ageW))
		if i == l.cur {
			row = paintBg(fg(th.accent).Render(gSel)+fit(row[1:], l.w-1), th.sel)
		}
		out = append(out, fit(row, l.w))
	}
	return frame(strings.Join(out, "\n"), l.w, l.h)
}

func (l *dashList) Hints() []hint {
	return []hint{{"⏎", "open"}, {"/", "filter"}, {"o", "browser"}, {"r", "reload"}, {"q", "quit"}}
}
