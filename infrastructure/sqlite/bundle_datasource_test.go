package sqlite_test

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/duck8823/traceary/application/usecase"
	"github.com/duck8823/traceary/domain/model"
	"github.com/duck8823/traceary/domain/types"
	"github.com/duck8823/traceary/infrastructure/sqlite"
)

func TestBundleDatasource_ImportSessionRejectsConflictingTerminalReplace(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	db := sqlite.NewDatabase(filepath.Join(t.TempDir(), "traceary.db"), onDiskSQLiteMigrations(t))
	if err := sqlite.NewStoreManagementDatasource(db).Initialize(ctx); err != nil {
		t.Fatalf("Initialize() error = %v", err)
	}
	bundles := sqlite.NewBundleDatasource(db, sqlite.NewEventDatasource(db))
	agent, _ := types.AgentFrom("codex")
	startedAt := time.Date(2026, 7, 22, 10, 0, 0, 0, time.UTC)
	newTerminal := func(reason types.TerminalReason, summary string) *model.Session {
		session, err := model.NewSessionWithRuntimeMode(types.SessionID("bundle-terminal"), startedAt, types.Client("hook"), agent, types.Workspace("workspace"), types.RuntimeModeOneShot)
		if err != nil {
			t.Fatalf("NewSessionWithRuntimeMode() error = %v", err)
		}
		if _, err := session.Terminate(startedAt.Add(time.Minute), reason, summary); err != nil {
			t.Fatalf("Terminate() error = %v", err)
		}
		return session
	}

	firstTx, err := bundles.BeginBundleImport(ctx)
	if err != nil {
		t.Fatalf("BeginBundleImport(first) error = %v", err)
	}
	if imported, err := firstTx.ImportSession(ctx, newTerminal(types.TerminalReasonSuccess, "first"), usecase.BundleConflictReplace, usecase.BundleMissingParentReject); err != nil || !imported {
		t.Fatalf("ImportSession(first) = %v/%v", imported, err)
	}
	if err := firstTx.Commit(ctx); err != nil {
		t.Fatalf("Commit(first) error = %v", err)
	}
	idempotentTx, err := bundles.BeginBundleImport(ctx)
	if err != nil {
		t.Fatalf("BeginBundleImport(idempotent) error = %v", err)
	}
	if imported, err := idempotentTx.ImportSession(ctx, newTerminal(types.TerminalReasonSuccess, "redelivery"), usecase.BundleConflictReplace, usecase.BundleMissingParentReject); err != nil || !imported {
		_ = idempotentTx.Rollback(ctx)
		t.Fatalf("ImportSession(idempotent) = %v/%v", imported, err)
	}
	if err := idempotentTx.Commit(ctx); err != nil {
		t.Fatalf("Commit(idempotent) error = %v", err)
	}

	modeConflict, err := model.NewSessionWithRuntimeMode(types.SessionID("bundle-terminal"), startedAt, types.Client("hook"), agent, types.Workspace("workspace"), types.RuntimeModeInteractive)
	if err != nil {
		t.Fatalf("NewSessionWithRuntimeMode(mode conflict) error = %v", err)
	}
	if _, err := modeConflict.Terminate(startedAt.Add(time.Minute), types.TerminalReasonSuccess, "mode conflict"); err != nil {
		t.Fatalf("Terminate(mode conflict) error = %v", err)
	}
	modeConflictTx, err := bundles.BeginBundleImport(ctx)
	if err != nil {
		t.Fatalf("BeginBundleImport(mode conflict) error = %v", err)
	}
	_, err = modeConflictTx.ImportSession(ctx, modeConflict, usecase.BundleConflictReplace, usecase.BundleMissingParentReject)
	if err == nil || !errors.Is(err, model.ErrConflictingTerminalState) {
		_ = modeConflictTx.Rollback(ctx)
		t.Fatalf("ImportSession(mode conflict) error = %v, want ErrConflictingTerminalState", err)
	}
	if !strings.Contains(err.Error(), `mode="one_shot"`) || !strings.Contains(err.Error(), `mode="interactive"`) {
		_ = modeConflictTx.Rollback(ctx)
		t.Fatalf("ImportSession(mode conflict) error lacks diagnostic modes: %v", err)
	}
	if err := modeConflictTx.Rollback(ctx); err != nil {
		t.Fatalf("Rollback(mode conflict) error = %v", err)
	}

	conflictTx, err := bundles.BeginBundleImport(ctx)
	if err != nil {
		t.Fatalf("BeginBundleImport(conflict) error = %v", err)
	}
	_, err = conflictTx.ImportSession(ctx, newTerminal(types.TerminalReasonFailure, "conflict"), usecase.BundleConflictReplace, usecase.BundleMissingParentReject)
	if err == nil || !errors.Is(err, model.ErrConflictingTerminalState) {
		_ = conflictTx.Rollback(ctx)
		t.Fatalf("ImportSession(conflict) error = %v, want ErrConflictingTerminalState", err)
	}
	if !strings.Contains(err.Error(), `"success"`) || !strings.Contains(err.Error(), `"failure"`) {
		_ = conflictTx.Rollback(ctx)
		t.Fatalf("ImportSession(conflict) error lacks diagnostic reasons: %v", err)
	}
	if err := conflictTx.Rollback(ctx); err != nil {
		t.Fatalf("Rollback(conflict) error = %v", err)
	}

	stored, err := sqlite.NewSessionDatasource(db).FindByID(ctx, types.SessionID("bundle-terminal"))
	if err != nil {
		t.Fatalf("FindByID() error = %v", err)
	}
	got, _ := stored.Value()
	if reason, ok := got.TerminalReason().Value(); !ok || reason != types.TerminalReasonSuccess || got.Summary() != "first" {
		t.Fatalf("stored terminal state = %q/%v summary=%q", reason, ok, got.Summary())
	}
}

