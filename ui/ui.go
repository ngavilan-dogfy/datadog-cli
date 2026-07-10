package ui

import "github.com/charmbracelet/lipgloss"

// NO_COLOR is automatically respected by lipgloss via the colorprofile library.

var (
	Primary   = lipgloss.Color("#632CA6") // Datadog purple
	Secondary = lipgloss.Color("#06B6D4")
	Success   = lipgloss.Color("#10B981")
	Warning   = lipgloss.Color("#F59E0B")
	Danger    = lipgloss.Color("#EF4444")
	Muted     = lipgloss.Color("#6B7280")
	Text      = lipgloss.Color("#E5E7EB")
	Subtle    = lipgloss.Color("#374151")

	Title = lipgloss.NewStyle().
		Bold(true).
		Foreground(Primary).
		MarginBottom(1)

	Subtitle = lipgloss.NewStyle().
		Bold(true).
		Foreground(Secondary)

	Key = lipgloss.NewStyle().
		Bold(true).
		Foreground(lipgloss.Color("#FFFFFF"))

	Label = lipgloss.NewStyle().
		Foreground(Muted).
		Width(14)

	Value = lipgloss.NewStyle().
		Foreground(Text)

	SuccessStyle = lipgloss.NewStyle().
		Foreground(Success).
		Bold(true)

	ErrorStyle = lipgloss.NewStyle().
		Foreground(Danger).
		Bold(true)

	Dimmed = lipgloss.NewStyle().
		Foreground(Muted)

	SectionHeader = lipgloss.NewStyle().
		Bold(true).
		Foreground(Secondary).
		BorderBottom(true).
		BorderStyle(lipgloss.NormalBorder()).
		BorderForeground(Subtle).
		MarginTop(1).
		MarginBottom(1)
)

// MonitorStateColor maps Datadog monitor overall_state to colors.
func MonitorStateColor(state string) lipgloss.Color {
	switch state {
	case "OK":
		return Success
	case "Alert":
		return Danger
	case "Warn":
		return Warning
	case "No Data":
		return Muted
	case "Ignored", "Skipped":
		return lipgloss.Color("#6366F1")
	default:
		return lipgloss.Color("#A78BFA")
	}
}

// MonitorStateBadge renders a colored badge for monitor state.
func MonitorStateBadge(state string) string {
	return lipgloss.NewStyle().
		Foreground(lipgloss.Color("#000000")).
		Background(MonitorStateColor(state)).
		Bold(true).
		Padding(0, 1).
		Render(state)
}

// MonitorTypeIcon returns a colored icon for monitor type.
func MonitorTypeIcon(monitorType string) string {
	switch monitorType {
	case "metric":
		return lipgloss.NewStyle().Foreground(Secondary).Render("◆")
	case "query alert":
		return lipgloss.NewStyle().Foreground(Secondary).Render("◆")
	case "service check":
		return lipgloss.NewStyle().Foreground(Success).Render("●")
	case "host":
		return lipgloss.NewStyle().Foreground(Warning).Render("■")
	case "process":
		return lipgloss.NewStyle().Foreground(lipgloss.Color("#F97316")).Render("▲")
	case "log alert":
		return lipgloss.NewStyle().Foreground(lipgloss.Color("#3B82F6")).Render("◎")
	case "synthetics alert":
		return lipgloss.NewStyle().Foreground(Primary).Render("◎")
	case "apm":
		return lipgloss.NewStyle().Foreground(lipgloss.Color("#EC4899")).Render("◆")
	case "composite":
		return lipgloss.NewStyle().Foreground(lipgloss.Color("#8B5CF6")).Render("◎")
	case "rum alert":
		return lipgloss.NewStyle().Foreground(lipgloss.Color("#F97316")).Render("●")
	default:
		return lipgloss.NewStyle().Foreground(Muted).Render("○")
	}
}

// SeverityColor returns a color for incident severity.
func SeverityColor(severity string) lipgloss.Color {
	switch severity {
	case "SEV-1":
		return Danger
	case "SEV-2":
		return lipgloss.Color("#F97316")
	case "SEV-3":
		return Warning
	case "SEV-4":
		return Success
	case "SEV-5":
		return Muted
	default:
		return Text
	}
}

// SeverityBadge renders a severity label.
func SeverityBadge(severity string) string {
	return lipgloss.NewStyle().
		Foreground(SeverityColor(severity)).
		Bold(true).
		Render(severity)
}

// IncidentStatusColor maps incident status to colors.
func IncidentStatusColor(status string) lipgloss.Color {
	switch status {
	case "active":
		return Danger
	case "stable":
		return Warning
	case "resolved":
		return Success
	default:
		return Muted
	}
}

// HostStatusBadge renders a host up/down badge.
func HostStatusBadge(up bool) string {
	if up {
		return lipgloss.NewStyle().Foreground(Success).Bold(true).Render("UP")
	}
	return lipgloss.NewStyle().Foreground(Danger).Bold(true).Render("DOWN")
}

// LogStatusColor maps log status to colors.
func LogStatusColor(status string) lipgloss.Color {
	switch status {
	case "error", "critical", "emergency", "alert":
		return Danger
	case "warn", "warning":
		return Warning
	case "info", "notice":
		return Secondary
	case "debug":
		return Muted
	case "ok":
		return Success
	default:
		return Text
	}
}

// EventAlertTypeColor maps event alert_type to colors.
func EventAlertTypeColor(alertType string) lipgloss.Color {
	switch alertType {
	case "error":
		return Danger
	case "warning":
		return Warning
	case "success":
		return Success
	case "info":
		return Secondary
	default:
		return Muted
	}
}

// SLOStatusColor maps SLO status to colors.
func SLOStatusColor(status string) lipgloss.Color {
	switch status {
	case "OK", "ok":
		return Success
	case "WARNING", "warning":
		return Warning
	case "BREACHED", "breached":
		return Danger
	default:
		return Muted
	}
}

// PriorityLabel returns a colored priority label for monitors (1-5).
func PriorityLabel(priority *int) string {
	if priority == nil {
		return lipgloss.NewStyle().Foreground(Muted).Render("—")
	}
	switch *priority {
	case 1:
		return lipgloss.NewStyle().Foreground(Danger).Bold(true).Render("P1")
	case 2:
		return lipgloss.NewStyle().Foreground(lipgloss.Color("#F97316")).Bold(true).Render("P2")
	case 3:
		return lipgloss.NewStyle().Foreground(Warning).Bold(true).Render("P3")
	case 4:
		return lipgloss.NewStyle().Foreground(Success).Render("P4")
	case 5:
		return lipgloss.NewStyle().Foreground(Muted).Render("P5")
	default:
		return lipgloss.NewStyle().Foreground(Muted).Render("—")
	}
}
