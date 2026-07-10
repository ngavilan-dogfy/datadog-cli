package tui

import (
	"fmt"
	"io"
	"strconv"

	"datadog-cli/datadog"

	"github.com/charmbracelet/bubbles/list"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

type monitorDelegate struct{}

func (d monitorDelegate) Height() int                             { return 1 }
func (d monitorDelegate) Spacing() int                            { return 0 }
func (d monitorDelegate) Update(_ tea.Msg, _ *list.Model) tea.Cmd { return nil }

func (d monitorDelegate) Render(w io.Writer, m list.Model, index int, item list.Item) {
	mi, ok := item.(MonitorItem)
	if !ok {
		return
	}

	width := m.Width()
	if width < 40 {
		width = 40
	}

	selected := index == m.Index()
	mon := mi.Monitor

	// Cursor
	cursor := "  "
	if selected {
		cursor = lipgloss.NewStyle().Foreground(clrPurple).Bold(true).Render("| ")
	}

	// Type icon
	icon := monitorTypeIcon(mon.Type)

	// ID
	idStr := lipgloss.NewStyle().Bold(true).Foreground(clrWhite).
		Width(9).Render(strconv.FormatInt(mon.ID, 10))

	// State: dot + name
	state := monitorStateDot(mon.OverallState) + " " +
		lipgloss.NewStyle().Foreground(monitorStateClr(mon.OverallState)).Bold(true).
			Width(9).Render(mon.OverallState)

	// Priority
	pri := priorityLabel(mon.Priority)

	// Updated
	updated := lipgloss.NewStyle().Foreground(clrDim).Width(7).Align(lipgloss.Right).
		Render(datadog.RelativeTime(mon.Modified))

	// Name fills remaining
	fixedW := 2 + 2 + 9 + 12 + 3 + 7 + 4
	nameW := width - fixedW
	if nameW < 10 {
		nameW = 10
	}
	name := mon.Name
	if len(name) > nameW {
		name = name[:nameW-1] + "…"
	}

	row := fmt.Sprintf("%s%s %s %s %s %s %s",
		cursor, icon, idStr, state, pri,
		lipgloss.NewStyle().Foreground(clrText).Render(name),
		updated)

	if selected {
		row = lipgloss.NewStyle().
			Background(lipgloss.Color("#1E293B")).
			Width(width).MaxWidth(width).
			Render(row)
	} else {
		row = lipgloss.NewStyle().
			Width(width).MaxWidth(width).
			Render(row)
	}

	fmt.Fprint(w, row)
}
