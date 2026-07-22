package sqlite_test

import (
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	apptypes "github.com/duck8823/traceary/application/types"
	"github.com/duck8823/traceary/domain/model"
	"github.com/duck8823/traceary/domain/types"
	infra "github.com/duck8823/traceary/infrastructure/sqlite"
)

func TestStoreManagementDatasource_PreviewOneShotSessions_DoesNotCreateStore(t *testing.T) {
	t.Parallel()
	dbPath := filepath.Join(t.TempDir(), "missing.db")
	store := infra.NewStoreManagementDatasource(infra.NewDatabase(dbPath, onDiskSQLiteMigrations(t)))
	now := time.Date(2026, 7, 22, 12, 0, 0, 0, time.UTC)
	_, err := store.PreviewOneShotSessions(context.Background(), apptypes.OneShotRepairParams{
		EvidenceHash: strings.Repeat("a", 64),
		StaleAfter:   24 * time.Hour,
		Now:          now,
		Entries:      []apptypes.OneShotRepairEvidenceEntry{repairEvidence("missing", types.TerminalReasonSuccess, now.Add(-time.Hour))},
	})
	if err == nil {
		t.Fatal("PreviewOneShotSessions() error = nil, want missing-store error")
	}
	if _, statErr := os.Stat(dbPath); !os.IsNotExist(statErr) {
		t.Fatalf("preview created store: stat error = %v", statErr)
	}
}

func TestStoreManagementDatasource_ApplyOneShotSessions_NormalizesFractionalTimestampBoundaries(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db := infra.NewDatabase(filepath.Join(t.TempDir(), "traceary.db"), onDiskSQLiteMigrations(t))
	store := infra.NewStoreManagementDatasource(db)
	if err := store.Initialize(ctx); err != nil {
		t.Fatalf("Initialize() error = %v", err)
	}
	sessions := infra.NewSessionDatasource(db)
	startedAt := time.Date(2026, 7, 20, 10, 0, 0, 0, time.UTC)
	seedRepairSession(t, sessions, "fractional-boundary", types.RuntimeModeOneShot, startedAt, "event-fractional")
	completedAt := startedAt.Add(250 * time.Millisecond)
	now := startedAt.Add(24*time.Hour + 500*time.Millisecond)
	result, err := store.ApplyOneShotSessions(ctx, apptypes.OneShotRepairParams{
		EvidenceHash: strings.Repeat("d", 64),
		StaleAfter:   24 * time.Hour,
		Now:          now,
		Entries:      []apptypes.OneShotRepairEvidenceEntry{repairEvidence("fractional-boundary", types.TerminalReasonSuccess, completedAt)},
	})
	if err != nil {
		t.Fatalf("ApplyOneShotSessions() error = %v", err)
	}
	if result.Before.StaleCount != 1 || result.AppliedCount() != 1 {
		t.Fatalf("result = %+v, want one stale applied candidate", result)
	}
	assertRepairTerminal(t, sessions, "fractional-boundary", types.TerminalReasonSuccess)
}

func TestStoreManagementDatasource_PreviewOneShotSessions_ExplainsContradictoryCompletion(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db := infra.NewDatabase(filepath.Join(t.TempDir(), "traceary.db"), onDiskSQLiteMigrations(t))
	store := infra.NewStoreManagementDatasource(db)
	if err := store.Initialize(ctx); err != nil {
		t.Fatalf("Initialize() error = %v", err)
	}
	sessions := infra.NewSessionDatasource(db)
	startedAt := time.Date(2026, 7, 20, 10, 0, 0, 0, time.UTC)
	seedRepairSession(t, sessions, "before-start", types.RuntimeModeOneShot, startedAt, "event-before-start")
	seedRepairSession(t, sessions, "before-activity", types.RuntimeModeOneShot, startedAt, "event-before-activity")
	laterEvent := model.EventOf("event-later-activity", types.EventKindNote, "cli", "codex", "before-activity", "workspace", "later activity", startedAt.Add(2*time.Hour))
	if err := infra.NewEventDatasource(db).Save(ctx, laterEvent); err != nil {
		t.Fatalf("Save(later activity) error = %v", err)
	}
	now := time.Date(2026, 7, 22, 12, 0, 0, 0, time.UTC)
	result, err := store.PreviewOneShotSessions(ctx, apptypes.OneShotRepairParams{
		EvidenceHash: strings.Repeat("e", 64), StaleAfter: 24 * time.Hour, Now: now,
		Entries: []apptypes.OneShotRepairEvidenceEntry{
			repairEvidence("before-start", types.TerminalReasonSuccess, startedAt.Add(-time.Second)),
			repairEvidence("before-activity", types.TerminalReasonSuccess, startedAt.Add(time.Hour)),
		},
	})
	if err != nil {
		t.Fatalf("PreviewOneShotSessions() error = %v", err)
	}
	if result.Candidates[0].Decision != "completion_before_start" || result.Candidates[1].Decision != "completion_before_latest_activity" {
		t.Fatalf("candidates = %+v", result.Candidates)
	}
}