func TestBundleDatasource_CommandAuditBeforeEventFailsFK(t *testing.T) {
	t.Parallel()

	dbPath := filepath.Join(t.TempDir(), "traceary", "traceary.db")
	db := sqlite.NewDatabase(dbPath, onDiskSQLiteMigrations(t))
	store := sqlite.NewStoreManagementDatasource(db)
	if err := store.Initialize(context.Background()); err != nil {
		t.Fatalf("Initialize() error = %v", err)
	}

	eventStore := sqlite.NewEventDatasource(db)
	sut := sqlite.NewBundleDatasource(db, eventStore)
	tx, err := sut.BeginBundleImport(context.Background())
	if err != nil {
		t.Fatalf("BeginBundleImport() error = %v", err)
	}
	defer func() { _ = tx.Rollback(context.Background()) }()

	eventID, err := types.EventIDFrom("event-1")
	if err != nil {
		t.Fatalf("EventIDFrom() error = %v", err)
	}
	audit, err := model.NewCommandAudit(eventID, "go test ./...", "", "", false, false)
	if err != nil {
		t.Fatalf("NewCommandAudit() error = %v", err)
	}

	_, err = tx.ImportCommandAudit(context.Background(), audit, usecase.BundleConflictSkip)
	if err == nil {
		t.Fatalf("ImportCommandAudit() succeeded before event import, want FK error")
	}
	if !strings.Contains(err.Error(), "event not found") && !strings.Contains(err.Error(), "FOREIGN KEY") {
		t.Fatalf("ImportCommandAudit() error = %v, want FK/event-not-found error", err)
	}
}

func TestBundleDatasource_ImportSessionBackfillsMissingParent(t *testing.T) {
	t.Parallel()

	dbPath := filepath.Join(t.TempDir(), "traceary", "traceary.db")
	db := sqlite.NewDatabase(dbPath, onDiskSQLiteMigrations(t))
	store := sqlite.NewStoreManagementDatasource(db)
	if err := store.Initialize(context.Background()); err != nil {
		t.Fatalf("Initialize() error = %v", err)
	}

	eventStore := sqlite.NewEventDatasource(db)
	sut := sqlite.NewBundleDatasource(db, eventStore)
	tx, err := sut.BeginBundleImport(context.Background())
	if err != nil {
		t.Fatalf("BeginBundleImport() error = %v", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback(context.Background())
		}
	}()

	agent, err := types.AgentFrom("codex")
	if err != nil {
		t.Fatalf("AgentFrom() error = %v", err)
	}
	child := model.SessionOf(
		types.SessionID("child-session"),
		time.Date(2026, 4, 25, 10, 0, 0, 0, time.UTC),
		types.None[time.Time](),
		types.Client("cli"),
		agent,
		types.Workspace("ws"),
		"child",
		"",
		types.SessionID("missing-parent"),
	)
	imported, err := tx.ImportSession(context.Background(), child, usecase.BundleConflictSkip, usecase.BundleMissingParentBackfill)
	if err != nil {
		t.Fatalf("ImportSession() error = %v", err)
	}
	if !imported {
		t.Fatalf("ImportSession() imported = false, want true")
	}
	if err := tx.Commit(context.Background()); err != nil {
		t.Fatalf("Commit() error = %v", err)
	}
	committed = true

	sessionStore := sqlite.NewSessionDatasource(db)
	stub, err := sessionStore.FindByID(context.Background(), types.SessionID("missing-parent"))
	if err != nil {
		t.Fatalf("FindByID(parent) error = %v", err)
	}
	parent, ok := stub.Value()
	if !ok {
		t.Fatalf("backfilled parent not found")
	}
	if got := parent.Label(); got != "traceary:bundle-backfilled-parent" {
		t.Fatalf("parent label = %q, want marker", got)
	}
}
