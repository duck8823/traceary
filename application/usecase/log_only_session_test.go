package usecase_test

import (
	"context"
	"database/sql"
	"errors"
	apptypes "github.com/duck8823/traceary/application/types"
	"testing"
	"time"

	"github.com/duck8823/traceary/application/usecase"
	"github.com/duck8823/traceary/domain/model"
	"github.com/duck8823/traceary/domain/types"
	sqlite "github.com/duck8823/traceary/infrastructure/sqlite"
)

func TestLogOnlySessionRegistrationAndBoundaries(t *testing.T) {
	ctx := context.Background()
	db := newParentEndChildrenTestDatabase(t)
	sessions := sqlite.NewSessionDatasource(db)
	events := sqlite.NewEventDatasource(db)
	sut := usecase.NewSessionUsecase(events, sessions, sessions, events)
	first, err := sut.Start(ctx, "cli", "codex", "log-group", "workspace", "")
	if err != nil {
		t.Fatal(err)
	}
	resumed, err := sut.Start(ctx, "cli", "codex", "log-group", "workspace", "")
	if err != nil {
		t.Fatalf("same metadata resume: %v", err)
	}
	if resumed == nil || resumed.EventID() != first.EventID() {
		t.Fatalf("resume must return original persisted start: %v", resumed)
	}
	if _, err := sut.Start(ctx, "cli", "codex", "log-group", "other", ""); !errors.Is(err, model.ErrInvalidSessionState) {
		t.Fatalf("workspace conflict: %v", err)
	}
	end1, err := sut.End(ctx, "", "", "log-group", "", "")
	if err != nil {
		t.Fatal(err)
	}
	end2, err := sut.End(ctx, "", "", "log-group", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if end1.EventID() == end2.EventID() {
		t.Fatal("distinct explicit ends collapsed")
	}
	stored, err := sessions.FindByID(ctx, types.SessionID("log-group"))
	if err != nil {
		t.Fatal(err)
	}
	group, ok := stored.Value()
	if !ok {
		t.Fatal("group missing")
	}
	if _, ended := group.EndedAt().Value(); ended {
		t.Fatal("ordinary end terminalized grouping")
	}
}

func TestLogOnlyConcurrentFirstBoundaryForImportedGrouping(t *testing.T) {
	ctx := context.Background()
	db := newParentEndChildrenTestDatabase(t)
	sessions := sqlite.NewSessionDatasource(db)
	events := sqlite.NewEventDatasource(db)
	imported := model.NewSession("imported-group", time.Now().Add(-72*time.Hour), "cli", "codex", "workspace")
	if err := sessions.Save(ctx, imported); err != nil {
		t.Fatal(err)
	}
	sut := usecase.NewSessionUsecase(events, sessions, sessions, events)
	const n = 8
	results := make(chan *model.Event, n)
	errs := make(chan error, n)
	begin := make(chan struct{})
	for range n {
		go func() {
			<-begin
			e, err := sut.Start(ctx, "cli", "codex", "imported-group", "workspace", "")
			results <- e
			errs <- err
		}()
	}
	close(begin)
	var canonical types.EventID
	for range n {
		if err := <-errs; err != nil {
			t.Fatal(err)
		}
		e := <-results
		if e == nil {
			t.Fatal("nil canonical event")
		}
		if canonical == "" {
			canonical = e.EventID()
		}
		if e.EventID() != canonical {
			t.Fatal("racing registration returned different boundary")
		}
	}
	recorded, err := events.ListRecent(ctx, 20, 0, types.EventKindSessionStarted, "", "", "imported-group", "", false, time.Time{}, time.Time{}, "")
	if err != nil || len(recorded) != 1 {
		t.Fatalf("first recorded boundaries = %d, err=%v", len(recorded), err)
	}
	receipt := apptypes.WithHookDelivery(apptypes.WithSourceHook(ctx, "session_start"), apptypes.HookDeliveryInputOf("native:start-retry", "/workspace"))
	if _, err := sut.Start(receipt, "cli", "codex", "imported-group", "workspace", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := sut.Start(receipt, "cli", "other", "imported-group", "workspace", ""); !errors.Is(err, model.ErrInvalidSessionState) {
		t.Fatalf("native receipt bypassed metadata conflict: %v", err)
	}
}

func TestLogOnlyPublicStartPreservesRecordedOrdinaryModeAndLegacyEnd(t *testing.T) {
	for _, mode := range []types.RuntimeMode{types.RuntimeModeResumed, types.RuntimeModeBackground} {
		t.Run(mode.String(), func(t *testing.T) {
			ctx := context.Background()
			db := newParentEndChildrenTestDatabase(t)
			sessions := sqlite.NewSessionDatasource(db)
			events := sqlite.NewEventDatasource(db)
			original, err := model.NewSessionWithRuntimeMode("mode-group", time.Now().Add(-72*time.Hour), "cli", "codex", "workspace", mode)
			if err != nil {
				t.Fatal(err)
			}
			if err := sessions.Save(ctx, original); err != nil {
				t.Fatal(err)
			}
			raw, err := sql.Open("sqlite", db.Path())
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = raw.Close() }()
			ended := time.Now().Add(-48 * time.Hour).UTC().Format(time.RFC3339Nano)
			if _, err := raw.Exec(`UPDATE sessions SET ended_at=?, terminal_reason='legacy_unknown', summary='historical' WHERE session_id='mode-group'`, ended); err != nil {
				t.Fatal(err)
			}
			sut := usecase.NewSessionUsecase(events, sessions, sessions, events)
			first, err := sut.Start(ctx, "cli", "codex", "mode-group", "workspace", "")
			if err != nil {
				t.Fatal(err)
			}
			again, err := sut.Start(ctx, "cli", "codex", "mode-group", "workspace", "")
			if err != nil || again.EventID() != first.EventID() {
				t.Fatalf("resume: %v", err)
			}
			if _, err := sut.End(ctx, "", "", "mode-group", "", ""); err != nil {
				t.Fatal(err)
			}
			stored, err := sessions.FindByID(ctx, "mode-group")
			if err != nil {
				t.Fatal(err)
			}
			group, _ := stored.Value()
			end, ok := group.EndedAt().Value()
			reason, _ := group.TerminalReason().Value()
			if !ok || end.Format(time.RFC3339Nano) != ended || reason != types.TerminalReasonLegacyUnknown || group.RuntimeMode() != mode || group.Summary() != "historical" {
				t.Fatal("ordinary capture rewrote recorded legacy metadata")
			}
		})
	}
}
