package tui

import (
	"time"

	"github.com/ngavilan-dogfy/datadog-cli/datadog"

	tea "github.com/charmbracelet/bubbletea"
)

func cmdFetchMonitors(client *datadog.Client) tea.Cmd {
	if client == nil {
		return nil
	}
	return func() tea.Msg {
		monitors, err := client.ListMonitors("", 100)
		if err != nil {
			return errActionMsg{err}
		}
		return monitorsMsg{monitors: monitors}
	}
}

func cmdFetchMonitorDetail(client *datadog.Client, id int64) tea.Cmd {
	return func() tea.Msg {
		monitor, err := client.GetMonitor(id)
		if err != nil {
			return errActionMsg{err}
		}
		return monitorDetailMsg{monitor: monitor}
	}
}

func cmdMuteMonitor(client *datadog.Client, id int64) tea.Cmd {
	return func() tea.Msg {
		if err := client.MuteMonitor(id, 0); err != nil {
			return errActionMsg{err}
		}
		return actionDoneMsg{text: "Muted"}
	}
}

func cmdUnmuteMonitor(client *datadog.Client, id int64) tea.Cmd {
	return func() tea.Msg {
		if err := client.UnmuteMonitor(id); err != nil {
			return errActionMsg{err}
		}
		return actionDoneMsg{text: "Unmuted"}
	}
}

func cmdClearFlash(d time.Duration) tea.Cmd {
	return tea.Tick(d, func(time.Time) tea.Msg { return clearFlashMsg{} })
}

// cmdAutoRefresh returns a cmd that ticks every interval.
type tickMsg struct{}

func cmdAutoRefresh(interval time.Duration) tea.Cmd {
	return tea.Tick(interval, func(time.Time) tea.Msg { return tickMsg{} })
}
