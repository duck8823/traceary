package cli

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func applyCockpitImmediateCommandForTest(t *testing.T, model cockpitModel, cmd tea.Cmd) cockpitModel {
	t.Helper()
	if cmd == nil {
		return model
	}
	return applyCockpitImmediateMessageForTest(t, model, cmd())
}

func applyCockpitImmediateMessageForTest(t *testing.T, model cockpitModel, msg tea.Msg) cockpitModel {
	t.Helper()
	switch msg := msg.(type) {
	case nil:
		return model
	case tea.BatchMsg:
		for _, batchedCmd := range msg {
			model = applyCockpitImmediateCommandForTest(t, model, batchedCmd)
		}
		return model
	default:
		updated, _ := model.Update(msg)
		next, ok := updated.(cockpitModel)
		if !ok {
			t.Fatalf("cockpit update returned %T, want cockpitModel", updated)
		}
		return next
	}
}
