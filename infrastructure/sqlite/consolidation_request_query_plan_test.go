package sqlite

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

func TestConsolidationRequestQueriesUseBoundedIndexes(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "traceary.db")
	database := NewDatabase(dbPath, os.DirFS(filepath.Join("..", "..", "schema", "sqlite", "migrations")))
	if err := NewStoreManagementDatasource(database).Initialize(ctx); err != nil {
		t.Fatalf("Initialize() error = %v", err)
	}

	raw, err := sql.Open("sqlite", sqliteDSN(dbPath))
	if err != nil {
		t.Fatalf("sql.Open() error = %v", err)
	}
	t.Cleanup(func() { _ = raw.Close() })

	since := formatMemoryValidityTimestamp(time.Date(2026, 8, 22, 0, 0, 0, 0, time.UTC))
	t.Run("conversion uses requested_at client index", func(t *testing.T) {
		plan := explainConsolidationQueryPlan(t, raw, selectConsolidationConversionQuery, since)
		joined := strings.ToLower(strings.Join(plan, "\n"))
		if !strings.Contains(joined, "idx_consolidation_requests_requested_at_client") {
			t.Fatalf("conversion plan missing requested_at index:\n%s", strings.Join(plan, "\n"))
		}
		if strings.Contains(joined, "scan consolidation_requests") {
			t.Fatalf("conversion plan scans consolidation_requests:\n%s", strings.Join(plan, "\n"))
		}
	})
	t.Run("authorship reaches session_refinements by primary key", func(t *testing.T) {
		plan := explainConsolidationQueryPlan(t, raw, selectConsolidationRefinementAuthorshipQuery, since)
		joined := strings.ToLower(strings.Join(plan, "\n"))
		if !strings.Contains(joined, "session_refinements") {
			t.Fatalf("authorship plan does not mention session_refinements:\n%s", strings.Join(plan, "\n"))
		}
		if strings.Contains(joined, "scan session_refinements") {
			t.Fatalf("authorship plan scans session_refinements:\n%s", strings.Join(plan, "\n"))
		}
	})
	t.Run("latest event id uses created_at_norm index", func(t *testing.T) {
		plan := explainConsolidationQueryPlan(t, raw, selectLatestEventIDForSessionByNormQuery, "sess-1")
		joined := strings.ToLower(strings.Join(plan, "\n"))
		if !strings.Contains(joined, "idx_events_session_created_at_norm_id_desc") {
			t.Fatalf("latest event plan missing session created_at_norm index:\n%s", strings.Join(plan, "\n"))
		}
		if strings.Contains(joined, "scan events") {
			t.Fatalf("latest event plan scans events:\n%s", strings.Join(plan, "\n"))
		}
	})
}

func explainConsolidationQueryPlan(t *testing.T, db *sql.DB, query string, args ...any) []string {
	t.Helper()
	rows, err := db.Query("EXPLAIN QUERY PLAN "+query, args...)
	if err != nil {
		t.Fatalf("EXPLAIN QUERY PLAN error = %v", err)
	}
	defer func() { _ = rows.Close() }()
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
	return plan
}
