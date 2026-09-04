package sqlite

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCompactPreservesRepairCompletion(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	source := filepath.Join(dir, "source.db")
	seedRetiredOneOffStore(t, source, false)
	if err := NewStoreManagementDatasource(NewDatabase(source, preparedMigrations(t))).InitializeAuthorized(ctx); err != nil {
		t.Fatalf("InitializeAuthorized(078) error = %v", err)
	}

	candidate := filepath.Join(dir, "candidate.db")
	if err := os.WriteFile(candidate, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	builder := SQLiteCompactionBuilder{}
	if err := builder.Build(ctx, source, candidate); err != nil {
		t.Fatalf("Build error = %v", err)
	}
	if err := builder.VerifyPair(ctx, source, candidate); err != nil {
		t.Fatalf("VerifyPair error = %v", err)
	}

	db, err := sql.Open("sqlite", candidate)
	if err != nil {
		t.Fatal(err)
	}
	var version int64
	if err := db.QueryRow(`SELECT version FROM schema_migrations WHERE version = 78`).Scan(&version); err != nil {
		t.Fatalf("078 missing from candidate schema_migrations: %v", err)
	}
	var exhausted int
	if err := db.QueryRow(`SELECT exhausted FROM workspace_observation_catchup_state WHERE singleton = 1`).Scan(&exhausted); err != nil {
		t.Fatalf("read candidate exhausted: %v", err)
	}
	if exhausted != 1 {
		t.Fatalf("candidate exhausted = %d, want 1", exhausted)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	log := &repairStatementLog{}
	installRepairStatementObserver(t, candidate, log)
	openRetiredStoreSkippingProjection(t, candidate)
	log.reset()
	openRetiredStoreSkippingProjection(t, candidate)
	if kinds := log.forbidden(); len(kinds) != 0 {
		t.Fatalf("compacted candidate second open issued repair-object statements %v\nqueries:\n%s", kinds, strings.Join(log.queries, "\n"))
	}
}
