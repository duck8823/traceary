package sqlite_test

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	infra "github.com/duck8823/traceary/infrastructure/sqlite"
)

// TestMigration048_ClearsHistoricalCommandExecutedBodies pins the reclaimed
// bytes #1675 claims. Stopping the duplicate write only helps new rows; the
// 1.785 GiB measured on the live store is history, and it is reclaimed only if
// the migration actually rewrites those rows. Other kinds must be untouched.
//
// Only audited command_executed rows are cleared: a log-only command_executed
// body is the sole payload and must survive upgrade.
func TestMigration048_ClearsHistoricalCommandExecutedBodies(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "traceary.db")

	preMigration := infra.NewDatabase(dbPath, onDiskSQLiteMigrationsBefore(t, 48))
	if err := infra.NewStoreManagementDatasource(preMigration).Initialize(ctx); err != nil {
		t.Fatalf("initialize pre-48 store: %v", err)
	}

	const (
		auditedCommandBody = "go test ./...\n\nINPUT:\nstdin\n\nOUTPUT:\nPASS ok github.com/example/pkg"
		loggedCommandBody  = "manual command record"
		transcriptBody     = "a transcript body that migration 048 must not touch"
	)
	seedEventBodiesForBodyClearing(t, dbPath, map[string]struct{ kind, body string }{
		"evt-audited":    {"command_executed", auditedCommandBody},
		"evt-logged":     {"command_executed", loggedCommandBody},
		"evt-transcript": {"transcript", transcriptBody},
	})
	seedCommandAuditForBodyClearing(t, dbPath, "evt-audited", "go test ./...")

	before := eventBodyBytesByIDs(t, dbPath, "evt-audited", "evt-logged", "evt-transcript")
	if before["evt-audited"] == 0 {
		t.Fatalf("seed failed: audited command body is already empty before migration")
	}
	if before["evt-logged"] == 0 {
		t.Fatalf("seed failed: logged-only command body is already empty before migration")
	}

	current := infra.NewDatabase(dbPath, onDiskSQLiteMigrations(t))
	if err := infra.NewStoreManagementDatasource(current).Initialize(ctx); err != nil {
		t.Fatalf("initialize current store: %v", err)
	}

	after := eventBodyBytesByIDs(t, dbPath, "evt-audited", "evt-logged", "evt-transcript")
	if diff := cmp.Diff(0, after["evt-audited"]); diff != "" {
		t.Fatalf("audited command_executed body bytes after migration (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff(before["evt-logged"], after["evt-logged"]); diff != "" {
		t.Fatalf("migration 048 erased a log-only command_executed body (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff(before["evt-transcript"], after["evt-transcript"]); diff != "" {
		t.Fatalf("migration 048 rewrote a non-command body (-want +got):\n%s", diff)
	}
	t.Logf("audited body: %d -> %d; logged body kept: %d; transcript kept: %d",
		before["evt-audited"], after["evt-audited"], after["evt-logged"], after["evt-transcript"])
}

func seedEventBodiesForBodyClearing(t *testing.T, dbPath string, rows map[string]struct{ kind, body string }) {
	t.Helper()

	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("sql.Open() error = %v", err)
	}
	defer func() { _ = db.Close() }()

	createdAt := time.Date(2026, 8, 9, 12, 0, 0, 0, time.UTC).Format(time.RFC3339Nano)
	for id, row := range rows {
		if _, err := db.Exec(`
INSERT INTO events(id, kind, client, agent, session_id, workspace, body, body_availability, created_at, source_hook)
VALUES(?, ?, 'cli', 'codex', 'session-048', 'github.com/duck8823/traceary', ?, 'available', ?, '')`,
			id, row.kind, row.body, createdAt); err != nil {
			t.Fatalf("insert seed event %s: %v", id, err)
		}
	}
}

func seedCommandAuditForBodyClearing(t *testing.T, dbPath string, eventID, command string) {
	t.Helper()

	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("sql.Open() error = %v", err)
	}
	defer func() { _ = db.Close() }()

	if _, err := db.Exec(`
INSERT INTO command_audits(event_id, command_text, input_text, output_text, input_truncated, output_truncated)
VALUES(?, ?, '', '', 0, 0)`, eventID, command); err != nil {
		t.Fatalf("insert seed command_audit for %s: %v", eventID, err)
	}
}

func eventBodyBytesByIDs(t *testing.T, dbPath string, ids ...string) map[string]int {
	t.Helper()

	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("sql.Open() error = %v", err)
	}
	defer func() { _ = db.Close() }()

	out := make(map[string]int, len(ids))
	for _, id := range ids {
		var n int
		if err := db.QueryRow(`SELECT length(CAST(body AS BLOB)) FROM events WHERE id=?`, id).Scan(&n); err != nil {
			t.Fatalf("read body bytes for %s: %v", id, err)
		}
		out[id] = n
	}
	return out
}
