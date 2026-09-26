package tui

import (
	"os"
	"strings"

	"github.com/charmbracelet/lipgloss"
	colorful "github.com/lucasb-eyer/go-colorful"
	"github.com/muesli/termenv"
)

// The TUI paints with the terminal's own ANSI palette (colors 1-8) so it
// inherits whatever theme the user runs — gruvbox, solarized, catppuccin...
// Only the neutral surfaces (bars, selection, rules) need shades the ANSI
// palette doesn't have; those are blended from the terminal's real background
// and foreground, queried once at startup via OSC 10/11.

type palette struct {
	dark bool

	bg      lipgloss.TerminalColor // terminal background (pill text)
	surface lipgloss.TerminalColor // header/footer bars, widget titles
	sel     lipgloss.TerminalColor // selected row, focused widget
	faint   lipgloss.TerminalColor // rules, borders
	muted   lipgloss.TerminalColor // secondary text
	text    lipgloss.TerminalColor // body text (terminal default)

	accent  lipgloss.TerminalColor
	red     lipgloss.TerminalColor
	green   lipgloss.TerminalColor
	yellow  lipgloss.TerminalColor
	blue    lipgloss.TerminalColor
	magenta lipgloss.TerminalColor
	cyan    lipgloss.TerminalColor
}

// th is the active palette. Tests get the deterministic fallback.
var th = fallbackPalette()

func fallbackPalette() palette {
	return palette{
		dark:    true,
		bg:      lipgloss.Color("#282828"),
		surface: lipgloss.Color("#32302f"),
		sel:     lipgloss.Color("#3c3836"),
		faint:   lipgloss.Color("#595048"),
		muted:   lipgloss.Color("8"),
		text:    lipgloss.NoColor{},
		accent:  lipgloss.Color("5"),
		red:     lipgloss.Color("1"),
		green:   lipgloss.Color("2"),
		yellow:  lipgloss.Color("3"),
		blue:    lipgloss.Color("4"),
		magenta: lipgloss.Color("5"),
		cyan:    lipgloss.Color("6"),
	}
}

// initTheme queries the terminal colors. Must run before Bubble Tea takes
// over stdin, otherwise the OSC replies are swallowed as key presses.
func initTheme() {
	if os.Getenv("NO_COLOR") != "" {
		return
	}
	out := termenv.NewOutput(os.Stdout)
	bg, okBg := rgbOf(out.BackgroundColor())
	if !okBg {
		return
	}
	fg, okFg := rgbOf(out.ForegroundColor())
	_, _, l := bg.Hcl()
	dark := l < 0.5
	if !okFg {
		if dark {
			fg, _ = colorful.Hex("#d4d4d4")
		} else {
			fg, _ = colorful.Hex("#303030")
		}
	}
	mix := func(t float64) lipgloss.Color {
		return lipgloss.Color(bg.BlendLab(fg, t).Clamped().Hex())
	}
	p := fallbackPalette()
	p.dark = dark
	p.bg = lipgloss.Color(bg.Hex())
	p.surface = mix(0.07)
	p.sel = mix(0.13)
	p.faint = mix(0.30)
	p.muted = mix(0.55)
	th = p
}

func rgbOf(c termenv.Color) (colorful.Color, bool) {
	rgb, ok := c.(termenv.RGBColor)
	if !ok {
		return colorful.Color{}, false
	}
	col, err := colorful.Hex(string(rgb))
	if err != nil {
		return colorful.Color{}, false
	}
	return col, true
}

// ─── style shorthands ────────────────────────────────────────────

func fg(c lipgloss.TerminalColor) lipgloss.Style { return lipgloss.NewStyle().Foreground(c) }

func sMuted() lipgloss.Style  { return fg(th.muted) }
func sFaint() lipgloss.Style  { return fg(th.faint) }
func sAccent() lipgloss.Style { return fg(th.accent) }
func sBold() lipgloss.Style   { return lipgloss.NewStyle().Bold(true) }

func lipglossStyle() lipgloss.Style { return lipgloss.NewStyle() }

// pill renders a compact colored label: dark text on a colored background.
func pill(text string, bg lipgloss.TerminalColor) string {
	return lipgloss.NewStyle().Foreground(th.bg).Background(bg).Bold(true).Padding(0, 1).Render(text)
}

// ─── glyphs ──────────────────────────────────────────────────────
//
// Every glyph below exists in common programming fonts (checked against Fira
// Mono): missing glyphs fall back to other fonts and break the column grid.

const (
	gSel      = "▎"
	gBullet   = "•"
	gDot      = "·"
	gEllipsis = "…"
	gArrow    = "→"
	gUp       = "↑"
	gDown     = "↓"
	gOpen     = "▼"
	gClosed   = "►"
	gRule     = "─"
	gEnter    = "⏎"
	gBar      = "▌"
)

var spinnerFrames = []string{"◐", "◓", "◑", "◒"}

// ─── Datadog semantics ───────────────────────────────────────────

// monitorState normalizes Datadog's overall_state values.
func monitorState(s string) string {
	switch strings.ToLower(s) {
	case "alert":
		return "Alert"
	case "warn":
		return "Warn"
	case "no data":
		return "No Data"
	case "ok":
		return "OK"
	case "skipped":
		return "Skipped"
	case "ignored":
		return "Ignored"
	}
	if s == "" {
		return "Unknown"
	}
	return s
}

// stateRank orders monitor groups: what needs you first.
func stateRank(state string) int {
	switch monitorState(state) {
	case "Alert":
		return 0
	case "Warn":
		return 1
	case "No Data":
		return 2
	case "Unknown", "Skipped", "Ignored":
		return 3
	}
	return 4 // OK
}

func stateColor(state string) lipgloss.TerminalColor {
	switch monitorState(state) {
	case "Alert":
		return th.red
	case "Warn":
		return th.yellow
	case "No Data":
		return th.magenta
	case "OK":
		return th.green
	}
	return th.muted
}

func stateGlyph(state string) string {
	switch monitorState(state) {
	case "Alert":
		return "●"
	case "Warn":
		return "◐"
	case "No Data":
		return "◇"
	case "OK":
		return "○"
	}
	return "·"
}

func stateIcon(state string) string { return fg(stateColor(state)).Render(stateGlyph(state)) }

// logStatusColor colors a log line by its status.
func logStatusColor(status string) lipgloss.TerminalColor {
	switch strings.ToLower(status) {
	case "emergency", "alert", "critical", "error", "err":
		return th.red
	case "warn", "warning":
		return th.yellow
	case "notice", "info":
		return th.blue
	case "debug", "trace":
		return th.muted
	case "ok", "success":
		return th.green
	}
	return th.muted
}
