package cmd

import (
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/ngavilan-dogfy/datadog-cli/datadog"
	"github.com/ngavilan-dogfy/datadog-cli/ui"

	"github.com/charmbracelet/lipgloss"
)

func tailLogs(query string, interval time.Duration, jsonOutput bool) error {
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	if isTTY() && !jsonOutput {
		fmt.Println(ui.Title.Render(fmt.Sprintf(" Tailing logs · %s", query)))
		fmt.Println(ui.Dimmed.Render(fmt.Sprintf("  Polling every %s · Ctrl+C to stop", interval)))
		fmt.Println()
	}

	seen := make(map[string]bool)
	cursor := time.Now().Add(-30 * time.Second)

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	// Initial fetch
	if err := tailFetch(query, cursor, seen, jsonOutput); err != nil {
		fmt.Fprintf(os.Stderr, "  %s %s\n", ui.ErrorStyle.Render("error:"), err)
	}

	for {
		select {
		case <-sigCh:
			if isTTY() && !jsonOutput {
				fmt.Println()
				fmt.Println(ui.Dimmed.Render("  Stopped."))
			}
			return nil
		case <-ticker.C:
			cursor = time.Now().Add(-interval - 5*time.Second)
			if err := tailFetch(query, cursor, seen, jsonOutput); err != nil {
				fmt.Fprintf(os.Stderr, "  %s %s\n", ui.ErrorStyle.Render("error:"), err)
			}
		}
	}
}

func tailFetch(query string, since time.Time, seen map[string]bool, jsonOutput bool) error {
	from := since.Format(time.RFC3339)
	to := time.Now().Format(time.RFC3339)

	result, err := client.SearchLogs(query, from, to, 50)
	if err != nil {
		return err
	}

	// Process in reverse order (oldest first)
	for i := len(result.Data) - 1; i >= 0; i-- {
		log := result.Data[i]
		if seen[log.ID] {
			continue
		}
		seen[log.ID] = true

		if jsonOutput {
			printJSON(logJSONOut{
				ID:        log.ID,
				Timestamp: log.Attributes.Timestamp,
				Status:    log.Attributes.Status,
				Host:      log.Attributes.Host,
				Service:   log.Attributes.Service,
				Message:   log.Attributes.Message,
			})
		} else {
			renderTailLine(log)
		}
	}

	// Evict old entries from seen map
	if len(seen) > 5000 {
		seen = make(map[string]bool)
	}

	return nil
}

func renderTailLine(log datadog.LogData) {
	ts := log.Attributes.Timestamp
	if t := datadog.ParseTime(ts); !t.IsZero() {
		ts = t.Format("15:04:05")
	}

	statusColor := ui.LogStatusColor(log.Attributes.Status)
	status := lipgloss.NewStyle().Foreground(statusColor).Bold(true).Width(8).Render(log.Attributes.Status)

	host := log.Attributes.Host
	if len(host) > 20 {
		host = host[:17] + "..."
	}
	hostStyled := lipgloss.NewStyle().Foreground(ui.Muted).Width(20).Render(host)

	service := log.Attributes.Service
	if len(service) > 15 {
		service = service[:12] + "..."
	}
	serviceStyled := lipgloss.NewStyle().Foreground(ui.Secondary).Width(15).Render(service)

	msg := log.Attributes.Message
	msg = strings.ReplaceAll(msg, "\n", " ")
	if len(msg) > 100 {
		msg = msg[:97] + "..."
	}

	tsStyled := lipgloss.NewStyle().Foreground(ui.Muted).Render(ts)
	fmt.Printf("%s %s %s %s %s\n", tsStyled, status, hostStyled, serviceStyled, msg)
}
