package tui

import (
	"fmt"
	"io"
	"strings"

	"datadog-cli/datadog"

	"github.com/charmbracelet/bubbles/list"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// --- Tab bar ---

var tabNames = []string{"Monitors", "Dashboards", "Hosts"}

func renderTabs(active int) string {
	var tabs []string
	for i, name := range tabNames {
		style := lipgloss.NewStyle().Padding(0, 2)
		if i == active {
			style = style.Bold(true).
				Foreground(lipgloss.Color("#FFFFFF")).
				Background(clrPurple)
		} else {
			style = style.Foreground(clrMuted)
		}
		tabs = append(tabs, style.Render(name))
	}
	return lipgloss.NewStyle().MarginLeft(1).Render(strings.Join(tabs, " "))
}

// --- Dashboard items + delegate ---

type DashboardItem struct {
	Dashboard datadog.DashboardSummary
}

func (i DashboardItem) FilterValue() string {
	return i.Dashboard.ID + " " + i.Dashboard.Title + " " + i.Dashboard.AuthorHandle
}

type dashboardsMsg struct {
	dashboards []datadog.DashboardSummary
}

func cmdFetchDashboards(client *datadog.Client) tea.Cmd {
	return func() tea.Msg {
		dashboards, err := client.ListDashboards()
		if err != nil {
			return errActionMsg{err}
		}
		return dashboardsMsg{dashboards: dashboards}
	}
}

type dashboardDelegate struct{}

func (d dashboardDelegate) Height() int                             { return 1 }
func (d dashboardDelegate) Spacing() int                            { return 0 }
func (d dashboardDelegate) Update(_ tea.Msg, _ *list.Model) tea.Cmd { return nil }

func (d dashboardDelegate) Render(w io.Writer, m list.Model, index int, item list.Item) {
	di, ok := item.(DashboardItem)
	if !ok {
		return
	}

	width := m.Width()
	if width < 40 {
		width = 40
	}

	selected := index == m.Index()
	dash := di.Dashboard

	cursor := "  "
	if selected {
		cursor = lipgloss.NewStyle().Foreground(clrPurple).Bold(true).Render("| ")
	}

	id := lipgloss.NewStyle().Bold(true).Foreground(clrWhite).Width(15).Render(dash.ID)
	layout := lipgloss.NewStyle().Foreground(clrDim).Width(12).Render(dash.LayoutType)

	author := dash.AuthorHandle
	if idx := strings.Index(author, "@"); idx > 0 {
		author = author[:idx]
	}
	authorStyled := lipgloss.NewStyle().Foreground(clrMuted).Width(12).Render(author)

	fixedW := 2 + 15 + 12 + 12 + 4
	titleW := width - fixedW
	if titleW < 10 {
		titleW = 10
	}
	title := dash.Title
	if len(title) > titleW {
		title = title[:titleW-1] + "…"
	}

	row := fmt.Sprintf("%s%s %s %s %s", cursor, id, layout, authorStyled,
		lipgloss.NewStyle().Foreground(clrText).Render(title))

	if selected {
		row = lipgloss.NewStyle().Background(lipgloss.Color("#1E293B")).Width(width).MaxWidth(width).Render(row)
	} else {
		row = lipgloss.NewStyle().Width(width).MaxWidth(width).Render(row)
	}

	fmt.Fprint(w, row)
}

// --- Host items + delegate ---

type HostItem struct {
	Host datadog.Host
}

func (i HostItem) FilterValue() string {
	return i.Host.Name + " " + strings.Join(i.Host.Apps, " ")
}

type hostsMsg struct {
	hosts []datadog.Host
	total int
}

func cmdFetchHosts(client *datadog.Client) tea.Cmd {
	return func() tea.Msg {
		result, err := client.ListHosts("", 200)
		if err != nil {
			return errActionMsg{err}
		}
		return hostsMsg{hosts: result.HostList, total: result.TotalMatching}
	}
}

type hostDelegate struct{}

func (d hostDelegate) Height() int                             { return 1 }
func (d hostDelegate) Spacing() int                            { return 0 }
func (d hostDelegate) Update(_ tea.Msg, _ *list.Model) tea.Cmd { return nil }

func (d hostDelegate) Render(w io.Writer, m list.Model, index int, item list.Item) {
	hi, ok := item.(HostItem)
	if !ok {
		return
	}

	width := m.Width()
	if width < 40 {
		width = 40
	}

	selected := index == m.Index()
	host := hi.Host

	cursor := "  "
	if selected {
		cursor = lipgloss.NewStyle().Foreground(clrPurple).Bold(true).Render("| ")
	}

	name := host.Name
	if len(name) > 35 {
		name = name[:32] + "…"
	}
	nameStyled := lipgloss.NewStyle().Bold(true).Foreground(clrWhite).Width(35).Render(name)

	statusClr := clrGreen
	statusText := "UP"
	if !host.Up {
		statusClr = clrRed
		statusText = "DOWN"
	}
	status := lipgloss.NewStyle().Foreground(statusClr).Bold(true).Width(6).Render(statusText)

	muted := lipgloss.NewStyle().Foreground(clrDim).Width(6).Render("")
	if host.IsMuted {
		muted = lipgloss.NewStyle().Foreground(clrYellow).Width(6).Render("muted")
	}

	platform := lipgloss.NewStyle().Foreground(clrDim).Width(10).Render(host.Meta.Platform)

	apps := strings.Join(host.Apps, ",")
	if len(apps) > 20 {
		apps = apps[:17] + "…"
	}
	appsStyled := lipgloss.NewStyle().Foreground(clrMuted).Render(apps)

	row := fmt.Sprintf("%s%s %s %s %s %s", cursor, nameStyled, status, muted, platform, appsStyled)

	if selected {
		row = lipgloss.NewStyle().Background(lipgloss.Color("#1E293B")).Width(width).MaxWidth(width).Render(row)
	} else {
		row = lipgloss.NewStyle().Width(width).MaxWidth(width).Render(row)
	}

	fmt.Fprint(w, row)
}
