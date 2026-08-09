package sqlite_test

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/duck8823/traceary/domain/model"
	"github.com/duck8823/traceary/domain/types"
	infra "github.com/duck8823/traceary/infrastructure/sqlite"
)

// legacySearchWorkspace is shared by the retirement tests so a search can be
// scoped the same way before and after the family is dropped.
const legacySearchWorkspace = "github.com/duck8823/traceary"

// TestMigration052_AppliesOverEveryRecordedTransitionState covers the four
// authority/phase combinations migration 040 permitted. Migration 052 removes
// the control table itself, so a store parked in any of them — including one
// halfway through the retirement the removed command drove — must still open.
func TestMigration052_AppliesOverEveryRecordedTransitionState(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		authority string
		phase     string
	}{
		{name: "never transitioned", authority: "legacy", phase: "active"},
		{name: "interrupted mid-retirement", authority: "tiered", phase: "retiring"},
		{name: "already retired", authority: "tiered", phase: "retired"},
		{name: "interrupted mid-restore", authority: "tiered", phase: "restoring"},
	}
	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			ctx := context.Background()
			dbPath := filepath.Join(t.TempDir(), "traceary.db")
			seedPre052Store(t, dbPath)
			setSearchMaintenanceState(t, dbPath, tc.authority, tc.phase)

			sut, store := newEventDatasource(t, dbPath, onDiskSQLiteMigrations(t))
			if err := store.Initialize(ctx); err != nil {
				t.Fatalf("Initialize() error = %v", err)
			}
			if objectExists(t, dbPath, "search_maintenance_control") {
				t.Fatal("search_maintenance_control survived migration 052")
			}
			got := searchLegacyFixture(ctx, t, sut)
			if diff := cmp.Diff([]string{"event-needle"}, eventIDs(got)); diff != "" {
				t.Fatalf("Search() IDs mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

// TestMigration052_LeavesTheLargeTablesForTheOperator pins the upgrade
// contract chosen for #1718: startup drops only constant-cost objects, so a
// multi-GiB store is never blocked behind an unattended DROP. Reclaiming the
// space stays an explicit `store search-retire`.
func TestMigration052_LeavesTheLargeTablesForTheOperator(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "traceary.db")
	seedPre052Store(t, dbPath)

	_, store := newEventDatasource(t, dbPath, onDiskSQLiteMigrations(t))
	if err := store.Initialize(ctx); err != nil {
		t.Fatalf("Initialize() error = %v", err)
	}
	for _, name := range []string{"event_search_documents", "event_search_fts", "event_search_backfill_state"} {
		if !objectExists(t, dbPath, name) {
			t.Fatalf("migration 052 removed %s; the large tables belong to search-retire", name)
		}
	}
	for _, name := range []string{
		"event_search_projection",
		"events_search_after_insert",
		"command_audits_search_after_insert",
	} {
		if objectExists(t, dbPath, name) {
			t.Fatalf("migration 052 left %s in place", name)
		}
	}
}

// TestRetireLegacySearchProjection_DropsTheFamilyAndKeepsSearchAnswering is the
// operator path end to end: the tables are gone rather than emptied, a rerun is
// a no-op, and search keeps working throughout.
func TestRetireLegacySearchProjection_DropsTheFamilyAndKeepsSearchAnswering(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "traceary.db")
	seedPre052Store(t, dbPath)

	database := infra.NewDatabase(dbPath, onDiskSQLiteMigrations(t))
	if err := infra.NewStoreManagementDatasource(database).Initialize(ctx); err != nil {
		t.Fatalf("Initialize() error = %v", err)
	}
	sut := infra.NewEventDatasource(database)
	before := searchLegacyFixture(ctx, t, sut)

	report, err := database.RetireLegacySearchProjection(ctx)
	if err != nil {
		t.Fatalf("RetireLegacySearchProjection() error = %v", err)
	}
	if report.AlreadyRemoved {
		t.Fatal("RetireLegacySearchProjection() reported already_removed on a store that carried the family")
	}
	if !report.FileSizeUnchangedUntilCompact {
		t.Fatal("RetireLegacySearchProjection() must state that the file shrinks only at compact")
	}
	if report.LogicalBytesBefore <= 0 {
		t.Fatalf("logical_bytes_before = %d, want the indexed corpus", report.LogicalBytesBefore)
	}
	if report.LogicalBytesAfter != 0 {
		t.Fatalf("logical_bytes_after = %d, want 0", report.LogicalBytesAfter)
	}

	// Dropped, not emptied. Deleting rows from an FTS5 content table appends
	// delete markers and grows the index, which is the opposite of the point.
	for _, name := range []string{"event_search_documents", "event_search_fts", "event_search_backfill_state"} {
		if objectExists(t, dbPath, name) {
			t.Fatalf("%s still exists; search-retire must drop it, not empty it", name)
		}
	}

	after := searchLegacyFixture(ctx, t, sut)
	if diff := cmp.Diff(eventIDs(before), eventIDs(after)); diff != "" {
		t.Fatalf("search results changed across retirement (-before +after):\n%s", diff)
	}

	rerun, err := database.RetireLegacySearchProjection(ctx)
	if err != nil {
		t.Fatalf("second RetireLegacySearchProjection() error = %v", err)
	}
	if !rerun.AlreadyRemoved {
		t.Fatal("second RetireLegacySearchProjection() must report already_removed")
	}
	if rerun.PhysicalBytesBefore != rerun.PhysicalBytesAfter {
		t.Fatalf("no-op rerun changed physical bytes: %d -> %d", rerun.PhysicalBytesBefore, rerun.PhysicalBytesAfter)
	}
}

