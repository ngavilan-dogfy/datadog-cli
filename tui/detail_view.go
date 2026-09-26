package tui

import (
	"fmt"
	"strings"

	"github.com/ngavilan-dogfy/datadog-cli/datadog"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

type detailView struct {
	client   *datadog.Client
	id       int64
	monitor  *datadog.Monitor
	viewport viewport.Model
	width    int
	height   int
	ready    bool
}

func newDetailView(client *datadog.Client, id int64, w, h int) *detailView {
	return &detailView{
		client: client,
		id:     id,
		width:  w,
		height: h,
	}
}

func (v *detailView) Init() tea.Cmd {
	return cmdFetchMonitorDetail(v.client, v.id)
}

func (v *detailView) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {

	case tea.WindowSizeMsg:
		v.width = msg.Width
		v.height = msg.Height
		if v.ready {
			v.viewport.Width = msg.Width
			v.viewport.Height = msg.Height - 4
		}
		return v, nil

	case monitorDetailMsg:
		v.monitor = msg.monitor
		v.initViewport()
		return v, nil

	case tea.KeyMsg:
		switch msg.String() {
		case "esc", "q", "backspace":
			return v, func() tea.Msg { return popMsg{} }
		case "o":
			if v.monitor != nil {
				url := fmt.Sprintf("https://app.datadoghq.eu/monitors/%d", v.monitor.ID)
				openBrowser(url)
			}
			return v, nil
		case "m":
			if v.monitor != nil {
				return v, cmdMuteMonitor(v.client, v.monitor.ID)
			}
		case "u":
			if v.monitor != nil {
				return v, cmdUnmuteMonitor(v.client, v.monitor.ID)
			}
		case "r":
			return v, cmdFetchMonitorDetail(v.client, v.id)
		}
	}

	if v.ready {
		var cmd tea.Cmd
		v.viewport, cmd = v.viewport.Update(msg)
		return v, cmd
	}

	return v, nil
}

func (v *detailView) View() string {
	if v.monitor == nil {
		return lipgloss.NewStyle().
			MarginLeft(2).MarginTop(1).
			Foreground(clrMuted).
			Render("Loading...")
	}

	if !v.ready {
		v.initViewport()
	}

	// Title bar
	title := lipgloss.NewStyle().Bold(true).Foreground(clrPurple).MarginLeft(1).
		Render(fmt.Sprintf("%d  %s", v.monitor.ID, v.monitor.Name))

	// Footer
	footer := lipgloss.NewStyle().MarginLeft(1).Render(
		hint("esc", "back") +
			hint("o", "browser") +
			hint("m", "mute") +
			hint("u", "unmute") +
			hint("r", "refresh") +
			hint("j/k", "scroll"))

	return title + "\n\n" + v.viewport.View() + "\n" + footer
}

func (v *detailView) initViewport() {
	vp := viewport.New(v.width, v.height-4)
	vp.SetContent(v.renderContent())
	v.viewport = vp
	v.ready = true
}

func (v *detailView) renderContent() string {
	m := v.monitor
	if m == nil {
		return ""
	}

	var b strings.Builder
	indent := "  "

	// State
	stateBg := monitorStateClr(m.OverallState)
	stateStyled := lipgloss.NewStyle().
		Foreground(lipgloss.Color("#000000")).
		Background(stateBg).
		Bold(true).Padding(0, 1).
		Render(m.OverallState)
	b.WriteString(indent + "State:       " + stateStyled + "\n")

	// Type
	b.WriteString(indent + "Type:        " + monitorTypeIcon(m.Type) + " " + m.Type + "\n")

	// Priority
	if m.Priority != nil {
		b.WriteString(indent + "Priority:    " + priorityLabel(m.Priority) + "\n")
	}

	// Creator
	b.WriteString(indent + "Creator:     " + lipgloss.NewStyle().Foreground(clrText).Render(m.Creator.Handle) + "\n")

	// Tags
	if len(m.Tags) > 0 {
		tagsStyled := lipgloss.NewStyle().Foreground(clrCyan).Render(strings.Join(m.Tags, ", "))
		b.WriteString(indent + "Tags:        " + tagsStyled + "\n")
	}

	// Downtimes
	if len(m.MatchingDowntimes) > 0 {
		b.WriteString(indent + "Downtimes:   " +
			lipgloss.NewStyle().Foreground(clrYellow).Render(fmt.Sprintf("%d active", len(m.MatchingDowntimes))) + "\n")
	}

	// Times
	b.WriteString(indent + "Created:     " + lipgloss.NewStyle().Foreground(clrMuted).Render(m.Created) + "\n")
	b.WriteString(indent + "Modified:    " + lipgloss.NewStyle().Foreground(clrMuted).Render(datadog.RelativeTime(m.Modified)) + "\n")

	// Query
	if m.Query != "" {
		b.WriteString("\n")
		b.WriteString(indent + lipgloss.NewStyle().Bold(true).Foreground(clrCyan).Render("Query") + "\n")
		b.WriteString(indent + lipgloss.NewStyle().Foreground(clrDim).Render("─────────────────────────") + "\n")
		// Wrap long queries
		query := m.Query
		maxW := v.width - 6
		if maxW > 0 && len(query) > maxW {
			lines := wrapText(query, maxW)
			for _, line := range lines {
				b.WriteString("    " + lipgloss.NewStyle().Foreground(clrText).Render(line) + "\n")
			}
		} else {
			b.WriteString("    " + lipgloss.NewStyle().Foreground(clrText).Render(query) + "\n")
		}
	}

	// Message
	if m.Message != "" {
		b.WriteString("\n")
		b.WriteString(indent + lipgloss.NewStyle().Bold(true).Foreground(clrCyan).Render("Message") + "\n")
		b.WriteString(indent + lipgloss.NewStyle().Foreground(clrDim).Render("─────────────────────────") + "\n")
		msg := m.Message
		if len(msg) > 1000 {
			msg = msg[:997] + "..."
		}
		maxW := v.width - 6
		if maxW > 0 {
			lines := wrapText(msg, maxW)
			for _, line := range lines {
				b.WriteString("    " + lipgloss.NewStyle().Foreground(clrText).Render(line) + "\n")
			}
		} else {
			b.WriteString("    " + lipgloss.NewStyle().Foreground(clrText).Render(msg) + "\n")
		}
	}

	// URL
	b.WriteString("\n")
	b.WriteString(indent + lipgloss.NewStyle().Foreground(clrDim).
		Render(fmt.Sprintf("https://app.datadoghq.eu/monitors/%d", m.ID)) + "\n")

	return b.String()
}

func wrapText(text string, maxWidth int) []string {
	if maxWidth <= 0 {
		return []string{text}
	}
	var lines []string
	for _, paragraph := range strings.Split(text, "\n") {
		if len(paragraph) <= maxWidth {
			lines = append(lines, paragraph)
			continue
		}
		for len(paragraph) > maxWidth {
			// Find last space before maxWidth
			cut := maxWidth
			for i := maxWidth; i > maxWidth/2; i-- {
				if paragraph[i] == ' ' {
					cut = i
					break
				}
			}
			lines = append(lines, paragraph[:cut])
			paragraph = strings.TrimLeft(paragraph[cut:], " ")
		}
		if paragraph != "" {
			lines = append(lines, paragraph)
		}
	}
	return lines
}
