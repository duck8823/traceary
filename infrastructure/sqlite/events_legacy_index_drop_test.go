package sqlite_test

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	"github.com/duck8823/traceary/infrastructure/sqlite"
)

var supersededEventsCreatedAtIndexes = []string{
	"idx_events_created_at",
	"idx_events_session_created_at",
	"idx_events_session_created_at_id_desc",
	"idx_events_workspace_created_at",
	"idx_events_source_hook_time",
}

func TestMigration071DropsSupersededEventsCreatedAtIndexes(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "store.db")
	before := sqlite.NewStoreManagementDatasource(sqlite.NewDatabase(path, onDiskSQLiteMigrationsBefore(t, 71)))
	if err := before.Initialize(ctx); err != nil {
		t.Fatal(err)
	}
	conn, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()
	for _, name := range supersededEventsCreatedAtIndexes {
		var n int
		if err := conn.QueryRow(`SELECT COUNT(*) FROM sqlite_schema WHERE type='index' AND name=?`, name).Scan(&n); err != nil {
			t.Fatal(err)
		}
		if n != 1 {
			t.Fatalf("pre-071 missing %s", name)
		}
	}

	current := sqlite.NewStoreManagementDatasource(sqlite.NewDatabase(path, onDiskSQLiteMigrations(t)))
	if err := current.Initialize(ctx); err != nil {
		t.Fatal(err)
	}
	for _, name := range supersededEventsCreatedAtIndexes {
		var n int
		if err := conn.QueryRow(`SELECT COUNT(*) FROM sqlite_schema WHERE type='index' AND name=?`, name).Scan(&n); err != nil {
			t.Fatal(err)
		}
		if n != 0 {
			t.Fatalf("post-071 still has %s", name)
		}
	}
}
