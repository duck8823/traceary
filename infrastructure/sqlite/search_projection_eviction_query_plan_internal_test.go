package sqlite

import (
	"context"
	"database/sql"
	"strings"
	"testing"
)

func TestSelectProjectionEvictionUsesEvictionIndexWithoutTempBTree(t *testing.T) {
	t.Parallel()
	path := t.TempDir() + "/projection.db"
	db, err := sql.Open("sqlite", sqliteDSN(path))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	if _, err := db.ExecContext(context.Background(), `CREATE TABLE search_projection_recent_documents(document_id INTEGER PRIMARY KEY,generation_id TEXT NOT NULL,event_rowid INTEGER NOT NULL,event_id TEXT NOT NULL,created_at_norm TEXT NOT NULL,body_text TEXT NOT NULL); CREATE INDEX idx_search_projection_recent_eviction ON search_projection_recent_documents(generation_id,created_at_norm,event_rowid);`); err != nil {
		t.Fatal(err)
	}
	rows, err := db.QueryContext(context.Background(), "EXPLAIN QUERY PLAN "+selectProjectionEvictionQuery, "2026-08-10T00:00:00.000000000Z", "g", 2)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	var plan []string
	for rows.Next() {
		var id, parent, unused int
		var detail string
		if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
			t.Fatal(err)
		}
		plan = append(plan, detail)
	}
	joined := strings.Join(plan, "\n")
	if !strings.Contains(joined, "idx_search_projection_recent_eviction") {
		t.Fatalf("plan does not use eviction index: %s", joined)
	}
	if strings.Contains(joined, "TEMP B-TREE") {
		t.Fatalf("plan uses temporary b-tree: %s", joined)
	}
}
