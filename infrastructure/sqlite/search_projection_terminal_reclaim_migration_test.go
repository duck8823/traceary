package sqlite_test

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	_ "modernc.org/sqlite"
)

func TestMigration000074BackfillsTerminalLifecycleColumns(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "traceary.db")
	pre := onDiskSQLiteMigrationsBefore(t, 74)
	if err := newStoreManagementDatasource(t, dbPath, pre).Initialize(ctx); err != nil {
		t.Fatalf("Initialize(pre-74): %v", err)
	}
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`
		INSERT INTO search_projection_generation_lifecycle(generation_id,state,config_hash,source_revision,high_water,abandoned_at)
		VALUES('gen-failed','failed','h',0,1,''),
		      ('gen-other','failed','h',0,1,''),
		      ('gen-abandoned','abandoned','h',0,1,'2026-07-01T00:00:00Z');
		UPDATE search_projection_state
		   SET generation_id='gen-failed', state='failed', failure_class='oversize_row', updated_at='2026-08-01T00:00:00Z'
		 WHERE singleton=1;
	`); err != nil {
		_ = db.Close()
		t.Fatalf("seed: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	if err := newStoreManagementDatasource(t, dbPath, onDiskSQLiteMigrations(t)).InitializeAuthorized(ctx); err != nil {
		t.Fatalf("Initialize(current): %v", err)
	}
	db, err = sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	var class, terminal string
	if err := db.QueryRow(`SELECT failure_class, terminal_at FROM search_projection_generation_lifecycle WHERE generation_id='gen-failed'`).Scan(&class, &terminal); err != nil {
		t.Fatal(err)
	}
	if class != "oversize_row" || terminal == "" {
		t.Fatalf("gen-failed class=%q terminal=%q", class, terminal)
	}
	if err := db.QueryRow(`SELECT failure_class, terminal_at FROM search_projection_generation_lifecycle WHERE generation_id='gen-other'`).Scan(&class, &terminal); err != nil {
		t.Fatal(err)
	}
	if class != "" || terminal != "" {
		t.Fatalf("unrelated generation backfilled class=%q terminal=%q", class, terminal)
	}
	if err := db.QueryRow(`SELECT terminal_at FROM search_projection_generation_lifecycle WHERE generation_id='gen-abandoned'`).Scan(&terminal); err != nil {
		t.Fatal(err)
	}
	if terminal != "2026-07-01T00:00:00Z" {
		t.Fatalf("abandoned terminal_at=%q", terminal)
	}
}
