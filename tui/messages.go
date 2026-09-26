package tui

import (
	"github.com/ngavilan-dogfy/datadog-cli/datadog"

	tea "github.com/charmbracelet/bubbletea"
)

// Navigation messages
type pushMsg struct{ view tea.Model }
type popMsg struct{}

// Data messages
type monitorsMsg struct {
	monitors []datadog.Monitor
}
type monitorDetailMsg struct {
	monitor *datadog.Monitor
}

// Action messages
type actionDoneMsg struct{ text string }
type errActionMsg struct{ err error }
type clearFlashMsg struct{}
