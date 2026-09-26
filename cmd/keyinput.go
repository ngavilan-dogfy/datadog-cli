package cmd

import (
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/ngavilan-dogfy/datadog-cli/config"

	"github.com/atotto/clipboard"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/huh"
)

// keyPrompt asks for a secret (an API key, a token) and, while it waits,
// watches the clipboard: the moment the user clicks Copy on the key in the
// browser, it's taken — no pasting. Typing or pasting still works.
type keyPrompt struct {
	Title string
	Hint  string
	Clean func(string) string // normalizes a paste (spaces, newlines…)
	Match func(string) bool   // what a key looks like, after Clean
	Label string              // "API key", for "Found an API key in your clipboard"
	Skip  map[string]bool     // keys already tried and refused: not offered again
}

// readClipboard is swappable in tests; errors mean "no clipboard here".
var readClipboard = clipboard.ReadAll

// tookFromClipboard remembers keys taken from the clipboard, to clear them
// from it once they're saved.
var tookFromClipboard []string

type clipMsg struct {
	text string
	err  error
}

type keyModel struct {
	p        keyPrompt
	input    textinput.Model
	seen     string // clipboard content when we last looked
	offered  string // a key already in the clipboard when we started
	watching bool
	value    string
	fromClip bool
	err      error
	frame    int
}

func (m *keyModel) Init() tea.Cmd { return tea.Batch(textinput.Blink, m.poll(0)) }

func (m *keyModel) poll(after time.Duration) tea.Cmd {
	return tea.Tick(after, func(time.Time) tea.Msg {
		s, err := readClipboard()
		return clipMsg{s, err}
	})
}

func (m *keyModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case clipMsg:
		m.frame++
		if msg.err != nil {
			m.watching = false
			return m, nil
		}
		first := !m.watching && m.seen == "" && m.frame == 1
		m.watching = true
		if first {
			m.seen = msg.text
			if k := m.p.Clean(msg.text); m.p.Match(k) && !m.p.Skip[k] {
				m.offered = k
			}
			return m, m.poll(400 * time.Millisecond)
		}
		if msg.text != m.seen {
			m.seen = msg.text
			if k := m.p.Clean(msg.text); m.p.Match(k) && !m.p.Skip[k] {
				m.value, m.fromClip = k, true
				return m, tea.Quit
			}
		}
		return m, m.poll(400 * time.Millisecond)
	case tea.KeyMsg:
		switch msg.Type {
		case tea.KeyCtrlC, tea.KeyEsc:
			m.err = errCancelled
			return m, tea.Quit
		case tea.KeyEnter:
			typed := m.p.Clean(m.input.Value())
			switch {
			case typed != "":
				m.value = typed
				return m, tea.Quit
			case m.offered != "":
				m.value, m.fromClip = m.offered, true
				return m, tea.Quit
			}
			return m, nil
		}
	}
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	return m, cmd
}

func (m *keyModel) View() string {
	if m.value != "" || m.err != nil {
		return ""
	}
	bar := wzAccent.Render("┃")
	var b strings.Builder
	b.WriteString("  " + bar + " " + wzBold.Render(m.p.Title) + "\n")
	if m.p.Hint != "" {
		b.WriteString("  " + bar + " " + wzMuted.Render(m.p.Hint) + "\n")
	}
	b.WriteString("  " + bar + " " + m.input.View() + "\n")
	switch {
	case m.offered != "" && m.input.Value() == "":
		b.WriteString("  " + bar + " " + wzOK.Render("●") + " Your clipboard holds what looks like " + article(m.p.Label) + " " + wzMuted.Render(maskKey(m.offered)) + " — enter tries it\n")
	case m.watching:
		frames := []string{"◐", "◓", "◑", "◒"}
		b.WriteString("  " + bar + " " + wzAccent.Render(frames[m.frame%len(frames)]) + wzMuted.Render(" Watching your clipboard: click Copy on the key and I'll take it from there") + "\n")
	}
	b.WriteString("  " + bar + " " + wzMuted.Render("enter confirm · esc cancel") + "\n")
	return b.String()
}

func article(label string) string {
	if label != "" && strings.ContainsRune("AEIOUaeiou", rune(label[0])) {
		return "an " + label
	}
	return "a " + label
}

// maskKey shows only the end of a secret, like the rest of setup: ****a1b2.
func maskKey(k string) string { return config.Mask(k) }

// readKey runs the prompt. Without a real terminal (or with ACCESSIBLE set)
// it falls back to a plain hidden input.
func readKey(p keyPrompt) (string, error) {
	if os.Getenv("ACCESSIBLE") != "" || !interactive() {
		key := ""
		err := ask(huh.NewInput().Title(p.Title).Description(p.Hint).EchoMode(huh.EchoModePassword).Value(&key).
			Validate(func(s string) error {
				if p.Clean(s) == "" {
					return fmt.Errorf("paste the %s", p.Label)
				}
				return nil
			}))
		return p.Clean(key), err
	}
	in := textinput.New()
	in.EchoMode = textinput.EchoPassword
	in.EchoCharacter = '•'
	in.Placeholder = "paste it here, or just copy it in the browser"
	in.Prompt = "> "
	in.Focus()
	m := &keyModel{p: p, input: in}
	final, err := tea.NewProgram(m).Run()
	if err != nil {
		return "", err
	}
	fm := final.(*keyModel)
	if fm.err != nil {
		return "", fm.err
	}
	if fm.fromClip {
		tookFromClipboard = append(tookFromClipboard, fm.value)
		sayInfo("Took " + article(p.Label) + " from your clipboard " + wzMuted.Render(maskKey(fm.value)))
	}
	return fm.value, nil
}

// clearTakenKeys empties the clipboard when it still holds a key we took:
// a key left there ends up pasted where it shouldn't.
func clearTakenKeys() {
	if len(tookFromClipboard) == 0 {
		return
	}
	defer func() { tookFromClipboard = nil }()
	cur, err := readClipboard()
	if err != nil {
		return
	}
	for _, k := range tookFromClipboard {
		if strings.Contains(strings.ToLower(cur), k) || strings.Contains(cur, k) {
			if clipboard.WriteAll("") == nil {
				sayInfo("Cleared the key from your clipboard")
			}
			return
		}
	}
}
