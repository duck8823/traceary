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
	db = openHookDeliveryTestDB(t, dbPath)
	if _, err := db.Exec(`
		INSERT INTO hook_delivery_attempts (delivery_record_id, attempted_event_id, outcome, attempt_origin, observed_at)
		SELECT delivery_record_id, 'historical-exact', 'exact_redelivery', 'backfill', '2026-07-21T00:00:00Z'
		  FROM hook_deliveries
		 ORDER BY accepted_at
		 LIMIT 1`); err != nil {
		t.Fatalf("insert historical exact backfill: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close historical setup DB: %v", err)
	}
	if err := store.Initialize(ctx); err != nil {
		t.Fatalf("Initialize(catch-up) error = %v", err)
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
	if source.Relationships.Exact != 1 || source.Relationships.Conflict != 1 || source.ConflictPairCount != 1 || source.DeliveryAttemptCount != 4 || source.RuntimeAttemptCount != 3 || source.BackfilledAttemptCount != 1 || source.ExactRedeliveryCount != 1 {
		t.Fatalf("source = %#v", source)
	}
	if report.Coverage.ObservationRows != 2 || report.Coverage.ObservationKeys != 2 {
		t.Fatalf("observation rows/keys = %d/%d, want 2/2", report.Coverage.ObservationRows, report.Coverage.ObservationKeys)
	}
	if report.ConflictPairCount != 1 {
		t.Fatalf("conflict_pair_count = %d, want 1", report.ConflictPairCount)
	}
	if len(report.ConflictSamples) != 1 || report.ConflictSamples[0].EventID != "event-3" || report.ConflictSamples[0].Workspace != "/other" {
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

func TestWorkspaceIdentityDatasource_CountsDistinctConflictPairs(t *testing.T) {
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
	for i, event := range []*model.Event{
		hookDeliveryTestEvent(t, "event-a1", "session-1", "/other", "/other", "a1", "event_id:delivery-a1"),
		hookDeliveryTestEvent(t, "event-a2", "session-1", "/other", "/other", "a2", "event_id:delivery-a2"),
		hookDeliveryTestEvent(t, "event-a3", "session-1", "/other", "/other", "a3", "event_id:delivery-a3"),
		hookDeliveryTestEvent(t, "event-b1", "session-1", "/third", "/third", "b1", "event_id:delivery-b1"),
	} {
		if err := events.Save(ctx, event); err != nil {
			t.Fatalf("Save(%d) error = %v", i, err)
		}
	}

	sut := sqlite.NewWorkspaceIdentityDatasource(database)
	report, err := sut.WorkspaceIdentityReport(ctx, 10)
	if err != nil {
		t.Fatalf("WorkspaceIdentityReport() error = %v", err)
	}
	if report.Sources[0].Relationships.Conflict != 4 {
		t.Fatalf("conflict rows = %d, want 4 (volume)", report.Sources[0].Relationships.Conflict)
	}
	if report.Sources[0].ConflictPairCount != 2 || report.ConflictPairCount != 2 {
		t.Fatalf("conflict pairs source=%d report=%d, want 2", report.Sources[0].ConflictPairCount, report.ConflictPairCount)
	}
	if len(report.ConflictSamples) != 2 {
		t.Fatalf("samples = %#v, want one row per pair", report.ConflictSamples)
	}
	got := map[string]bool{}
	for _, sample := range report.ConflictSamples {
		got[sample.Workspace] = true
		if sample.SessionID != "session-1" || sample.Workspace == "" {
			t.Fatalf("sample missing session/workspace: %#v", sample)
		}
	}
	if !got["/other"] || !got["/third"] {
		t.Fatalf("sample workspaces = %v, want /other and /third", got)
	}

	limited, err := sut.WorkspaceIdentityReport(ctx, 1)
	if err != nil {
		t.Fatalf("WorkspaceIdentityReport(limit=1) error = %v", err)
	}
	if limited.ConflictPairCount != 2 || len(limited.ConflictSamples) != 1 {
		t.Fatalf("limited report pairs=%d samples=%d, want pairs=2 samples=1", limited.ConflictPairCount, len(limited.ConflictSamples))
	}
}

func TestWorkspaceIdentityDatasource_SourceVolumesUseObservationCount(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "traceary.db")
	database := sqlite.NewDatabase(dbPath, onDiskSQLiteMigrations(t))
	store := sqlite.NewStoreManagementDatasource(database)
	if err := store.Initialize(ctx); err != nil {
		t.Fatalf("Initialize() error = %v", err)
	}
	db := openHookDeliveryTestDB(t, dbPath)
	if _, err := db.Exec(`
		INSERT INTO session_workspace_observations (
			session_id, workspace, observed_relationship, source_client, source_hook, observation_kind,
			observation_count, first_observed_at, last_observed_at, observed_event_id, raw_workspace,
			delivery_record_id, attribution_fingerprint, diagnostic_reason, observation_origin
		) VALUES
			('s1', '/repo', 'exact', 'codex', 'user_prompt_submit', 'primary',
			 3, '2026-07-22T00:00:00Z', '2026-07-22T00:00:02Z', 'e3', NULL, NULL, 'fp-a', '', 'runtime'),
			('s1', '/other', 'conflict', 'codex', 'user_prompt_submit', 'primary',
			 2, '2026-07-22T00:00:03Z', '2026-07-22T00:00:04Z', 'e5', NULL, NULL, 'fp-b', '', 'runtime')
	`); err != nil {
		t.Fatalf("insert aggregates: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	report, err := sqlite.NewWorkspaceIdentityDatasource(database).WorkspaceIdentityReport(ctx, 10)
	if err != nil {
		t.Fatalf("WorkspaceIdentityReport() error = %v", err)
	}
	if report.Coverage.ObservationCount != 5 || report.Coverage.ObservationRows != 2 {
		t.Fatalf("coverage = %#v, want volume=5 rows=2", report.Coverage)
	}
	if len(report.Sources) != 1 || report.Sources[0].ObservationCount != 5 || report.Sources[0].Relationships.Exact != 3 || report.Sources[0].Relationships.Conflict != 2 {
		t.Fatalf("source = %#v, want volume 3+2", report.Sources)
	}
}

func TestWorkspaceIdentityDatasource_ConflictSamplesUseLatestEventPerKey(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "traceary.db")
	database := sqlite.NewDatabase(dbPath, onDiskSQLiteMigrations(t))
	store := sqlite.NewStoreManagementDatasource(database)
	if err := store.Initialize(ctx); err != nil {
		t.Fatalf("Initialize() error = %v", err)
	}
	db := openHookDeliveryTestDB(t, dbPath)
	if _, err := db.Exec(`
		INSERT INTO session_workspace_observations (
			session_id, workspace, observed_relationship, source_client, source_hook, observation_kind,
			observation_count, first_observed_at, last_observed_at, observed_event_id, raw_workspace,
			delivery_record_id, attribution_fingerprint, diagnostic_reason, observation_origin
		) VALUES
			('session-1', '/other', 'conflict', 'codex', 'user_prompt_submit', 'primary',
			 3, '2026-07-22T00:00:00Z', '2026-07-22T00:00:02Z', 'event-latest', NULL, NULL, 'fp', '', 'runtime')
	`); err != nil {
		t.Fatalf("insert conflict aggregate: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	report, err := sqlite.NewWorkspaceIdentityDatasource(database).WorkspaceIdentityReport(ctx, 10)
	if err != nil {
		t.Fatalf("WorkspaceIdentityReport() error = %v", err)
	}
	if len(report.ConflictSamples) != 1 || report.ConflictSamples[0].EventID != "event-latest" || report.ConflictSamples[0].Workspace != "/other" {
		t.Fatalf("samples = %#v, want newest observed_event_id", report.ConflictSamples)
	}
}
