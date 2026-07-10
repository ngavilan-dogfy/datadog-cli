package tui

import (
	"fmt"
	"strings"
	"time"

	"datadog-cli/datadog"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/list"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

var stateFilters = []string{"", "OK", "Alert", "Warn", "No Data"}

type listView struct {
	client *datadog.Client

	// Tab state
	activeTab int // 0=monitors, 1=dashboards, 2=hosts
	lists     [3]list.Model

	// Cached data
	monitors   []datadog.Monitor
	dashboards []datadog.DashboardSummary
	hosts      []datadog.Host
	hostsTotal int

	stateIdx    int // monitor state filter index
	width       int
	height      int
	autoRefresh bool
	loaded      [3]bool
}

func newListView(client *datadog.Client, w, h int) *listView {
	listH := h - 6

	// Monitors list
	monList := list.New(nil, monitorDelegate{}, w, listH)
	monList.Title = "Monitors"
	monList.SetShowStatusBar(true)
	monList.SetShowHelp(false)
	monList.SetFilteringEnabled(true)
	monList.Styles.Title = lipgloss.NewStyle().Bold(true).Foreground(clrPurple).MarginLeft(1)
	monList.Styles.FilterPrompt = lipgloss.NewStyle().Foreground(clrCyan)
	monList.Styles.FilterCursor = lipgloss.NewStyle().Foreground(clrPurple)

	// Dashboards list
	dashList := list.New(nil, dashboardDelegate{}, w, listH)
	dashList.Title = "Dashboards"
	dashList.SetShowStatusBar(true)
	dashList.SetShowHelp(false)
	dashList.SetFilteringEnabled(true)
	dashList.Styles.Title = lipgloss.NewStyle().Bold(true).Foreground(clrPurple).MarginLeft(1)
	dashList.Styles.FilterPrompt = lipgloss.NewStyle().Foreground(clrCyan)
	dashList.Styles.FilterCursor = lipgloss.NewStyle().Foreground(clrPurple)

	// Hosts list
	hostList := list.New(nil, hostDelegate{}, w, listH)
	hostList.Title = "Hosts"
	hostList.SetShowStatusBar(true)
	hostList.SetShowHelp(false)
	hostList.SetFilteringEnabled(true)
	hostList.Styles.Title = lipgloss.NewStyle().Bold(true).Foreground(clrPurple).MarginLeft(1)
	hostList.Styles.FilterPrompt = lipgloss.NewStyle().Foreground(clrCyan)
	hostList.Styles.FilterCursor = lipgloss.NewStyle().Foreground(clrPurple)

	return &listView{
		client:      client,
		lists:       [3]list.Model{monList, dashList, hostList},
		width:       w,
		height:      h,
		autoRefresh: true,
	}
}

func (v *listView) Init() tea.Cmd {
	return tea.Batch(
		cmdFetchMonitors(v.client),
		cmdAutoRefresh(30*time.Second),
	)
}

func (v *listView) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {

	case tea.WindowSizeMsg:
		v.width = msg.Width
		v.height = msg.Height
		for i := range v.lists {
			v.lists[i].SetSize(msg.Width, msg.Height-6)
		}
		return v, nil

	case monitorsMsg:
		v.monitors = msg.monitors
		v.loaded[0] = true
		v.rebuildMonitorItems()
		return v, nil

	case dashboardsMsg:
		v.dashboards = msg.dashboards
		v.loaded[1] = true
		v.rebuildDashboardItems()
		return v, nil

	case hostsMsg:
		v.hosts = msg.hosts
		v.hostsTotal = msg.total
		v.loaded[2] = true
		v.rebuildHostItems()
		return v, nil

	case tickMsg:
		if v.autoRefresh && !v.lists[v.activeTab].SettingFilter() {
			var cmds []tea.Cmd
			switch v.activeTab {
			case 0:
				cmds = append(cmds, cmdFetchMonitors(v.client))
			case 1:
				cmds = append(cmds, cmdFetchDashboards(v.client))
			case 2:
				cmds = append(cmds, cmdFetchHosts(v.client))
			}
			cmds = append(cmds, cmdAutoRefresh(30*time.Second))
			return v, tea.Batch(cmds...)
		}
		return v, cmdAutoRefresh(30*time.Second)

	case tea.KeyMsg:
		// Don't intercept keys during filtering
		if v.lists[v.activeTab].SettingFilter() {
			var cmd tea.Cmd
			v.lists[v.activeTab], cmd = v.lists[v.activeTab].Update(msg)
			return v, cmd
		}

		switch {
		// Tab switching
		case msg.String() == "tab":
			v.activeTab = (v.activeTab + 1) % len(tabNames)
			return v, v.ensureTabLoaded()

		case msg.String() == "shift+tab":
			v.activeTab = (v.activeTab + len(tabNames) - 1) % len(tabNames)
			return v, v.ensureTabLoaded()

		case msg.String() == "1":
			v.activeTab = 0
			return v, v.ensureTabLoaded()
		case msg.String() == "2":
			v.activeTab = 1
			return v, v.ensureTabLoaded()
		case msg.String() == "3":
			v.activeTab = 2
			return v, v.ensureTabLoaded()

		case key.Matches(msg, actionKeys.Open):
			return v, v.openSelected()

		case key.Matches(msg, actionKeys.Mute):
			if v.activeTab == 0 {
				if item, ok := v.lists[0].SelectedItem().(MonitorItem); ok {
					return v, cmdMuteMonitor(v.client, item.Monitor.ID)
				}
			}

		case key.Matches(msg, actionKeys.Unmute):
			if v.activeTab == 0 {
				if item, ok := v.lists[0].SelectedItem().(MonitorItem); ok {
					return v, cmdUnmuteMonitor(v.client, item.Monitor.ID)
				}
			}

		case key.Matches(msg, actionKeys.Refresh):
			return v, v.refreshTab()

		case key.Matches(msg, actionKeys.Filter):
			if v.activeTab == 0 {
				v.stateIdx = (v.stateIdx + 1) % len(stateFilters)
				v.rebuildMonitorItems()
			}
			return v, nil

		case msg.String() == "enter":
			if v.activeTab == 0 {
				if item, ok := v.lists[0].SelectedItem().(MonitorItem); ok {
					dv := newDetailView(v.client, item.Monitor.ID, v.width, v.height)
					return v, func() tea.Msg { return pushMsg{view: dv} }
				}
			} else if v.activeTab == 1 {
				// Open dashboard in browser
				return v, v.openSelected()
			}

		case msg.String() == "q":
			return v, tea.Quit
		}
	}

	var cmd tea.Cmd
	v.lists[v.activeTab], cmd = v.lists[v.activeTab].Update(msg)
	return v, cmd
}

