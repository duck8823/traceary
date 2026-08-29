package sqlite_test

import (
	"context"
	"testing"
	"time"

	"github.com/duck8823/traceary/domain/model"
	"github.com/duck8823/traceary/domain/types"
)

func TestSessionDatasource_IsMainSession(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	fx := newSessionRefinementFixture(t)
	base := time.Date(2026, 8, 1, 10, 0, 0, 0, time.UTC)

	save := func(session *model.Session, eventID string) {
		t.Helper()
		start, err := model.NewEventWithClock(
			types.EventID(eventID), types.EventKindSessionStarted, session.Client(), session.Agent(),
			session.SessionID(), session.Workspace(), "start",
			fixedEventClock{at: base},
		)
		if err != nil {
			t.Fatal(err)
		}
		if err := fx.sessions.SaveBoundary(ctx, session, start); err != nil {
			t.Fatal(err)
		}
	}

	plain := model.NewSession("sess-main", base, "cli", "claude", "ws")
	save(plain, "evt-main")

	parent := model.NewSession("sess-parent", base, "cli", "claude", "ws")
	save(parent, "evt-parent")
	child := model.NewChildSession(parent, "sess-child", base, "claude", "ws", "evt-parent", "explore", 1)
	save(child, "evt-child")

	kinded := model.NewChildSession(plain, "sess-kind", base, "claude", "ws", "evt-main", "plan", 1)
	save(kinded, "evt-kind")

	slash := model.NewSession("sess-slash", base, "cli", "claude/explore", "ws")
	save(slash, "evt-slash")

	t.Run("IsMainSession is true for a plain session", func(t *testing.T) {
		got, err := fx.sessions.IsMainSession(ctx, "sess-main")
		if err != nil {
			t.Fatal(err)
		}
		main, ok := got.Value()
		if !ok || !main {
			t.Fatalf("IsMainSession() = %v ok=%v, want true", main, ok)
		}
	})
	t.Run("IsMainSession is false for a child session", func(t *testing.T) {
		got, err := fx.sessions.IsMainSession(ctx, "sess-child")
		if err != nil {
			t.Fatal(err)
		}
		main, ok := got.Value()
		if !ok || main {
			t.Fatalf("IsMainSession() = %v ok=%v, want false", main, ok)
		}
	})
	t.Run("IsMainSession is false for a subagent_kind session", func(t *testing.T) {
		got, err := fx.sessions.IsMainSession(ctx, "sess-kind")
		if err != nil {
			t.Fatal(err)
		}
		main, ok := got.Value()
		if !ok || main {
			t.Fatalf("IsMainSession() = %v ok=%v, want false", main, ok)
		}
	})
	t.Run("IsMainSession is false for a slash agent", func(t *testing.T) {
		got, err := fx.sessions.IsMainSession(ctx, "sess-slash")
		if err != nil {
			t.Fatal(err)
		}
		main, ok := got.Value()
		if !ok || main {
			t.Fatalf("IsMainSession() = %v ok=%v, want false", main, ok)
		}
	})
	t.Run("IsMainSession is None for a missing session", func(t *testing.T) {
		got, err := fx.sessions.IsMainSession(ctx, "missing")
		if err != nil {
			t.Fatal(err)
		}
		if _, ok := got.Value(); ok {
			t.Fatal("IsMainSession() = Some, want None")
		}
	})
}
