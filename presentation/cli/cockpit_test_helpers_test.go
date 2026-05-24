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
		// Deliberately ignore follow-up commands here: Tail auto-follow can
		// return tea.Tick, and recursively draining it would create a polling
		// loop. Tests that need follow-up side effects should inspect the
		// returned command directly.
		updated, _ := model.Update(msg)
		next, ok := updated.(cockpitModel)
		if !ok {
			t.Fatalf("cockpit update returned %T, want cockpitModel", updated)
		}
		return next
	}
}

func runFirstCockpitBatchCommandForTest(t *testing.T, cmd tea.Cmd) {
	t.Helper()
	if cmd == nil {
		t.Fatalf("command = nil, want tea.BatchMsg")
	}
	msg := cmd()
	batch, ok := msg.(tea.BatchMsg)
	if !ok {
		t.Fatalf("command message = %T, want tea.BatchMsg", msg)
	}
	if len(batch) == 0 || batch[0] == nil {
		t.Fatalf("batch first command missing: %#v", batch)
	}
	if followMsg := batch[0](); followMsg != nil {
		t.Fatalf("batch first command returned message = %T, want nil", followMsg)
	}
}
