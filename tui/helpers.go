package tui

import (
	"os/exec"
	"runtime"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

func openBrowser(url string) {
	switch runtime.GOOS {
	case "darwin":
		exec.Command("open", url).Start()
	case "linux":
		exec.Command("xdg-open", url).Start()
	}
}

func hint(k, label string) string {
	return lipgloss.NewStyle().Bold(true).Foreground(clrCyan).Render(k) + " " +
		lipgloss.NewStyle().Foreground(clrMuted).Render(label) + "  "
}

func placeCenter(bg, overlay string, w, h int) string {
	bgLines := strings.Split(bg, "\n")
	for len(bgLines) < h {
		bgLines = append(bgLines, "")
	}

	ovLines := strings.Split(overlay, "\n")
	ovH := len(ovLines)
	ovW := lipgloss.Width(overlay)

	y0 := max(0, (h-ovH)/2)
	x0 := max(0, (w-ovW)/2)

	for i, line := range ovLines {
		y := y0 + i
		if y >= len(bgLines) {
			break
		}
		bgLines[y] = strings.Repeat(" ", x0) + line
	}

	return strings.Join(bgLines, "\n")
}
