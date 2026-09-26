package tui

import (
	"strings"
	"time"

	"github.com/ngavilan-dogfy/datadog-cli/datadog"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// RootModel orchestrates the context stack, shared state, and flash messages.
type RootModel struct {
	client *datadog.Client

	stack []tea.Model

	// Buffer messages that arrive before the stack is initialized
	pendingMsgs []tea.Msg

	width, height int

	flash   string
	flashOK bool
}

func New(client *datadog.Client) RootModel {
	return RootModel{
		client: client,
	}
}

func (m RootModel) Init() tea.Cmd {
	return cmdFetchMonitors(m.client)
}

func (m RootModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {

	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height

		// Initialize the stack on first resize (we now know terminal size)
		firstInit := len(m.stack) == 0
		if firstInit {
			lv := newListView(m.client, m.width, m.height)
			m.stack = []tea.Model{lv}
		}

		var cmds []tea.Cmd
		if firstInit {
			if cmd := m.stack[0].Init(); cmd != nil {
				cmds = append(cmds, cmd)
			}
			for _, pending := range m.pendingMsgs {
				newView, cmd := m.stack[0].Update(pending)
				m.stack[0] = newView
				if cmd != nil {
					cmds = append(cmds, cmd)
				}
			}
			m.pendingMsgs = nil
		}
		for i, v := range m.stack {
			newV, cmd := v.Update(msg)
			m.stack[i] = newV
			if cmd != nil {
				cmds = append(cmds, cmd)
			}
		}
		return m, tea.Batch(cmds...)

	case monitorsMsg, dashboardsMsg, hostsMsg:
		if len(m.stack) == 0 {
			m.pendingMsgs = append(m.pendingMsgs, msg)
			return m, nil
		}
		newView, cmd := m.stack[0].Update(msg)
		m.stack[0] = newView
		return m, cmd

	case monitorDetailMsg:
		return m.forwardToActive(msg)

	case actionDoneMsg:
		m.flash = msg.text
		m.flashOK = true
		return m, tea.Batch(
			cmdClearFlash(3*time.Second),
			cmdFetchMonitors(m.client),
		)

	case errActionMsg:
		s := msg.err.Error()
		if len(s) > 100 {
			s = s[:97] + "..."
		}
		m.flash = s
		m.flashOK = false
		return m, cmdClearFlash(5 * time.Second)

	case clearFlashMsg:
		m.flash = ""
		return m, nil

	case pushMsg:
		cmd := msg.view.Init()
		m.stack = append(m.stack, msg.view)
		return m, cmd

	case popMsg:
		if len(m.stack) > 1 {
			m.stack = m.stack[:len(m.stack)-1]
		}
		return m, nil

	default:
		if len(m.stack) > 0 {
			return m.forwardToActive(msg)
		}
		return m, nil
	}
}

func (m RootModel) View() string {
	if len(m.stack) == 0 || m.width == 0 {
		return ""
	}

	view := m.stack[len(m.stack)-1].View()

	if m.flash != "" {
		view = m.addFlash(view)
	}

	return view
}

func (m RootModel) forwardToActive(msg tea.Msg) (RootModel, tea.Cmd) {
	top := len(m.stack) - 1
	newView, cmd := m.stack[top].Update(msg)
	m.stack[top] = newView
	return m, cmd
}

func (m RootModel) addFlash(view string) string {
	style := lipgloss.NewStyle().Bold(true).Padding(0, 1)
	if m.flashOK {
		style = style.Foreground(lipgloss.Color("#10B981"))
	} else {
		style = style.Foreground(lipgloss.Color("#EF4444"))
	}

	prefix := "OK "
	if !m.flashOK {
		prefix = "ERR "
	}

	flash := style.Render(prefix + m.flash)

	lines := strings.Split(view, "\n")
	if len(lines) >= 2 {
		pos := len(lines) - 2
		lines[pos] = flash
	}

	return strings.Join(lines, "\n")
}
