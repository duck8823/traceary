package sqlite

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestActiveSessionQueryStreamsStartedCandidatesBeforeHydration(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "traceary.db")
	database := NewDatabase(path, os.DirFS(filepath.Join("..", "..", "schema", "sqlite", "migrations")))
	if err := NewStoreManagementDatasource(database).Initialize(ctx); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", sqliteDSN(path))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	args := []any{"session_started", "", "", "", "", "", "", "session_started", "session_ended"}
	rows, err := db.QueryContext(ctx, "EXPLAIN QUERY PLAN "+findActiveSessionQuery, args...)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	var plan strings.Builder
	for rows.Next() {
		var id, parent, unused int
		var detail string
		if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
			t.Fatal(err)
		}
		plan.WriteString(detail)
		plan.WriteByte('\n')
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	normalized := strings.ToLower(plan.String())
	if !strings.Contains(normalized, "search started using index idx_event_metadata_kind_created_at_norm_id_desc (kind=?)") {
		t.Fatalf("active candidates are not streamed in time order from the body-free kind index:\n%s", plan.String())
	}
	if strings.Contains(normalized, "use temp b-tree for order by") {
		t.Fatalf("active lookup sorts all candidates instead of stopping at the first match:\n%s", plan.String())
	}
	if !strings.Contains(normalized, "search e using index sqlite_autoindex_events_1 (id=?)") {
		t.Fatalf("active lookup does not hydrate only the selected identity:\n%s", plan.String())
	}
}
