package tui

import (
	"testing"

	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"
)

// quitOnCtrlCModel demonstrates the testable model/update/view seam used by
// the upcoming inbox-review and top dashboard screens: a model that owns a
// KeyMap and converts the canonical Quit binding into a tea.Quit command.
type quitOnCtrlCModel struct {
	keys KeyMap
}

func (m quitOnCtrlCModel) Init() tea.Cmd { return nil }

func (m quitOnCtrlCModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if k, ok := msg.(tea.KeyMsg); ok {
		if key.Matches(k, m.keys.Quit) {
			return m, tea.Quit
		}
	}
	return m, nil
}

func (m quitOnCtrlCModel) View() string { return "" }

func TestModel_QuitBindingProducesTeaQuit(t *testing.T) {
	model := quitOnCtrlCModel{keys: DefaultKeyMap()}

	cases := []tea.KeyMsg{
		{Type: tea.KeyCtrlC},
		{Type: tea.KeyRunes, Runes: []rune{'q'}},
		{Type: tea.KeyEsc},
	}
	for _, msg := range cases {
		_, cmd := model.Update(msg)
		if cmd == nil {
			t.Fatalf("expected Quit cmd for %#v, got nil", msg)
		}
		if _, ok := cmd().(tea.QuitMsg); !ok {
			t.Fatalf("expected tea.QuitMsg for %#v, got %T", msg, cmd())
		}
	}
}
