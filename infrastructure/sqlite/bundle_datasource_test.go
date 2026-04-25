package sqlite_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/duck8823/traceary/application/usecase"
	"github.com/duck8823/traceary/domain/model"
	"github.com/duck8823/traceary/domain/types"
	"github.com/duck8823/traceary/infrastructure/sqlite"
)

func TestBundleDatasource_CommandAuditBeforeEventFailsFK(t *testing.T) {
	t.Parallel()

	dbPath := filepath.Join(t.TempDir(), "traceary", "traceary.db")
	db := sqlite.NewDatabase(dbPath, os.DirFS("../../schema/sqlite/migrations"))
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
	db := sqlite.NewDatabase(dbPath, os.DirFS("../../schema/sqlite/migrations"))
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
