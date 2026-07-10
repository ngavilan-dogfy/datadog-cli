package tui

import "github.com/charmbracelet/lipgloss"

var (
	clrPurple = lipgloss.Color("#632CA6") // Datadog purple
	clrCyan   = lipgloss.Color("#06B6D4")
	clrGreen  = lipgloss.Color("#10B981")
	clrYellow = lipgloss.Color("#F59E0B")
	clrRed    = lipgloss.Color("#EF4444")
	clrOrange = lipgloss.Color("#F97316")
	clrBlue   = lipgloss.Color("#3B82F6")
	clrMuted  = lipgloss.Color("#6B7280")
	clrText   = lipgloss.Color("#D1D5DB")
	clrDim    = lipgloss.Color("#4B5563")
	clrWhite  = lipgloss.Color("#F9FAFB")
	clrBorder = lipgloss.Color("#374151")
)

func monitorStateClr(state string) lipgloss.Color {
	switch state {
	case "OK":
		return clrGreen
	case "Alert":
		return clrRed
	case "Warn":
		return clrYellow
	case "No Data":
		return clrMuted
	case "Ignored", "Skipped":
		return lipgloss.Color("#6366F1")
	default:
		return lipgloss.Color("#A78BFA")
	}
}

func monitorStateDot(state string) string {
	return lipgloss.NewStyle().Foreground(monitorStateClr(state)).Render("●")
}

func monitorTypeIcon(t string) string {
	switch t {
	case "metric", "query alert":
		return lipgloss.NewStyle().Foreground(clrCyan).Render("◆")
	case "service check":
		return lipgloss.NewStyle().Foreground(clrGreen).Render("●")
	case "host":
		return lipgloss.NewStyle().Foreground(clrYellow).Render("■")
	case "process":
		return lipgloss.NewStyle().Foreground(clrOrange).Render("▲")
	case "log alert":
		return lipgloss.NewStyle().Foreground(clrBlue).Render("◎")
	case "synthetics alert":
		return lipgloss.NewStyle().Foreground(clrPurple).Render("◎")
	case "composite":
		return lipgloss.NewStyle().Foreground(lipgloss.Color("#8B5CF6")).Render("◎")
	default:
		return lipgloss.NewStyle().Foreground(clrMuted).Render("○")
	}
}

func priorityLabel(p *int) string {
	if p == nil {
		return "  "
	}
	switch *p {
	case 1:
		return lipgloss.NewStyle().Foreground(clrRed).Bold(true).Render("P1")
	case 2:
		return lipgloss.NewStyle().Foreground(clrOrange).Bold(true).Render("P2")
	case 3:
		return lipgloss.NewStyle().Foreground(clrYellow).Render("P3")
	case 4:
		return lipgloss.NewStyle().Foreground(clrGreen).Render("P4")
	case 5:
		return lipgloss.NewStyle().Foreground(clrMuted).Render("P5")
	default:
		return "  "
	}
}
