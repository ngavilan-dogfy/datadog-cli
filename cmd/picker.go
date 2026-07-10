package cmd

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
)

// fzfAvailable returns true if fzf is on PATH.
func fzfAvailable() bool {
	_, err := exec.LookPath("fzf")
	return err == nil
}

// fzfPick runs fzf with the given lines (each line is shown to the user) and
// returns the selected line. Returns ErrPickerCancelled if the user aborts.
func fzfPick(lines []string, prompt string) (string, error) {
	if len(lines) == 0 {
		return "", fmt.Errorf("nothing to pick from")
	}
	if !fzfAvailable() {
		return "", fmt.Errorf("fzf not found on PATH — install it or pass the argument explicitly")
	}
	cmd := exec.Command("fzf", "--ansi", "--with-nth=1..", "--delimiter=\t", "--prompt="+prompt)
	cmd.Stdin = strings.NewReader(strings.Join(lines, "\n"))
	cmd.Stderr = os.Stderr
	var out bytes.Buffer
	cmd.Stdout = &out
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("fzf cancelled")
	}
	return strings.TrimSpace(out.String()), nil
}

// pickMonitorID returns a monitor ID, either from args[0] or via fzf.
func pickMonitorID(args []string) (int64, error) {
	if len(args) > 0 && strings.TrimSpace(args[0]) != "" {
		return strconv.ParseInt(args[0], 10, 64)
	}
	monitors, err := client.ListMonitors("", 200)
	if err != nil {
		return 0, fmt.Errorf("list monitors: %w", err)
	}
	var lines []string
	for _, m := range monitors {
		lines = append(lines, fmt.Sprintf("%d\t[%s]\t%s", m.ID, m.OverallState, strings.ReplaceAll(m.Name, "\t", " ")))
	}
	selected, err := fzfPick(lines, "monitor> ")
	if err != nil {
		return 0, err
	}
	parts := strings.SplitN(selected, "\t", 2)
	return strconv.ParseInt(parts[0], 10, 64)
}

// pickDashboardID returns a dashboard ID, either from args[0] or via fzf.
func pickDashboardID(args []string) (string, error) {
	if len(args) > 0 && strings.TrimSpace(args[0]) != "" {
		return args[0], nil
	}
	dashes, err := client.ListDashboards()
	if err != nil {
		return "", err
	}
	var lines []string
	for _, d := range dashes {
		lines = append(lines, fmt.Sprintf("%s\t[%s]\t%s", d.ID, d.LayoutType, strings.ReplaceAll(d.Title, "\t", " ")))
	}
	selected, err := fzfPick(lines, "dashboard> ")
	if err != nil {
		return "", err
	}
	parts := strings.SplitN(selected, "\t", 2)
	return parts[0], nil
}

// pickServiceName returns a service name, either from args[0] or via fzf.
func pickServiceName(args []string) (string, error) {
	if len(args) > 0 && strings.TrimSpace(args[0]) != "" {
		return args[0], nil
	}
	services, err := client.ListServices()
	if err != nil {
		return "", err
	}
	var lines []string
	for _, s := range services {
		name := s.Attributes.Schema.DDService
		if name == "" {
			continue
		}
		tier := s.Attributes.Schema.Tier
		lines = append(lines, fmt.Sprintf("%s\t[%s]\t%s", name, tier, s.Attributes.Schema.Description))
	}
	selected, err := fzfPick(lines, "service> ")
	if err != nil {
		return "", err
	}
	parts := strings.SplitN(selected, "\t", 2)
	return parts[0], nil
}
