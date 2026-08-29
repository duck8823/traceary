package sqlite_test

import (
	"context"
	"testing"
	"time"

	"github.com/duck8823/traceary/domain/model"
	"github.com/duck8823/traceary/domain/types"
)

func TestEventDatasource_CountKindAfter(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	fx := newSessionRefinementFixture(t)
	sessionID := types.SessionID("sess-count")
	otherID := types.SessionID("sess-other")
	base := time.Date(2026, 8, 1, 10, 0, 0, 0, time.UTC)

	session := model.NewSession(sessionID, base, "cli", "codex", "ws")
	start, err := model.NewEventWithClock(
		"evt-start", types.EventKindSessionStarted, "cli", "codex", sessionID, "ws", "start",
		fixedEventClock{at: base.Add(-time.Second)},
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := fx.sessions.SaveBoundary(ctx, session, start); err != nil {
		t.Fatal(err)
	}
	other := model.NewSession(otherID, base, "cli", "codex", "ws")
	otherStart, err := model.NewEventWithClock(
		"evt-other-start", types.EventKindSessionStarted, "cli", "codex", otherID, "ws", "start",
		fixedEventClock{at: base},
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := fx.sessions.SaveBoundary(ctx, other, otherStart); err != nil {
		t.Fatal(err)
	}

	seeds := []struct {
		id   string
		kind types.EventKind
		sid  types.SessionID
		at   time.Time
	}{
		{id: "evt-whole", kind: types.EventKindCommandExecuted, sid: sessionID, at: base},
		{id: "evt-frac", kind: types.EventKindCommandExecuted, sid: sessionID, at: base.Add(500 * time.Millisecond)},
		{id: "evt-later", kind: types.EventKindCommandExecuted, sid: sessionID, at: base.Add(2 * time.Second)},
		{id: "evt-note", kind: types.EventKindNote, sid: sessionID, at: base.Add(3 * time.Second)},
		{id: "evt-other", kind: types.EventKindCommandExecuted, sid: otherID, at: base.Add(3 * time.Second)},
	}
	for _, seed := range seeds {
		event, err := model.NewEventWithClock(
			types.EventID(seed.id), seed.kind, "cli", "codex", seed.sid, "ws", "x",
			fixedEventClock{at: seed.at},
		)
		if err != nil {
			t.Fatal(err)
		}
		if err := fx.events.Save(ctx, event); err != nil {
			t.Fatal(err)
		}
	}

	t.Run("CountKindAfter counts strictly after the boundary under (created_at_norm, id) order", func(t *testing.T) {
		got, err := fx.events.CountKindAfter(ctx, sessionID, types.EventKindCommandExecuted, types.Some(types.EventID("evt-whole")))
		if err != nil {
			t.Fatalf("CountKindAfter() error = %v", err)
		}
		if got != 2 {
			t.Fatalf("CountKindAfter() = %d, want 2 (frac+later)", got)
		}
	})
	t.Run("CountKindAfter with no boundary counts the whole session", func(t *testing.T) {
		got, err := fx.events.CountKindAfter(ctx, sessionID, types.EventKindCommandExecuted, types.None[types.EventID]())
		if err != nil {
			t.Fatalf("CountKindAfter() error = %v", err)
		}
		if got != 3 {
			t.Fatalf("CountKindAfter() = %d, want 3", got)
		}
	})
	t.Run("CountKindAfter ignores other sessions and other kinds", func(t *testing.T) {
		got, err := fx.events.CountKindAfter(ctx, sessionID, types.EventKindNote, types.None[types.EventID]())
		if err != nil {
			t.Fatalf("CountKindAfter() error = %v", err)
		}
		if got != 1 {
			t.Fatalf("CountKindAfter() = %d, want 1", got)
		}
	})
}

func TestEventDatasource_CountKindAfter_EmptySession(t *testing.T) {
	t.Parallel()
	fx := newSessionRefinementFixture(t)
	got, err := fx.events.CountKindAfter(
		context.Background(),
		"missing",
		types.EventKindCommandExecuted,
		types.None[types.EventID](),
	)
	if err != nil {
		t.Fatalf("CountKindAfter() error = %v", err)
	}
	if got != 0 {
		t.Fatalf("CountKindAfter() = %d, want 0", got)
	}
}