func TestStoreManagementDatasource_OneShotRepair_PreviewApplyAndRerun(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db := infra.NewDatabase(filepath.Join(t.TempDir(), "traceary.db"), onDiskSQLiteMigrations(t))
	store := infra.NewStoreManagementDatasource(db)
	if err := store.Initialize(ctx); err != nil {
		t.Fatalf("Initialize() error = %v", err)
	}
	sessions := infra.NewSessionDatasource(db)
	startedAt := time.Date(2026, 7, 20, 10, 0, 0, 0, time.UTC)
	seedRepairSession(t, sessions, "legacy-attested", types.RuntimeModeInteractive, startedAt, "event-legacy")
	seedRepairSession(t, sessions, "typed-one-shot", types.RuntimeModeOneShot, startedAt, "event-one-shot")
	seedRepairSession(t, sessions, "recent-one-shot", types.RuntimeModeOneShot, time.Date(2026, 7, 22, 10, 0, 0, 0, time.UTC), "event-recent")
	now := time.Date(2026, 7, 22, 12, 0, 0, 0, time.UTC)
	params := apptypes.OneShotRepairParams{
		EvidenceHash: strings.Repeat("a", 64), StaleAfter: 24 * time.Hour, Now: now,
		Entries: []apptypes.OneShotRepairEvidenceEntry{
			repairEvidence("legacy-attested", types.TerminalReasonSuccess, time.Date(2026, 7, 21, 10, 0, 0, 0, time.UTC)),
			repairEvidence("typed-one-shot", types.TerminalReasonFailure, time.Date(2026, 7, 21, 11, 0, 0, 0, time.UTC)),
			repairEvidence("recent-one-shot", types.TerminalReasonSuccess, time.Date(2026, 7, 22, 11, 0, 0, 0, time.UTC)),
			repairEvidence("missing", types.TerminalReasonSuccess, time.Date(2026, 7, 21, 10, 0, 0, 0, time.UTC)),
		},
	}

	dryRun, err := store.PreviewOneShotSessions(ctx, params)
	if err != nil {
		t.Fatalf("PreviewOneShotSessions() error = %v", err)
	}
	if dryRun.ApplyMode || dryRun.AppliedCount() != 0 || dryRun.Before != (apptypes.OneShotRepairStats{ActiveCount: 3, StaleCount: 2}) || dryRun.After != dryRun.Before {
		t.Fatalf("dry-run result = %+v", dryRun)
	}
	if !dryRun.Candidates[0].Eligible || !dryRun.Candidates[1].Eligible || dryRun.Candidates[2].Decision != "recently_active" || dryRun.Candidates[3].Decision != "missing_session" {
		t.Fatalf("dry-run candidates = %+v", dryRun.Candidates)
	}
	assertRepairSessionActive(t, sessions, "legacy-attested")

	applied, err := store.ApplyOneShotSessions(ctx, params)
	if err != nil {
		t.Fatalf("ApplyOneShotSessions() error = %v", err)
	}
	if !applied.ApplyMode || applied.AppliedCount() != 2 {
		t.Fatalf("applied result = %+v", applied)
	}
	wantAfter := apptypes.OneShotRepairStats{ActiveCount: 1, CompletedCount: 1, FailedCount: 1}
	if applied.After != wantAfter {
		t.Fatalf("after stats = %+v, want %+v", applied.After, wantAfter)
	}
	assertRepairTerminal(t, sessions, "legacy-attested", types.TerminalReasonSuccess)
	assertRepairTerminal(t, sessions, "typed-one-shot", types.TerminalReasonFailure)

	events, err := infra.NewEventDatasource(db).ListRecent(ctx, 10, 0, "", "", "", "", "", false, time.Time{}, time.Time{}, "one_shot_repair")
	if err != nil {
		t.Fatalf("ListRecent(repair events) error = %v", err)
	}
	if len(events) != 2 {
		t.Fatalf("repair events = %d, want 2", len(events))
	}

	rerun, err := store.ApplyOneShotSessions(ctx, params)
	if err != nil {
		t.Fatalf("ApplyOneShotSessions(rerun) error = %v", err)
	}
	if rerun.AppliedCount() != 0 || rerun.Candidates[0].Decision != "already_terminal" || rerun.Candidates[1].Decision != "already_terminal" {
		t.Fatalf("rerun result = %+v", rerun)
	}
	events, err = infra.NewEventDatasource(db).ListRecent(ctx, 10, 0, "", "", "", "", "", false, time.Time{}, time.Time{}, "one_shot_repair")
	if err != nil || len(events) != 2 {
		t.Fatalf("repair events after rerun = %d, error=%v, want 2", len(events), err)
	}
}