func (v *listView) View() string {
	// Tab bar
	tabs := renderTabs(v.activeTab)

	// Status bar based on active tab
	var statusBar string
	switch v.activeTab {
	case 0:
		statusBar = v.monitorStatusBar()
	case 1:
		statusBar = lipgloss.NewStyle().MarginLeft(2).Foreground(clrDim).
			Render(fmt.Sprintf("%d dashboards", len(v.dashboards)))
	case 2:
		statusBar = lipgloss.NewStyle().MarginLeft(2).Foreground(clrDim).
			Render(fmt.Sprintf("%d hosts", len(v.hosts)))
	}

	// Footer
	var footerHints string
	switch v.activeTab {
	case 0:
		footerHints = hint("enter", "detail") +
			hint("m", "mute") +
			hint("u", "unmute") +
			hint("o", "browser") +
			hint("f", "filter") +
			hint("r", "refresh") +
			hint("/", "search") +
			hint("tab", "switch") +
			hint("q", "quit")
	case 1:
		footerHints = hint("enter", "open") +
			hint("o", "browser") +
			hint("r", "refresh") +
			hint("/", "search") +
			hint("tab", "switch") +
			hint("q", "quit")
	case 2:
		footerHints = hint("o", "infra") +
			hint("r", "refresh") +
			hint("/", "search") +
			hint("tab", "switch") +
			hint("q", "quit")
	}
	footer := lipgloss.NewStyle().MarginLeft(1).Render(footerHints)

	return tabs + "\n" + statusBar + "\n" + v.lists[v.activeTab].View() + "\n" + footer
}

func (v *listView) monitorStatusBar() string {
	counts := map[string]int{}
	for _, m := range v.monitors {
		counts[m.OverallState]++
	}
	var parts []string
	for _, s := range []string{"OK", "Alert", "Warn", "No Data"} {
		if c, ok := counts[s]; ok && c > 0 {
			clr := monitorStateClr(s)
			parts = append(parts, lipgloss.NewStyle().Foreground(clr).Bold(true).Render(fmt.Sprintf("%s:%d", s, c)))
		}
	}

	filterLabel := "all"
	if v.stateIdx > 0 {
		filterLabel = stateFilters[v.stateIdx]
	}

	return lipgloss.NewStyle().MarginLeft(2).Render(
		strings.Join(parts, "  ") + "  " +
			lipgloss.NewStyle().Foreground(clrDim).Render("filter:") +
			lipgloss.NewStyle().Foreground(clrCyan).Bold(true).Render(filterLabel))
}

func (v *listView) ensureTabLoaded() tea.Cmd {
	if v.loaded[v.activeTab] {
		return nil
	}
	return v.refreshTab()
}

func (v *listView) refreshTab() tea.Cmd {
	switch v.activeTab {
	case 0:
		return cmdFetchMonitors(v.client)
	case 1:
		return cmdFetchDashboards(v.client)
	case 2:
		return cmdFetchHosts(v.client)
	}
	return nil
}

func (v *listView) openSelected() tea.Cmd {
	switch v.activeTab {
	case 0:
		if item, ok := v.lists[0].SelectedItem().(MonitorItem); ok {
			openBrowser(fmt.Sprintf("https://app.datadoghq.eu/monitors/%d", item.Monitor.ID))
		}
	case 1:
		if item, ok := v.lists[1].SelectedItem().(DashboardItem); ok {
			openBrowser(fmt.Sprintf("https://app.datadoghq.eu/dashboard/%s", item.Dashboard.ID))
		}
	case 2:
		if item, ok := v.lists[2].SelectedItem().(HostItem); ok {
			openBrowser(fmt.Sprintf("https://app.datadoghq.eu/infrastructure?host=%s", item.Host.Name))
		}
	}
	return nil
}

func (v *listView) rebuildMonitorItems() {
	filter := ""
	if v.stateIdx > 0 {
		filter = stateFilters[v.stateIdx]
	}
	items := buildMonitorItems(v.monitors, filter)
	v.lists[0].SetItems(items)

	title := "Monitors"
	if filter != "" {
		title += " · " + filter
	}
	v.lists[0].Title = title
}

func (v *listView) rebuildDashboardItems() {
	var items []list.Item
	for _, d := range v.dashboards {
		items = append(items, DashboardItem{Dashboard: d})
	}
	v.lists[1].SetItems(items)
}

func (v *listView) rebuildHostItems() {
	var items []list.Item
	for _, h := range v.hosts {
		items = append(items, HostItem{Host: h})
	}
	v.lists[2].SetItems(items)
}
