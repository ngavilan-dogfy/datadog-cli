package cmd

import (
	"fmt"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"datadog-cli/datadog"
	"datadog-cli/ui"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/lipgloss/table"
)

func watchMonitors(state string, monitorType string, limit int, interval time.Duration) error {
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	// Initial render
	renderWatchFrame(state, monitorType, limit)

	for {
		select {
		case <-sigCh:
			fmt.Println()
			fmt.Println(ui.Dimmed.Render("  Stopped watching."))
			return nil
		case <-ticker.C:
			renderWatchFrame(state, monitorType, limit)
		}
	}
}

func renderWatchFrame(state string, monitorType string, limit int) {
	// Clear screen
	fmt.Print("\033[2J\033[H")

	monitors, err := client.ListMonitors("", limit)
	if err != nil {
		fmt.Println(ui.ErrorStyle.Render(fmt.Sprintf("  Error: %s", err)))
		return
	}

	if state != "" {
		monitors = filterMonitorsByState(monitors, state)
	}
	if monitorType != "" {
		monitors = filterMonitorsByType(monitors, monitorType)
	}

	// Count by state
	counts := map[string]int{}
	for _, m := range monitors {
		counts[m.OverallState]++
	}

	header := " Monitors"
	if state != "" {
		header += " · " + state
	}
	header += fmt.Sprintf("  [%s]", time.Now().Format("15:04:05"))
	fmt.Println(ui.Title.Render(header))

	// Summary line
	parts := []string{}
	for _, s := range []string{"OK", "Alert", "Warn", "No Data"} {
		if c, ok := counts[s]; ok && c > 0 {
			color := ui.MonitorStateColor(s)
			parts = append(parts, lipgloss.NewStyle().Foreground(color).Bold(true).Render(fmt.Sprintf("%s:%d", s, c)))
		}
	}
	if len(parts) > 0 {
		fmt.Println("  " + strings.Join(parts, "  "))
	}

	if len(monitors) == 0 {
		fmt.Println(ui.Dimmed.Render("  No monitors found."))
		return
	}

	var rows [][]string
	for _, m := range monitors {
		name := m.Name
		if len(name) > 55 {
			name = name[:52] + "..."
		}
		pri := "—"
		if m.Priority != nil {
			pri = fmt.Sprintf("P%d", *m.Priority)
		}

		rows = append(rows, []string{
			strconv.FormatInt(m.ID, 10),
			ui.MonitorTypeIcon(m.Type),
			m.OverallState,
			pri,
			name,
			datadog.RelativeTime(m.Modified),
		})
	}

	t := table.New().
		Headers("ID", "", "STATE", "PRI", "NAME", "MODIFIED").
		Rows(rows...).
		Border(lipgloss.RoundedBorder()).
		BorderStyle(lipgloss.NewStyle().Foreground(ui.Subtle)).
		StyleFunc(func(row, col int) lipgloss.Style {
			if row == table.HeaderRow {
				return lipgloss.NewStyle().Bold(true).Foreground(ui.Secondary).Padding(0, 1)
			}
			s := lipgloss.NewStyle().Padding(0, 1)
			switch col {
			case 0:
				s = s.Bold(true).Foreground(lipgloss.Color("#FFFFFF")).Width(10)
			case 1:
				s = s.Width(2).Padding(0, 0)
			case 2:
				if row >= 0 && row < len(monitors) {
					s = s.Foreground(ui.MonitorStateColor(monitors[row].OverallState)).Bold(true)
				}
			case 3:
				s = s.Width(4).Foreground(ui.Muted)
			case 4:
				s = s.Width(57).Foreground(ui.Text)
			case 5:
				s = s.Foreground(ui.Muted)
			}
			return s
		})

	fmt.Println(t)
	fmt.Println(ui.Dimmed.Render(fmt.Sprintf("  %d monitors · refreshing every 10s · Ctrl+C to stop", len(monitors))))
}
