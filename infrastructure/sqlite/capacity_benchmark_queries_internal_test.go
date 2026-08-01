package sqlite

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
)

func TestCapacityBenchmarkQueriesShareProductionSources(t *testing.T) {
	path := filepath.Join(t.TempDir(), "queries.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TABLE event_search_backfill_state(singleton INTEGER,last_event_id TEXT,target_event_id TEXT,completed INTEGER,updated_at TEXT); INSERT INTO event_search_backfill_state VALUES(1,'',NULL,1,'now')`); err != nil {
		t.Fatal(err)
	}
	queries, err := CapacityBenchmarkQueries(context.Background(), db)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	if len(queries) != 3 {
		t.Fatalf("query count = %d", len(queries))
	}
	if queries[0].SQL != findLatestSessionQuery || queries[1].SQL != findLatestSessionQuery {
		t.Fatal("active/latest do not share production FindLatest source")
	}
	if queries[1].Args[12] != false {
		t.Fatal("latest must use production activeOnly=false binding")
	}
	plans := CapacityHandoffPlanQueries()
	if plans[0].SQL != listSessionsQuery || plans[1].SQL != selectRecentCommandPreviewsQuery {
		t.Fatal("handoff plans do not share production SQL sources")
	}
}
