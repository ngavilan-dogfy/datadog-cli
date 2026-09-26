package cmd

import (
	"strings"
	"testing"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
)

func newKeyModel() *keyModel {
	in := textinput.New()
	in.Focus()
	return &keyModel{p: keyPrompt{Title: "Your API key", Label: "API key", Clean: cleanKey, Match: reAPIKey.MatchString}, input: in}
}

const fakeAPIKey = "0123456789abcdef0123456789abcdef"

func TestKeyPromptTakesACopiedKey(t *testing.T) {
	m := newKeyModel()
	// The clipboard had something else when the prompt opened.
	m.Update(clipMsg{text: "some text"})
	if m.value != "" || !m.watching {
		t.Fatalf("nothing to take yet: %+v", m)
	}
	if !strings.Contains(m.View(), "Watching your clipboard") {
		t.Errorf("should say it watches the clipboard:\n%s", m.View())
	}
	// The user clicks Copy in Datadog (with a stray newline).
	_, cmd := m.Update(clipMsg{text: " " + strings.ToUpper(fakeAPIKey) + "\n"})
	if m.value != fakeAPIKey || !m.fromClip {
		t.Fatalf("should take the copied key, got %q", m.value)
	}
	if cmd == nil {
		t.Fatal("should quit once it has the key")
	}
}

func TestKeyPromptOffersButDoesntTakeAnOldKey(t *testing.T) {
	m := newKeyModel()
	m.Update(clipMsg{text: fakeAPIKey}) // already there when the prompt opened
	if m.value != "" {
		t.Fatal("a key already in the clipboard is offered, not taken")
	}
	if !strings.Contains(m.View(), "enter tries it") {
		t.Errorf("should offer it:\n%s", m.View())
	}
	m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if m.value != fakeAPIKey {
		t.Errorf("enter should use the offered key, got %q", m.value)
	}
}

func TestKeyPromptTypedAndNoClipboard(t *testing.T) {
	m := newKeyModel()
	m.Update(clipMsg{err: errCancelled}) // no clipboard on this machine
	if m.watching || strings.Contains(m.View(), "Watching") {
		t.Error("without a clipboard it shouldn't claim to watch it")
	}
	m.input.SetValue("abc")
	m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if m.value != "abc" || m.fromClip {
		t.Errorf("typed value: %q fromClip=%v", m.value, m.fromClip)
	}
	c := newKeyModel()
	c.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if c.err != errCancelled {
		t.Error("esc cancels")
	}
}

func TestClearTakenKeys(t *testing.T) {
	orig := readClipboard
	defer func() { readClipboard = orig; tookFromClipboard = nil }()
	readClipboard = func() (string, error) { return "unrelated", nil }
	tookFromClipboard = []string{fakeAPIKey}
	clearTakenKeys() // the clipboard moved on: leave it alone (and don't touch the real one)
	if tookFromClipboard != nil {
		t.Error("the list is emptied either way")
	}
	if maskKey(fakeAPIKey) != "****cdef" {
		t.Errorf("maskKey = %q", maskKey(fakeAPIKey))
	}
}
