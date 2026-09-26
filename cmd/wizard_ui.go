package cmd

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/charmbracelet/huh"
	"github.com/charmbracelet/lipgloss"
	"golang.org/x/term"
)

// Building blocks for the guided flows (setup, doctor): step headers,
// status lines with a fix underneath, a spinner for slow checks, and huh
// forms themed with the terminal's own ANSI colors.

var (
	wzAccent = lipgloss.NewStyle().Foreground(lipgloss.Color("5")).Bold(true)
	wzOK     = lipgloss.NewStyle().Foreground(lipgloss.Color("2"))
	wzWarn   = lipgloss.NewStyle().Foreground(lipgloss.Color("3"))
	wzFail   = lipgloss.NewStyle().Foreground(lipgloss.Color("1")).Bold(true)
	wzMuted  = lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
	wzBold   = lipgloss.NewStyle().Bold(true)
	wzCode   = lipgloss.NewStyle().Foreground(lipgloss.Color("6"))
)

// errCancelled is returned when the user leaves a wizard (esc / ctrl+c).
var errCancelled = errors.New("cancelled")

func stdinIsTTY() bool { return term.IsTerminal(int(os.Stdin.Fd())) }

// interactive reports whether we can ask questions.
func interactive() bool { return isTTY() && stdinIsTTY() }

// wzTheme is huh's ANSI theme, indented to line up with the step headers
// and status lines.
func wzTheme() *huh.Theme {
	t := huh.ThemeBase16()
	t.Form.Base = t.Form.Base.MarginLeft(2)
	return t
}

// ask runs a huh form, mapping an abort to errCancelled.
func ask(fields ...huh.Field) error {
	f := huh.NewForm(huh.NewGroup(fields...)).WithTheme(wzTheme()).WithShowHelp(true)
	if os.Getenv("ACCESSIBLE") != "" {
		f = f.WithAccessible(true)
	}
	if err := f.Run(); err != nil {
		if errors.Is(err, huh.ErrUserAborted) {
			return errCancelled
		}
		return err
	}
	return nil
}

func stepHeader(n, total int, title, subtitle string) {
	fmt.Println()
	fmt.Println("  " + wzAccent.Render(fmt.Sprintf("Step %d of %d", n, total)) + wzMuted.Render("  ·  ") + wzBold.Render(title))
	if subtitle != "" {
		for _, l := range strings.Split(subtitle, "\n") {
			fmt.Println("  " + wzMuted.Render(l))
		}
	}
	fmt.Println()
}

func sayOK(msg string)   { fmt.Println("  " + wzOK.Render("●") + " " + msg) }
func sayInfo(msg string) { fmt.Println("  " + wzMuted.Render("·") + " " + msg) }

// sayWarn prints a warning with an optional fix on the next line.
func sayWarn(msg, fix string) {
	fmt.Println("  " + wzWarn.Render("!") + " " + msg)
	printFix(fix)
}

func sayFail(msg, fix string) {
	fmt.Println("  " + wzFail.Render("×") + " " + msg)
	printFix(fix)
}

func printFix(fix string) {
	if fix == "" {
		return
	}
	for _, l := range strings.Split(fix, "\n") {
		fmt.Println("    " + wzMuted.Render(l))
	}
}

// cmdHint renders a command the user can copy.
func cmdHint(c string) string { return wzCode.Render(c) }

// withSpinner shows "◐ label…" while fn runs, then clears the line. The
// caller prints the outcome.
func withSpinner(label string, fn func() error) error {
	if !isTTY() {
		return fn()
	}
	frames := []string{"◐", "◓", "◑", "◒"}
	done := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		t := time.NewTicker(120 * time.Millisecond)
		defer t.Stop()
		for i := 0; ; i++ {
			fmt.Printf("\r  %s %s", wzAccent.Render(frames[i%len(frames)]), wzMuted.Render(label+"…"))
			select {
			case <-done:
				fmt.Print("\r\x1b[2K")
				return
			case <-t.C:
			}
		}
	}()
	err := fn()
	close(done)
	wg.Wait()
	return err
}
