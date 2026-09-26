package tui

import (
	"fmt"
	"strings"

	"github.com/ngavilan-dogfy/datadog-cli/datadog"

	"github.com/charmbracelet/bubbles/list"
)

// MonitorItem wraps a Datadog monitor for the bubbles/list component.
type MonitorItem struct {
	Monitor datadog.Monitor
}

func (i MonitorItem) FilterValue() string {
	parts := []string{
		fmt.Sprintf("%d", i.Monitor.ID),
		i.Monitor.Name,
		i.Monitor.OverallState,
		i.Monitor.Type,
	}
	parts = append(parts, i.Monitor.Tags...)
	return strings.Join(parts, " ")
}

// buildMonitorItems creates a list of MonitorItems with optional state filtering.
func buildMonitorItems(monitors []datadog.Monitor, stateFilter string) []list.Item {
	var items []list.Item
	for _, m := range monitors {
		if stateFilter != "" && !strings.EqualFold(m.OverallState, stateFilter) {
			continue
		}
		items = append(items, MonitorItem{Monitor: m})
	}
	return items
}
