package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	_ "modernc.org/sqlite"

	apptypes "github.com/duck8823/traceary/application/types"
	"github.com/duck8823/traceary/domain/types"
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
			args:      []any{"", "", "", "", "", "", "", "", "", "", 0, "", "", "", "", "", "", "", "", 25, 100},
			wantIndex: "idx_events_created_at_norm_id_desc",
		},
		{
			name:      "workspace list with offset",
			query:     selectRecentEventMetadataByWorkspaceQuery,
			args:      []any{"repo-current", "", "", "", "", "", "", "", "", 0, "", "", "", "", "", "", "", "", 25, 100},
			wantIndex: "idx_events_workspace_created_at_norm_id_desc",
		},
		{
			name:      "session list with offset",
			query:     selectRecentEventMetadataBySessionQuery,
			args:      []any{"session-1", "", "", "", "", "", "", "", "", 0, "", "", "", "", "", "", "", "", 25, 100},
			wantIndex: "idx_events_session_created_at_norm_id_desc",
		},
		{
			name:      "workspace session context",
			query:     getContextEventMetadataByWorkspaceSessionQuery,
			args:      []any{"repo-current", "session-1", "", "", "", "", "", "", 25, 0},
			wantIndex: "idx_events_workspace_session_created_at_norm_id_desc",
		},
		{
			name:      "source hook list with offset",
			query:     selectRecentEventMetadataBySourceHookQuery,
			args:      []any{"stop", "", "", "", "", "", "", "", "", "", "", 0, "", "", "", "", "", "", "", "", 25, 100},
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

func TestMetadataRangeAndLegacyFallbackPlansUseProductionMigrationIndexes(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "traceary.db")
	migrations := os.DirFS(filepath.Join("..", "..", "schema", "sqlite", "migrations"))
	if err := NewStoreManagementDatasource(NewDatabase(dbPath, migrations)).Initialize(ctx); err != nil {
		t.Fatalf("Initialize() error = %v", err)
	}
	db, err := sql.Open("sqlite", sqliteDSN(dbPath))
	if err != nil {
		t.Fatalf("sql.Open() error = %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	for _, row := range []struct {
		id, kind, sourceHook, body, createdAt string
	}{
		{"tagged", "session_ended", "subagent_stop", "tagged", "2026-07-25T00:00:02Z"},
		{"legacy", "session_ended", "", "[phase:subagent] legacy", "2026-07-25T00:00:01Z"},
	} {
		if _, err := db.ExecContext(ctx, `INSERT INTO events(id, kind, client, agent, session_id, workspace, body, created_at, source_hook, body_availability)
			VALUES (?, ?, 'hook', 'codex', 'session-1', 'repo-current', ?, ?, NULLIF(?, ''), 'available')`, row.id, row.kind, row.body, row.createdAt, row.sourceHook); err != nil {
			t.Fatalf("insert %s: %v", row.id, err)
		}
	}

	from := "2026-07-25T00:00:00.000000000Z"
	to := "2026-07-25T00:00:03.000000000Z"
	criteria := apptypes.NewEventListCriteriaBuilder(25).
		Workspace(types.Workspace("repo-current")).
		From(time.Date(2026, 7, 25, 0, 0, 0, 0, time.UTC)).
		To(time.Date(2026, 7, 25, 0, 0, 3, 0, time.UTC)).
		Build()
	query, args := scopedRecentEventMetadataQuery(criteria, 0, from, to, 25, 0)
	assertPlanUsesDirectRangeIndex(t, explainQueryPlan(t, db, query, args...), "idx_events_workspace_created_at_norm_id_desc")

	legacyQuery := metadataTimeRangeQuery(selectRecentEventMetadataBySourceHookWithLegacyQuery, from, to)
	legacyArgs := metadataSourceHookLegacyQueryArgs(
		"subagent_stop", "", "", "", "", "", 0, from, to, apptypes.EventPageAnchor{}, 25, 0,
	)
	assertPlanUsesDirectRangeIndex(t, explainQueryPlan(t, db, legacyQuery, legacyArgs...), "idx_events_created_at_norm_id_desc")
}

func assertPlanUsesOrderedIndex(t *testing.T, plan []string, index string) {
	t.Helper()
	joined := strings.Join(plan, "\n")
	if !strings.Contains(joined, index) {
		t.Fatalf("query plan does not use %s:\n%s", index, joined)
	}
	if strings.Contains(joined, "USE TEMP B-TREE") {
		t.Fatalf("query plan sorts outside its ordering index:\n%s", joined)
	}
}

func assertPlanUsesDirectRangeIndex(t *testing.T, plan []string, index string) {
	t.Helper()
	assertPlanUsesOrderedIndex(t, plan, index)
	if joined := strings.Join(plan, "\n"); !strings.Contains(joined, "created_at_norm>? AND created_at_norm<?") {
		t.Fatalf("query plan does not seek both direct timestamp bounds:\n%s", joined)
	}
}

func TestMetadataRangeQueryP95OnLargeMigratedStore(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "traceary.db")
	migrations := os.DirFS(filepath.Join("..", "..", "schema", "sqlite", "migrations"))
	if err := NewStoreManagementDatasource(NewDatabase(dbPath, migrations)).Initialize(ctx); err != nil {
		t.Fatalf("Initialize() error = %v", err)
	}
	db, err := sql.Open("sqlite", sqliteDSN(dbPath))
	if err != nil {
		t.Fatalf("sql.Open() error = %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("BeginTx() error = %v", err)
	}
	statement, err := tx.PrepareContext(ctx, `INSERT INTO events(id, kind, client, agent, session_id, workspace, body, created_at, body_availability)
		VALUES (?, 'note', 'hook', 'codex', 'session-1', 'repo-current', '', ?, 'available')`)
	if err != nil {
		t.Fatalf("PrepareContext() error = %v", err)
	}
	base := time.Date(2026, 7, 25, 0, 0, 0, 0, time.UTC)
	for i := 0; i < 10000; i++ {
		if _, err := statement.ExecContext(ctx, fmt.Sprintf("event-%05d", i), base.Add(time.Duration(i)*time.Millisecond).Format(time.RFC3339Nano)); err != nil {
			_ = statement.Close()
			_ = tx.Rollback()
			t.Fatalf("insert fixture %d: %v", i, err)
		}
	}
	if err := statement.Close(); err != nil {
		t.Fatalf("statement.Close() error = %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("Commit() error = %v", err)
	}
	criteria := apptypes.NewEventListCriteriaBuilder(50).
		Workspace(types.Workspace("repo-current")).
		From(base.Add(4 * time.Second)).
		To(base.Add(6 * time.Second)).
		Build()
	query, args := scopedRecentEventMetadataQuery(
		criteria, 0,
		formatMetadataOptionalTimestamp(criteria.From()),
		formatMetadataOptionalTimestamp(criteria.To()),
		criteria.Limit(), criteria.Offset(),
	)
	durations := make([]time.Duration, 0, 25)
	for range 25 {
		started := time.Now()
		rows, err := db.QueryContext(ctx, query, args...)
		if err != nil {
			t.Fatalf("range query: %v", err)
		}
		for rows.Next() {
			var values [16]any
			pointers := make([]any, len(values))
			for i := range values {
				pointers[i] = &values[i]
			}
			if err := rows.Scan(pointers...); err != nil {
				_ = rows.Close()
				t.Fatalf("scan range row: %v", err)
			}
		}
		if err := rows.Close(); err != nil {
			t.Fatalf("rows.Close() error = %v", err)
		}
		durations = append(durations, time.Since(started))
	}
	sort.Slice(durations, func(i, j int) bool { return durations[i] < durations[j] })
	p95 := durations[(len(durations)*95+99)/100-1]
	t.Logf("10k migrated metadata events, workspace direct range query p95=%s", p95)
	if p95 >= 50*time.Millisecond {
		t.Fatalf("range query p95=%s, want < 50ms", p95)
	}
}

// BenchmarkMetadataDirectRangeMultiGiB creates actual SQLite-managed pages and
// event bodies. It is intentionally opt-in because the setup writes at least
// 2 GiB; CI keeps the 10k-event smoke test above and never creates this store.
func BenchmarkMetadataDirectRangeMultiGiB(b *testing.B) {
	if os.Getenv("TRACEARY_RUN_MULTI_GIB_BENCHMARK") != "1" {
		b.Skip("set TRACEARY_RUN_MULTI_GIB_BENCHMARK=1 to create the multi-GiB fixture")
	}
	const (
		bodyBytes       = 256 << 20
		eventCount      = 8
		minimumDBBytes  = int64(2 << 30)
		measurementRuns = 25
	)
	ctx := context.Background()
	dbPath := filepath.Join(b.TempDir(), "traceary.db")
	migrations := os.DirFS(filepath.Join("..", "..", "schema", "sqlite", "migrations"))
	if err := NewStoreManagementDatasource(NewDatabase(dbPath, migrations)).Initialize(ctx); err != nil {
		b.Fatalf("Initialize() error = %v", err)
	}
	db, err := sql.Open("sqlite", sqliteDSN(dbPath))
	if err != nil {
		b.Fatalf("sql.Open() error = %v", err)
	}
	b.Cleanup(func() { _ = db.Close() })
	payload := strings.Repeat("x", bodyBytes)
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		b.Fatalf("BeginTx() error = %v", err)
	}
	statement, err := tx.PrepareContext(ctx, `INSERT INTO events(id, kind, client, agent, session_id, workspace, body, created_at, body_availability)
		VALUES (?, 'note', 'hook', 'codex', 'session-1', 'repo-current', ?, ?, 'available')`)
	if err != nil {
		_ = tx.Rollback()
		b.Fatalf("PrepareContext() error = %v", err)
	}
	base := time.Date(2026, 7, 25, 0, 0, 0, 0, time.UTC)
	for i := 0; i < eventCount; i++ {
		if _, err := statement.ExecContext(ctx, fmt.Sprintf("large-event-%03d", i), payload, base.Add(time.Duration(i)*time.Millisecond).Format(time.RFC3339Nano)); err != nil {
			_ = statement.Close()
			_ = tx.Rollback()
			b.Fatalf("insert fixture %d: %v", i, err)
		}
	}
	if err := statement.Close(); err != nil {
		b.Fatalf("statement.Close() error = %v", err)
	}
	if err := tx.Commit(); err != nil {
		b.Fatalf("Commit() error = %v", err)
	}
	if _, err := db.ExecContext(ctx, "PRAGMA wal_checkpoint(TRUNCATE)"); err != nil {
		b.Fatalf("checkpoint large fixture: %v", err)
	}
	var pageCount, pageSize, storedEvents, storedBodyBytes, missingStoredBodyBytes int64
	if err := db.QueryRowContext(ctx, "PRAGMA page_count").Scan(&pageCount); err != nil {
		b.Fatalf("read page_count: %v", err)
	}
	if err := db.QueryRowContext(ctx, "PRAGMA page_size").Scan(&pageSize); err != nil {
		b.Fatalf("read page_size: %v", err)
	}
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*), COALESCE(SUM(body_stored_bytes), 0),
		SUM(CASE WHEN body_stored_bytes IS NULL THEN 1 ELSE 0 END)
		FROM events`).Scan(&storedEvents, &storedBodyBytes, &missingStoredBodyBytes); err != nil {
		b.Fatalf("verify large fixture: %v", err)
	}
	if pageCount*pageSize < minimumDBBytes || storedEvents != eventCount || missingStoredBodyBytes != 0 || storedBodyBytes < minimumDBBytes {
		b.Fatalf("large fixture verification failed: page_count=%d page_size=%d managed_bytes=%d events=%d stored_body_bytes=%d missing_stored_body_bytes=%d", pageCount, pageSize, pageCount*pageSize, storedEvents, storedBodyBytes, missingStoredBodyBytes)
	}
	b.ReportMetric(float64(pageCount*pageSize), "managed_bytes")
	b.ReportMetric(float64(storedEvents), "events")
	b.ReportMetric(float64(storedEvents-missingStoredBodyBytes), "non_null_body_metadata")
	b.ReportMetric(float64(storedBodyBytes), "stored_body_bytes")

	criteria := apptypes.NewEventListCriteriaBuilder(50).
		Workspace(types.Workspace("repo-current")).
		From(base).
		To(base.Add(time.Second)).
		Build()
	query, args := scopedRecentEventMetadataQuery(criteria, 0, formatMetadataOptionalTimestamp(criteria.From()), formatMetadataOptionalTimestamp(criteria.To()), criteria.Limit(), criteria.Offset())
	durations := make([]time.Duration, 0, measurementRuns)
	b.ResetTimer()
	for range measurementRuns {
		started := time.Now()
		rows, err := db.QueryContext(ctx, query, args...)
		if err != nil {
			b.Fatalf("range query: %v", err)
		}
		for rows.Next() {
			var values [16]any
			pointers := make([]any, len(values))
			for i := range values {
				pointers[i] = &values[i]
			}
			if err := rows.Scan(pointers...); err != nil {
				_ = rows.Close()
				b.Fatalf("scan range row: %v", err)
			}
		}
		if err := rows.Close(); err != nil {
			b.Fatalf("rows.Close() error = %v", err)
		}
		durations = append(durations, time.Since(started))
	}
	b.StopTimer()
	sort.Slice(durations, func(i, j int) bool { return durations[i] < durations[j] })
	p95 := durations[(len(durations)*95+99)/100-1]
	b.ReportMetric(float64(p95)/float64(time.Millisecond), "p95_ms")
	if p95 >= 250*time.Millisecond {
		b.Fatalf("multi-GiB direct range p95=%s, want < 250ms", p95)
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
	// SQLite's busy handler counts requested sleep durations. An interrupted
	// platform sleep can therefore return the busy outcome before the full
	// configured timeout has elapsed in wall-clock time. The DSN tests verify
	// the exact pragma value; this regression only guards the upper bound.
	maximumBusyOutcome := 2 * time.Duration(sqliteBusyTimeout) * time.Millisecond
	if elapsed >= maximumBusyOutcome {
		t.Fatalf("busy metadata read elapsed = %s, want < %s", elapsed, maximumBusyOutcome)
	}
}
