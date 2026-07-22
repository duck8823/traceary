package model_test

import (
	"testing"
	"time"

	"github.com/duck8823/traceary/domain/model"
	"github.com/duck8823/traceary/domain/types"
)

func TestSessionFromSnapshotMapsLegacyLifecycleConservatively(t *testing.T) {
	t.Parallel()

	startedAt := time.Date(2026, 7, 22, 12, 0, 0, 0, time.UTC)
	endedAt := startedAt.Add(time.Hour)
	active, err := model.SessionFromSnapshot(lifecycleSnapshot(t, startedAt, types.None[time.Time](), types.None[types.TerminalReason]()))
	if err != nil {
		t.Fatalf("SessionFromSnapshot(active) error = %v", err)
	}
	if active.RuntimeMode() != types.RuntimeModeInteractive {
		t.Fatalf("legacy active RuntimeMode() = %q, want interactive", active.RuntimeMode())
	}
	if _, ok := active.TerminalReason().Value(); ok {
		t.Fatal("legacy active TerminalReason() should be empty")
	}

	ended, err := model.SessionFromSnapshot(lifecycleSnapshot(t, startedAt, types.Some(endedAt), types.None[types.TerminalReason]()))
	if err != nil {
		t.Fatalf("SessionFromSnapshot(ended) error = %v", err)
	}
	if reason, ok := ended.TerminalReason().Value(); !ok || reason != types.TerminalReasonLegacyUnknown {
		t.Fatalf("legacy ended TerminalReason() = %q/%v, want legacy_unknown/present", reason, ok)
	}
}

func TestSessionFromSnapshotRejectsInconsistentLifecycleState(t *testing.T) {
	t.Parallel()

	startedAt := time.Date(2026, 7, 22, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		name   string
		mutate func(*model.SessionSnapshot)
	}{
		{name: "active with reason", mutate: func(s *model.SessionSnapshot) {
			s.TerminalReason = types.Some(types.TerminalReasonFailure)
		}},
		{name: "unknown mode", mutate: func(s *model.SessionSnapshot) {
			s.RuntimeMode = types.RuntimeMode("other")
		}},
		{name: "unknown reason", mutate: func(s *model.SessionSnapshot) {
			s.EndedAt = types.Some(startedAt.Add(time.Minute))
			s.TerminalReason = types.Some(types.TerminalReason("other"))
		}},
		{name: "end before start", mutate: func(s *model.SessionSnapshot) {
			s.EndedAt = types.Some(startedAt.Add(-time.Minute))
			s.TerminalReason = types.Some(types.TerminalReasonFailure)
		}},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			snapshot := lifecycleSnapshot(t, startedAt, types.None[time.Time](), types.None[types.TerminalReason]())
			tt.mutate(&snapshot)
			if _, err := model.SessionFromSnapshot(snapshot); err == nil {
				t.Fatal("SessionFromSnapshot() error = nil, want inconsistent-state error")
			}
		})
	}
}

func lifecycleSnapshot(
	t *testing.T,
	startedAt time.Time,
	endedAt types.Optional[time.Time],
	reason types.Optional[types.TerminalReason],
) model.SessionSnapshot {
	t.Helper()
	agent, _ := types.AgentFrom("codex")
	sessionID, _ := types.SessionIDFrom("restore-lifecycle")
	return model.SessionSnapshot{
		SessionID:      sessionID,
		StartedAt:      startedAt,
		EndedAt:        endedAt,
		Client:         types.Client("hook"),
		Agent:          agent,
		Workspace:      types.Workspace("duck8823/traceary"),
		TerminalReason: reason,
	}
}