func TestStoreManagementDatasource_ApplyOneShotSessions_RollsBackWholeRun(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db := infra.NewDatabase(filepath.Join(t.TempDir(), "traceary.db"), onDiskSQLiteMigrations(t))
	store := infra.NewStoreManagementDatasource(db)
	if err := store.Initialize(ctx); err != nil {
		t.Fatalf("Initialize() error = %v", err)
	}
	sessions := infra.NewSessionDatasource(db)
	startedAt := time.Date(2026, 7, 20, 10, 0, 0, 0, time.UTC)
	seedRepairSession(t, sessions, "rollback-first", types.RuntimeModeOneShot, startedAt, "event-first")
	seedRepairSession(t, sessions, "rollback-second", types.RuntimeModeOneShot, startedAt, "event-second")

	evidenceHash := strings.Repeat("c", 64)
	completedAt := time.Date(2026, 7, 21, 10, 0, 0, 0, time.UTC)
	collisionID := repairEventID(evidenceHash, "rollback-second", types.TerminalReasonFailure, completedAt)
	holder := model.NewSession("collision-holder", startedAt, "cli", "codex", "workspace")
	holderEvent := model.EventOf(collisionID, types.EventKindSessionStarted, "cli", "codex", "collision-holder", "workspace", "session started", startedAt)
	if err := sessions.SaveBoundary(ctx, holder, holderEvent); err != nil {
		t.Fatalf("SaveBoundary(collision holder) error = %v", err)
	}

	_, err := store.ApplyOneShotSessions(ctx, apptypes.OneShotRepairParams{
		EvidenceHash: evidenceHash, StaleAfter: 24 * time.Hour, Now: time.Date(2026, 7, 22, 12, 0, 0, 0, time.UTC),
		Entries: []apptypes.OneShotRepairEvidenceEntry{
			repairEvidence("rollback-first", types.TerminalReasonSuccess, completedAt),
			repairEvidence("rollback-second", types.TerminalReasonFailure, completedAt),
		},
	})
	if err == nil {
		t.Fatal("ApplyOneShotSessions() error = nil, want event collision")
	}
	assertRepairSessionActive(t, sessions, "rollback-first")
	assertRepairSessionActive(t, sessions, "rollback-second")
	events, listErr := infra.NewEventDatasource(db).ListRecent(ctx, 10, 0, "", "", "", "", "", false, time.Time{}, time.Time{}, "one_shot_repair")
	if listErr != nil || len(events) != 0 {
		t.Fatalf("repair events after rollback = %d, error=%v, want 0", len(events), listErr)
	}
}

func seedRepairSession(t *testing.T, sessions *infra.SessionDatasource, sessionID types.SessionID, mode types.RuntimeMode, startedAt time.Time, eventID types.EventID) {
	t.Helper()
	var session *model.Session
	if mode == types.RuntimeModeInteractive {
		session = model.NewSession(sessionID, startedAt, "cli", "codex", "workspace")
	} else {
		var err error
		session, err = model.NewSessionWithRuntimeMode(sessionID, startedAt, "cli", "codex", "workspace", mode)
		if err != nil {
			t.Fatalf("NewSessionWithRuntimeMode() error = %v", err)
		}
	}
	event := model.EventOf(eventID, types.EventKindSessionStarted, "cli", "codex", sessionID, "workspace", "session started", startedAt)
	if err := sessions.SaveBoundary(context.Background(), session, event); err != nil {
		t.Fatalf("SaveBoundary(%s) error = %v", sessionID, err)
	}
}

func repairEvidence(sessionID types.SessionID, reason types.TerminalReason, completedAt time.Time) apptypes.OneShotRepairEvidenceEntry {
	return apptypes.OneShotRepairEvidenceEntry{
		SessionID: sessionID, RuntimeMode: types.RuntimeModeOneShot, TerminalReason: reason, CompletedAt: completedAt,
		EvidenceSource: apptypes.OneShotRepairEvidenceOperatorAttested, EvidenceRef: "test-run:42",
	}
}

func assertRepairSessionActive(t *testing.T, sessions *infra.SessionDatasource, sessionID types.SessionID) {
	t.Helper()
	stored, err := sessions.FindByID(context.Background(), sessionID)
	if err != nil {
		t.Fatal(err)
	}
	session, ok := stored.Value()
	if !ok {
		t.Fatalf("session %s not found", sessionID)
	}
	_, ended := session.EndedAt().Value()
	if ended {
		t.Fatalf("session %s active = %v/%v", sessionID, ok, session)
	}
}

func assertRepairTerminal(t *testing.T, sessions *infra.SessionDatasource, sessionID types.SessionID, want types.TerminalReason) {
	t.Helper()
	stored, err := sessions.FindByID(context.Background(), sessionID)
	if err != nil {
		t.Fatal(err)
	}
	session, ok := stored.Value()
	if !ok || session.RuntimeMode() != types.RuntimeModeOneShot {
		t.Fatalf("session %s missing or mode=%q", sessionID, session.RuntimeMode())
	}
	reason, ok := session.TerminalReason().Value()
	if !ok || reason != want {
		t.Fatalf("session %s terminal reason = %q/%v, want %q", sessionID, reason, ok, want)
	}
}

func repairEventID(evidenceHash string, sessionID types.SessionID, reason types.TerminalReason, completedAt time.Time) types.EventID {
	digest := sha256.Sum256([]byte(fmt.Sprintf(
		"%s\x00%s\x00%s\x00%s", evidenceHash, sessionID, reason, completedAt.UTC().Format(time.RFC3339Nano),
	)))
	return types.EventID(fmt.Sprintf("event-one-shot-repair-%x", digest[:16]))
}
