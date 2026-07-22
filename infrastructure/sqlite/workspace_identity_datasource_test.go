package sqlite_test

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/duck8823/traceary/domain/model"
	"github.com/duck8823/traceary/domain/types"
	"github.com/duck8823/traceary/infrastructure/sqlite"
)

func TestWorkspaceIdentityDatasource_ReportsCurrentReviewedRelationships(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "traceary.db")
	database := sqlite.NewDatabase(dbPath, onDiskSQLiteMigrations(t))
	store := sqlite.NewStoreManagementDatasource(database)
	if err := store.Initialize(ctx); err != nil {
		t.Fatalf("Initialize() error = %v", err)
	}
	db := openHookDeliveryTestDB(t, dbPath)
	if _, err := db.Exec(`INSERT INTO sessions (session_id, started_at, client, agent, workspace) VALUES ('session-1', '2026-07-22T00:00:00Z', 'hook', 'codex', '/repo')`); err != nil {
		t.Fatalf("insert session: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close setup DB: %v", err)
	}

	events := sqlite.NewEventDatasource(database)
	for _, event := range []*model.Event{
		hookDeliveryTestEvent(t, "event-1", "session-1", "/repo", "/repo", "first", "event_id:delivery-1"),
		hookDeliveryTestEvent(t, "event-2", "session-1", "/repo", "/repo", "first", "event_id:delivery-1"),
		hookDeliveryTestEvent(t, "event-3", "session-1", "/other", "/other", "second", "event_id:delivery-2"),
	} {
		if err := events.Save(ctx, event); err != nil {
			t.Fatalf("Save(%s) error = %v", event.EventID(), err)
		}
	}

	sut := sqlite.NewWorkspaceIdentityDatasource(database)
	report, err := sut.WorkspaceIdentityReport(ctx, 10)
	if err != nil {
		t.Fatalf("WorkspaceIdentityReport() error = %v", err)
	}
	if report.Coverage.EventCount != 2 || report.Coverage.CoveredEvents != 2 || report.Coverage.CoverageRate != 1 {
		t.Fatalf("coverage = %#v", report.Coverage)
	}
	if len(report.Sources) != 1 {
		t.Fatalf("sources = %#v", report.Sources)
	}
	source := report.Sources[0]
	if source.Relationships.Exact != 1 || source.Relationships.Conflict != 1 || source.DeliveryAttemptCount != 3 || source.ExactRedeliveryCount != 1 {
		t.Fatalf("source = %#v", source)
	}
	if len(report.ConflictSamples) != 1 || report.ConflictSamples[0].EventID != "event-3" {
		t.Fatalf("conflict samples = %#v", report.ConflictSamples)
	}

	alias, err := model.NewWorkspaceAlias(types.SessionID("session-1"), types.Workspace("/other"), time.Date(2026, 7, 22, 1, 0, 0, 0, time.UTC), "operator", "reviewed worktree")
	if err != nil {
		t.Fatalf("NewWorkspaceAlias() error = %v", err)
	}
	if err := sut.SaveWorkspaceAlias(ctx, alias); err != nil {
		t.Fatalf("SaveWorkspaceAlias() error = %v", err)
	}
	report, err = sut.WorkspaceIdentityReport(ctx, 10)
	if err != nil {
		t.Fatalf("WorkspaceIdentityReport(after alias) error = %v", err)
	}
	if report.Sources[0].Relationships.Conflict != 0 || report.Sources[0].Relationships.ExplicitAlias != 1 || len(report.ConflictSamples) != 0 || len(report.Aliases) != 1 {
		t.Fatalf("report after alias = %#v", report)
	}
	if err := sut.DeleteWorkspaceAlias(ctx, "session-1", "/other"); err != nil {
		t.Fatalf("DeleteWorkspaceAlias() error = %v", err)
	}
}
