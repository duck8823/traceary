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
		CREATE INDEX idx_events_workspace_created_at ON events(workspace, created_at DESC, id DESC);
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

func TestBoundedLatestEventMetadataByWorkspaceQueryPlanUsesTimestampIndex(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "traceary.db")
	db, err := sql.Open("sqlite", sqliteDSN(dbPath))
	if err != nil {
		t.Fatalf("sql.Open() error = %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.ExecContext(context.Background(), `
		CREATE TABLE events (
			id TEXT PRIMARY KEY, kind TEXT NOT NULL, client TEXT NOT NULL,
			agent TEXT NOT NULL, session_id TEXT NOT NULL, workspace TEXT NOT NULL,
			source_hook TEXT, created_at TEXT NOT NULL, created_at_norm TEXT NOT NULL, body_original_bytes INTEGER,
			body_stored_bytes INTEGER NOT NULL, body_ingest_truncated BOOLEAN,
			body_storage_truncated BOOLEAN, body_metadata_version INTEGER
		);
		CREATE TABLE command_audits (event_id TEXT PRIMARY KEY, exit_code INTEGER, failed BOOLEAN);
		CREATE INDEX idx_events_workspace_created_at ON events(workspace, created_at DESC, id DESC);
	`); err != nil {
		t.Fatalf("create workspace query-plan fixture: %v", err)
	}
	rows, err := db.QueryContext(context.Background(), "EXPLAIN QUERY PLAN "+selectLatestEventMetadataFastByWorkspaceQuery, "repo-current", "repo-current", "repo-current")
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
	if joined := strings.Join(plan, "\n"); !strings.Contains(joined, "idx_events_workspace_created_at") {
		t.Fatalf("bounded workspace query does not use workspace timestamp index:\n%s", joined)
	}
	if joined := strings.Join(plan, "\n"); strings.Contains(joined, "USE TEMP B-TREE FOR LAST TERM OF ORDER BY") {
		t.Fatalf("legacy workspace index must not sort every scoped row to choose the latest second:\n%s", joined)
	}
}

func TestGeneralMetadataListAndContextQueryPlansUseNormalizedTimestampIndexes(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "traceary.db")
	db, err := sql.Open("sqlite", sqliteDSN(dbPath))
	if err != nil {
		t.Fatalf("sql.Open() error = %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.ExecContext(context.Background(), `
		CREATE TABLE events (
			id TEXT PRIMARY KEY, kind TEXT NOT NULL, client TEXT NOT NULL,
			agent TEXT NOT NULL, session_id TEXT NOT NULL, workspace TEXT NOT NULL,
			source_hook TEXT, created_at TEXT NOT NULL, created_at_norm TEXT NOT NULL, body_original_bytes INTEGER,
			body_stored_bytes INTEGER NOT NULL, body_ingest_truncated BOOLEAN,
			body_storage_truncated BOOLEAN, body_metadata_version INTEGER
		);
		CREATE TABLE command_audits (event_id TEXT PRIMARY KEY, exit_code INTEGER, failed BOOLEAN);
		CREATE INDEX idx_events_created_at_norm_id_desc
			ON events(created_at_norm DESC, id DESC);
		CREATE INDEX idx_events_workspace_created_at_norm_id_desc
			ON events(workspace, created_at_norm DESC, id DESC);
		CREATE INDEX idx_events_session_created_at_norm_id_desc
			ON events(session_id, created_at_norm DESC, id DESC);
		CREATE INDEX idx_events_workspace_session_created_at_norm_id_desc
			ON events(workspace, session_id, created_at_norm DESC, id DESC);
		CREATE INDEX idx_events_source_hook_created_at_norm_id_desc
			ON events(source_hook, created_at_norm DESC, id DESC)
			WHERE source_hook IS NOT NULL;
	`); err != nil {
		t.Fatalf("create query-plan fixture: %v", err)
	}

	tests := []struct {
		name      string
		query     string
		args      []any
		wantIndex string
	}{
		{
			name:      "general list with offset",
			query:     selectRecentEventMetadataQuery,
			args:      []any{"", "", "", "", "", "", "", "", "", "", 0, "", "", "", "", 25, 100},
			wantIndex: "idx_events_created_at_norm_id_desc",
		},
		{
			name:      "workspace list with offset",
			query:     selectRecentEventMetadataByWorkspaceQuery,
			args:      []any{"repo-current", "", "", "", "", "", "", "", "", 0, "", "", "", "", 25, 100},
			wantIndex: "idx_events_workspace_created_at_norm_id_desc",
		},
		{
			name:      "session list with offset",
			query:     selectRecentEventMetadataBySessionQuery,
			args:      []any{"session-1", "", "", "", "", "", "", "", "", 0, "", "", "", "", 25, 100},
			wantIndex: "idx_events_session_created_at_norm_id_desc",
		},
		{
			name:      "workspace session context",
			query:     getContextEventMetadataByWorkspaceSessionQuery,
			args:      []any{"repo-current", "session-1", 25},
			wantIndex: "idx_events_workspace_session_created_at_norm_id_desc",
		},
		{
			name:      "source hook list with offset",
			query:     selectRecentEventMetadataBySourceHookQuery,
			args:      []any{"stop", "", "", "", "", "", "", "", "", "", "", 0, "", "", "", "", 25, 100},
			wantIndex: "idx_events_source_hook_created_at_norm_id_desc",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			plan := explainQueryPlan(t, db, tt.query, tt.args...)
			joined := strings.Join(plan, "\n")
			if !strings.Contains(joined, tt.wantIndex) {
				t.Fatalf("query plan does not use %s:\n%s", tt.wantIndex, joined)
			}
			if strings.Contains(joined, "USE TEMP B-TREE FOR ORDER BY") {
				t.Fatalf("query plan sorts metadata rows outside its ordering index:\n%s", joined)
			}
		})
	}
}

func explainQueryPlan(t *testing.T, db *sql.DB, query string, args ...any) []string {
	t.Helper()
	rows, err := db.QueryContext(context.Background(), "EXPLAIN QUERY PLAN "+query, args...)
	if err != nil {
		t.Fatalf("EXPLAIN QUERY PLAN error = %v", err)
	}
	t.Cleanup(func() { _ = rows.Close() })
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
	return plan
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
