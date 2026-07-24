package sqlite

import (
	"context"
	"database/sql"
	"path/filepath"
	"strings"
	"testing"
	"time"

	_ "modernc.org/sqlite"

	apptypes "github.com/duck8823/traceary/application/types"
)

func TestBoundedLatestEventMetadataQueryPlanUsesCreatedAtIndex(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "traceary.db")
	db, err := sql.Open("sqlite", sqliteDSN(dbPath))
	if err != nil {
		t.Fatalf("sql.Open() error = %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.ExecContext(context.Background(), `
		CREATE TABLE events (
			id TEXT PRIMARY KEY,
			kind TEXT NOT NULL,
			client TEXT NOT NULL,
			agent TEXT NOT NULL,
			session_id TEXT NOT NULL,
			workspace TEXT NOT NULL,
			source_hook TEXT,
			created_at TEXT NOT NULL,
			body_original_bytes INTEGER,
			body_stored_bytes INTEGER NOT NULL,
			body_ingest_truncated BOOLEAN,
			body_storage_truncated BOOLEAN,
			body_metadata_version INTEGER
		);
		CREATE TABLE command_audits (event_id TEXT PRIMARY KEY, exit_code INTEGER, failed BOOLEAN);
		CREATE INDEX idx_events_created_at ON events(created_at DESC, id DESC);
	`); err != nil {
		t.Fatalf("create query-plan fixture: %v", err)
	}
	rows, err := db.QueryContext(context.Background(), "EXPLAIN QUERY PLAN "+selectLatestEventMetadataFastQuery)
	if err != nil {
		t.Fatalf("EXPLAIN QUERY PLAN error = %v", err)
	}
	defer func() { _ = rows.Close() }()
	plan := make([]string, 0)
	for rows.Next() {
		var id, parent, unused int
		var detail string
		if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
			t.Fatalf("scan query plan: %v", err)
		}
		plan = append(plan, detail)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate query plan: %v", err)
	}
	if joined := strings.Join(plan, "\n"); !strings.Contains(joined, "idx_events_created_at") {
		t.Fatalf("bounded latest query does not use created_at index:\n%s", joined)
	}
}

func TestEventMetadataQuery_BoundedLatestReportsBusyInsteadOfSlowInitialization(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "traceary.db")
	setup, err := sql.Open("sqlite", "file:"+dbPath)
	if err != nil {
		t.Fatalf("sql.Open setup error = %v", err)
	}
	if _, err := setup.ExecContext(context.Background(), `
		CREATE TABLE events (
			id TEXT PRIMARY KEY, kind TEXT NOT NULL, client TEXT NOT NULL,
			agent TEXT NOT NULL, session_id TEXT NOT NULL, workspace TEXT NOT NULL,
			source_hook TEXT, created_at TEXT NOT NULL, body_original_bytes INTEGER,
			body_stored_bytes INTEGER NOT NULL, body_ingest_truncated BOOLEAN,
			body_storage_truncated BOOLEAN, body_metadata_version INTEGER
		);
		CREATE TABLE command_audits (event_id TEXT PRIMARY KEY, exit_code INTEGER, failed BOOLEAN);
		CREATE INDEX idx_events_created_at ON events(created_at DESC, id DESC);
	`); err != nil {
		_ = setup.Close()
		t.Fatalf("create busy fixture: %v", err)
	}
	if err := setup.Close(); err != nil {
		t.Fatalf("setup.Close() error = %v", err)
	}

	locker, err := sql.Open("sqlite", "file:"+dbPath)
	if err != nil {
		t.Fatalf("sql.Open locker error = %v", err)
	}
	t.Cleanup(func() { _ = locker.Close() })
	if _, err := locker.ExecContext(context.Background(), "BEGIN EXCLUSIVE"); err != nil {
		t.Fatalf("BEGIN EXCLUSIVE error = %v", err)
	}
	defer func() { _, _ = locker.ExecContext(context.Background(), "ROLLBACK") }()

	sut := NewEventDatasource(NewDatabase(dbPath, nil))
	started := time.Now()
	_, err = sut.ListRecentMetadata(context.Background(), apptypes.NewEventListCriteriaBuilder(1).Build())
	elapsed := time.Since(started)
	if err == nil {
		t.Fatal("ListRecentMetadata() error = nil, want SQLite busy/locked outcome")
	}
	if !strings.Contains(strings.ToLower(err.Error()), "busy") && !strings.Contains(strings.ToLower(err.Error()), "locked") {
		t.Fatalf("ListRecentMetadata() error = %v, want busy/locked classification", err)
	}
	if elapsed < 800*time.Millisecond || elapsed >= 2*time.Second {
		t.Fatalf("busy metadata read elapsed = %s, want configured short busy outcome", elapsed)
	}
}
