package sqlite

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	apptypes "github.com/duck8823/traceary/application/types"
)

func TestReportWindowSQLDoesNotUseOffset(t *testing.T) {
	t.Parallel()
	for name, query := range map[string]string{
		"sessions": listReportSessionsQuery,
		"usage":    listReportUsageQuery,
		"commands": listReportCommandAuditsQuery,
	} {
		if strings.Contains(strings.ToUpper(query), "OFFSET") {
			t.Errorf("%s report SQL still uses OFFSET", name)
		}
		if strings.Contains(query, "ts_norm(s.started_at)") ||
			strings.Contains(query, "ts_norm(started_at)") ||
			strings.Contains(query, "ts_norm(observation.observed_at)") ||
			strings.Contains(query, "ts_norm(e.created_at)") {
			t.Errorf("%s report SQL still wraps a stored column in ts_norm", name)
		}
	}
}

func TestReportSessionQueryPlanUsesStartedAtNormIndex(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "traceary.db")
	database := NewDatabase(dbPath, os.DirFS(filepath.Join("..", "..", "schema", "sqlite", "migrations")))
	if err := NewStoreManagementDatasource(database).Initialize(ctx); err != nil {
		t.Fatalf("Initialize() error = %v", err)
	}
	db, err := sql.Open("sqlite", sqliteDSN(dbPath))
	if err != nil {
		t.Fatalf("sql.Open() error = %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	from := time.Date(2026, 7, 23, 0, 0, 0, 0, time.UTC).Format(time.RFC3339Nano)
	to := time.Date(2026, 7, 24, 0, 0, 0, 0, time.UTC).Format(time.RFC3339Nano)
	rows, err := db.QueryContext(
		ctx,
		"EXPLAIN QUERY PLAN "+listReportSessionsQuery,
		"workspace", "workspace",
		"codex", "codex",
		from, from, to, to, "", "", "", 10,
		"workspace", "workspace",
		"codex", "codex",
		from, from, to, to,
	)
	if err != nil {
		t.Fatalf("EXPLAIN QUERY PLAN error = %v", err)
	}
	t.Cleanup(func() { _ = rows.Close() })
	joined := strings.ToLower(joinQueryPlan(t, rows))
	if !strings.Contains(joined, "idx_sessions_started_at_norm_id_desc") &&
		!strings.Contains(joined, "started_at_norm") {
		t.Fatalf("session report plan does not mention started_at_norm:\n%s", joined)
	}
}

func TestReportUsageQueryPlanUsesObservedAtNormIndex(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "traceary.db")
	database := NewDatabase(dbPath, os.DirFS(filepath.Join("..", "..", "schema", "sqlite", "migrations")))
	if err := NewStoreManagementDatasource(database).Initialize(ctx); err != nil {
		t.Fatalf("Initialize() error = %v", err)
	}
	db, err := sql.Open("sqlite", sqliteDSN(dbPath))
	if err != nil {
		t.Fatalf("sql.Open() error = %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	from := time.Date(2026, 7, 23, 0, 0, 0, 0, time.UTC).Format(time.RFC3339Nano)
	to := time.Date(2026, 7, 24, 0, 0, 0, 0, time.UTC).Format(time.RFC3339Nano)
	rows, err := db.QueryContext(
		ctx,
		"EXPLAIN QUERY PLAN "+listReportUsageQuery,
		"workspace", "workspace",
		"codex", "codex",
		from, from, to, to, "", "", "", 10,
	)
	if err != nil {
		t.Fatalf("EXPLAIN QUERY PLAN error = %v", err)
	}
	t.Cleanup(func() { _ = rows.Close() })
	joined := strings.ToLower(joinQueryPlan(t, rows))
	if !strings.Contains(joined, "idx_usage_observations_status_observed_at_norm_id_desc") &&
		!strings.Contains(joined, "observed_at_norm") {
		t.Fatalf("usage report plan does not mention observed_at_norm:\n%s", joined)
	}
}

func TestCatchUpReportWindowNormStampsEmptyColumns(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "traceary.db")
	database := NewDatabase(dbPath, os.DirFS(filepath.Join("..", "..", "schema", "sqlite", "migrations")))
	if err := NewStoreManagementDatasource(database).Initialize(ctx); err != nil {
		t.Fatalf("Initialize() error = %v", err)
	}
	db, err := sql.Open("sqlite", sqliteDSN(dbPath))
	if err != nil {
		t.Fatalf("sql.Open() error = %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	startedAt := time.Date(2026, 7, 23, 12, 0, 0, 0, time.UTC).Format(time.RFC3339Nano)
	if _, err := db.ExecContext(ctx, `
		INSERT INTO sessions (session_id, started_at, client, agent, workspace, started_at_norm)
		VALUES ('catchup-session', ?, 'codex', 'codex', 'workspace', '')
	`, startedAt); err != nil {
		t.Fatalf("insert session: %v", err)
	}
	if _, err := db.ExecContext(ctx, `UPDATE sessions SET started_at_norm = '' WHERE session_id = 'catchup-session'`); err != nil {
		t.Fatalf("clear started_at_norm: %v", err)
	}

	result, err := catchUpReportWindowNorm(ctx, db, 10)
	if err != nil {
		t.Fatalf("catchUpReportWindowNorm() error = %v", err)
	}
	if result.SessionsUpdated != 1 {
		t.Fatalf("sessions updated = %d, want 1", result.SessionsUpdated)
	}
	var stored string
	if err := db.QueryRowContext(ctx, `SELECT started_at_norm FROM sessions WHERE session_id = 'catchup-session'`).Scan(&stored); err != nil {
		t.Fatalf("select started_at_norm: %v", err)
	}
	want := formatMemoryValidityTimestamp(time.Date(2026, 7, 23, 12, 0, 0, 0, time.UTC))
	if stored != want {
		t.Fatalf("started_at_norm = %q, want %q", stored, want)
	}

	criteria, err := apptypes.ReportCriteriaFrom(
		"2026-07-23T00:00:00Z", "2026-07-24T00:00:00Z", "UTC",
		time.Date(2026, 7, 24, 0, 0, 0, 0, time.UTC),
		"workspace", "codex", 10, 0,
	)
	if err != nil {
		t.Fatalf("ReportCriteriaFrom() error = %v", err)
	}
	window, err := NewReportDatasource(database).LoadReportWindow(ctx, criteria)
	if err != nil {
		t.Fatalf("LoadReportWindow() error = %v", err)
	}
	if len(window.Sessions) != 1 || window.Sessions[0].SessionID != "catchup-session" {
		t.Fatalf("sessions after catch-up = %+v", window.Sessions)
	}
}

func joinQueryPlan(t *testing.T, rows *sql.Rows) string {
	t.Helper()
	var plan []string
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
	return strings.Join(plan, "\n")
}
