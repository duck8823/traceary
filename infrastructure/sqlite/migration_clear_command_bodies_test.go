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
func TestMigration048_ClearsHistoricalCommandExecutedBodies(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "traceary.db")

	preMigration := infra.NewDatabase(dbPath, onDiskSQLiteMigrationsBefore(t, 48))
	if err := infra.NewStoreManagementDatasource(preMigration).Initialize(ctx); err != nil {
		t.Fatalf("initialize pre-48 store: %v", err)
	}

	const (
		commandBody    = "go test ./...\n\nINPUT:\nstdin\n\nOUTPUT:\nPASS ok github.com/example/pkg"
		transcriptBody = "a transcript body that migration 048 must not touch"
	)
	seedEventBodiesForBodyClearing(t, dbPath, map[string]struct{ kind, body string }{
		"evt-command":    {"command_executed", commandBody},
		"evt-transcript": {"transcript", transcriptBody},
	})

	beforeCommand, beforeTranscript := eventBodyBytesByID(t, dbPath)
	if beforeCommand == 0 {
		t.Fatalf("seed failed: command body is already empty before migration")
	}

	current := infra.NewDatabase(dbPath, onDiskSQLiteMigrations(t))
	if err := infra.NewStoreManagementDatasource(current).Initialize(ctx); err != nil {
		t.Fatalf("initialize current store: %v", err)
	}

	afterCommand, afterTranscript := eventBodyBytesByID(t, dbPath)
	if diff := cmp.Diff(0, afterCommand); diff != "" {
		t.Fatalf("command_executed body bytes after migration (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff(beforeTranscript, afterTranscript); diff != "" {
		t.Fatalf("migration 048 rewrote a non-command body (-want +got):\n%s", diff)
	}
	t.Logf("command_executed body bytes: %d -> %d", beforeCommand, afterCommand)
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

func eventBodyBytesByID(t *testing.T, dbPath string) (commandBytes, transcriptBytes int) {
	t.Helper()

	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("sql.Open() error = %v", err)
	}
	defer func() { _ = db.Close() }()

	if err := db.QueryRow(`SELECT length(CAST(body AS BLOB)) FROM events WHERE id='evt-command'`).Scan(&commandBytes); err != nil {
		t.Fatalf("read command body bytes: %v", err)
	}
	if err := db.QueryRow(`SELECT length(CAST(body AS BLOB)) FROM events WHERE id='evt-transcript'`).Scan(&transcriptBytes); err != nil {
		t.Fatalf("read transcript body bytes: %v", err)
	}
	return commandBytes, transcriptBytes
}
