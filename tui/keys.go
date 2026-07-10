package tui

import "github.com/charmbracelet/bubbles/key"

type actionKeyMap struct {
	Mute    key.Binding
	Unmute  key.Binding
	Open    key.Binding
	Refresh key.Binding
	Filter  key.Binding
	Back    key.Binding
}

var actionKeys = actionKeyMap{
	Mute:    key.NewBinding(key.WithKeys("m"), key.WithHelp("m", "mute")),
	Unmute:  key.NewBinding(key.WithKeys("u"), key.WithHelp("u", "unmute")),
	Open:    key.NewBinding(key.WithKeys("o"), key.WithHelp("o", "browser")),
	Refresh: key.NewBinding(key.WithKeys("r"), key.WithHelp("r", "refresh")),
	Filter:  key.NewBinding(key.WithKeys("f"), key.WithHelp("f", "filter state")),
	Back:    key.NewBinding(key.WithKeys("esc"), key.WithHelp("esc", "back")),
}

func (k actionKeyMap) shortHelp() []key.Binding {
	return []key.Binding{
		k.Mute, k.Unmute, k.Open, k.Refresh, k.Filter,
	}
}
