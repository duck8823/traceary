package sqlite

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"

	_ "modernc.org/sqlite"
)

func TestListTimelineBlocksQueryUsesCreatedAtNormIndex(t *testing.T) {
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

	rows, err := db.QueryContext(
		ctx,
		"EXPLAIN QUERY PLAN "+listTimelineBlocksQuery,
		"", "", "", "", "", "", 2000, 900, 10,
	)
	if err != nil {
		t.Fatalf("EXPLAIN QUERY PLAN error = %v", err)
	}
	t.Cleanup(func() { _ = rows.Close() })
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
	joined := strings.Join(plan, "\n")
	if !strings.Contains(strings.ToLower(joined), "idx_events_created_at_norm_id_desc") &&
		!strings.Contains(strings.ToLower(joined), "created_at_norm") {
		t.Fatalf("query plan does not mention created_at_norm index:\n%s", joined)
	}
	if strings.Contains(listTimelineBlocksQuery, "e.body") {
		t.Fatal("gap-walk SQL still selects e.body")
	}
}

func TestTimelineEventScanCapScalesWithLimit(t *testing.T) {
	t.Parallel()
	if got := timelineEventScanCap(1); got != 2000 {
		t.Fatalf("scan cap for limit 1 = %d, want floor 2000", got)
	}
	if got := timelineEventScanCap(20); got != 5000 {
		t.Fatalf("scan cap for limit 20 = %d, want 5000", got)
	}
}