// seedPre052Store builds a store on the schema that still carried the legacy
// family, with one indexed event, so the upgrade under test is the real one.
func seedPre052Store(t *testing.T, dbPath string) {
	t.Helper()
	ctx := context.Background()
	sut, store := newEventDatasource(t, dbPath, onDiskSQLiteMigrationsBefore(t, 52))
	if err := store.Initialize(ctx); err != nil {
		t.Fatalf("initialize pre-52 store: %v", err)
	}
	event := newSearchEventFixture(
		t,
		"event-needle",
		types.EventKindNote,
		legacySearchWorkspace,
		"retirement needle",
		time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC),
	)
	if err := sut.Save(ctx, event); err != nil {
		t.Fatalf("save pre-52 event: %v", err)
	}
	if !objectExists(t, dbPath, "event_search_documents") {
		t.Fatal("pre-52 fixture does not carry the legacy family")
	}
}

func setSearchMaintenanceState(t *testing.T, dbPath, authority, phase string) {
	t.Helper()
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("sql.Open() error = %v", err)
	}
	defer func() { _ = db.Close() }()
	if _, err := db.Exec(
		`UPDATE search_maintenance_control SET authority = ?, phase = ? WHERE singleton = 1`,
		authority,
		phase,
	); err != nil {
		t.Fatalf("set search maintenance state %s/%s: %v", authority, phase, err)
	}
}

func objectExists(t *testing.T, dbPath, name string) bool {
	t.Helper()
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("sql.Open() error = %v", err)
	}
	defer func() { _ = db.Close() }()
	var exists int
	if err := db.QueryRow(
		`SELECT EXISTS(SELECT 1 FROM sqlite_schema WHERE name = ?)`,
		name,
	).Scan(&exists); err != nil {
		t.Fatalf("inspect schema object %s: %v", name, err)
	}
	return exists != 0
}

func searchLegacyFixture(ctx context.Context, t *testing.T, sut *infra.EventDatasource) []*model.Event {
	t.Helper()
	got, err := sut.Search(
		ctx,
		"retirement needle",
		types.Workspace(legacySearchWorkspace),
		"",
		"",
		"",
		"",
		time.Time{},
		time.Time{},
		20,
		0,
		false,
	)
	if err != nil {
		t.Fatalf("Search() error = %v", err)
	}
	return got
}
