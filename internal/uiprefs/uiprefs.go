// Package uiprefs keeps the person's display preferences (chart style…) in
// ~/.config/datadog-cli/ui.json, shared by the TUI and the commands.
package uiprefs

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	"github.com/ngavilan-dogfy/datadog-cli/config"
	"github.com/ngavilan-dogfy/datadog-cli/viz"
)

type prefs struct {
	Charts string `json:"charts,omitempty"` // braille | blocks
}

func path() string { return filepath.Join(config.Dir(), "ui.json") }

func load() prefs {
	var p prefs
	if data, err := os.ReadFile(path()); err == nil {
		_ = json.Unmarshal(data, &p)
	}
	return p
}

func save(p prefs) error {
	data, err := json.MarshalIndent(p, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(config.Dir(), 0o700); err != nil {
		return err
	}
	return os.WriteFile(path(), data, 0o600)
}

// ChartStyle is "braille" or "blocks": $DATADOG_CHARTS, else the saved
// choice, else braille only in terminals that draw braille themselves
// (kitty, Ghostty, WezTerm) — elsewhere it depends on the font.
func ChartStyle() string {
	if v := strings.ToLower(os.Getenv("DATADOG_CHARTS")); v == "braille" || v == "blocks" {
		return v
	}
	if p := load(); p.Charts == "braille" || p.Charts == "blocks" {
		return p.Charts
	}
	switch {
	case os.Getenv("KITTY_WINDOW_ID") != "", os.Getenv("TERM_PROGRAM") == "ghostty", os.Getenv("TERM_PROGRAM") == "WezTerm":
		return "braille"
	}
	return "blocks"
}

// SetChartStyle saves the choice.
func SetChartStyle(style string) error {
	p := load()
	p.Charts = style
	return save(p)
}

// VizStyle is ChartStyle as a viz.Style.
func VizStyle() viz.Style {
	if ChartStyle() == "braille" {
		return viz.Braille
	}
	return viz.Blocks
}
