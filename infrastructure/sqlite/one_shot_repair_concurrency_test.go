package sqlite

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	apptypes "github.com/duck8823/traceary/application/types"
	"github.com/duck8823/traceary/domain/model"
	"github.com/duck8823/traceary/domain/types"
)

func TestOneShotRepairFailsClosedWhenWriterCommitsAfterInspection(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name              string
		intervene         func(context.Context, *Database, types.SessionID, time.Time) error
		wantTerminal      bool
		wantTerminalEvent int
		wantActivityEvent int
	}{
		{
			name: "new activity",
			intervene: func(ctx context.Context, db *Database, sessionID types.SessionID, at time.Time) error {
				event := model.EventOf("event-competing-activity", types.EventKindNote, "cli", "codex", sessionID, "workspace", "competing activity", at)
				return NewEventDatasource(db).Save(ctx, event)
			},
			wantActivityEvent: 1,
		},
		{
			name: "terminal boundary",
			intervene: func(ctx context.Context, db *Database, sessionID types.SessionID, at time.Time) error {
				sessions := NewSessionDatasource(db)
				stored, err := sessions.FindByID(ctx, sessionID)
				if err != nil {
					return err
				}
				session, ok := stored.Value()
				if !ok {
					return model.ErrInvalidSessionState
				}
				if _, err := session.FinalizeOneShot(at, types.TerminalReasonFailure, "competing terminal boundary"); err != nil {
					return fmt.Errorf("finalize competing one-shot session: %w", err)
				}
				event := model.EventOf("event-competing-terminal", types.EventKindSessionEnded, "cli", "codex", sessionID, "workspace", "session ended", at)
				return sessions.SaveBoundary(ctx, session, event)
			},
			wantTerminal:      true,
			wantTerminalEvent: 1,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			ctx := context.Background()
			dbPath := filepath.Join(t.TempDir(), "traceary.db")
			db := NewDatabase(dbPath, os.DirFS(filepath.Join("..", "..", "schema", "sqlite", "migrations")))
			store := NewStoreManagementDatasource(db)
			if err := store.Initialize(ctx); err != nil {
				t.Fatalf("Initialize() error = %v", err)
			}
			startedAt := time.Date(2026, 7, 20, 10, 0, 0, 0, time.UTC)
			suffix := strings.ReplaceAll(tc.name, " ", "-")
			stableSessionID := types.SessionID("stable-" + suffix)
			conflictSessionID := types.SessionID("concurrent-" + suffix)
			for _, sessionID := range []types.SessionID{stableSessionID, conflictSessionID} {
				session, err := model.NewSessionWithRuntimeMode(sessionID, startedAt, "cli", "codex", "workspace", types.RuntimeModeOneShot)
				if err != nil {
					t.Fatalf("NewSessionWithRuntimeMode() error = %v", err)
				}
				started := model.EventOf(types.EventID("event-started-"+sessionID.String()), types.EventKindSessionStarted, "cli", "codex", sessionID, "workspace", "session started", startedAt)
				if err := NewSessionDatasource(db).SaveBoundary(ctx, session, started); err != nil {
					t.Fatalf("SaveBoundary(start) error = %v", err)
				}
			}

			completedAt := startedAt.Add(24 * time.Hour)
			params := apptypes.OneShotRepairParams{
				EvidenceHash: strings.Repeat("f", 64),
				StaleAfter:   24 * time.Hour,
				Now:          startedAt.Add(48 * time.Hour),
				Entries: []apptypes.OneShotRepairEvidenceEntry{
					concurrencyRepairEvidence(stableSessionID, completedAt),
					concurrencyRepairEvidence(conflictSessionID, completedAt),
				},
			}
			intervenedAt := completedAt.Add(time.Hour)
			_, err := store.repairOneShotSessions(ctx, params, true, func(ctx context.Context) error {
				return tc.intervene(ctx, db, conflictSessionID, intervenedAt)
			})
			if err == nil {
				t.Fatal("repairOneShotSessions() error = nil, want concurrent snapshot conflict")
			}

			stableStored, findErr := NewSessionDatasource(db).FindByID(ctx, stableSessionID)
			if findErr != nil {
				t.Fatalf("FindByID(stable) error = %v", findErr)
			}
			stable, ok := stableStored.Value()
			if !ok {
				t.Fatal("stable session missing after conflict")
			}
			if _, terminal := stable.TerminalReason().Value(); terminal {
				t.Fatal("stable candidate was partially repaired")
			}

			stored, findErr := NewSessionDatasource(db).FindByID(ctx, conflictSessionID)
			if findErr != nil {
				t.Fatalf("FindByID() error = %v", findErr)
			}
			got, ok := stored.Value()
			if !ok {
				t.Fatal("session missing after conflict")
			}
			_, terminal := got.TerminalReason().Value()
			if terminal != tc.wantTerminal {
				t.Fatalf("terminal = %v, want %v", terminal, tc.wantTerminal)
			}
			if tc.wantTerminal {
				reason, _ := got.TerminalReason().Value()
				if reason != types.TerminalReasonFailure {
					t.Fatalf("terminal reason = %q, want failure", reason)
				}
			}
			repairEvents, listErr := NewEventDatasource(db).ListRecent(ctx, 10, 0, "", "", "", "", "", false, time.Time{}, time.Time{}, "one_shot_repair")
			if listErr != nil {
				t.Fatalf("ListRecent(repair) error = %v", listErr)
			}
			if len(repairEvents) != 0 {
				t.Fatalf("repair events = %d, want 0", len(repairEvents))
			}
			terminalEvents, listErr := NewEventDatasource(db).ListRecent(ctx, 10, 0, types.EventKindSessionEnded, "", "", "", "", false, time.Time{}, time.Time{}, "")
			if listErr != nil {
				t.Fatalf("ListRecent(terminal) error = %v", listErr)
			}
			if len(terminalEvents) != tc.wantTerminalEvent {
				t.Fatalf("terminal events = %d, want %d", len(terminalEvents), tc.wantTerminalEvent)
			}
			activityEvents, listErr := NewEventDatasource(db).ListRecent(ctx, 10, 0, types.EventKindNote, "", "", conflictSessionID, "", false, time.Time{}, time.Time{}, "")
			if listErr != nil {
				t.Fatalf("ListRecent(activity) error = %v", listErr)
			}
			if len(activityEvents) != tc.wantActivityEvent {
				t.Fatalf("activity events = %d, want %d", len(activityEvents), tc.wantActivityEvent)
			}
		})
	}
}

func concurrencyRepairEvidence(sessionID types.SessionID, completedAt time.Time) apptypes.OneShotRepairEvidenceEntry {
	return apptypes.OneShotRepairEvidenceEntry{
		SessionID: sessionID, RuntimeMode: types.RuntimeModeOneShot,
		TerminalReason: types.TerminalReasonSuccess, CompletedAt: completedAt,
		EvidenceSource: apptypes.OneShotRepairEvidenceOperatorAttested, EvidenceRef: "test:concurrent-writer",
	}
}
