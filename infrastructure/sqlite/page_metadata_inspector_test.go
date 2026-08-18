package sqlite_test

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"

	_ "modernc.org/sqlite"

	infra "github.com/duck8823/traceary/infrastructure/sqlite"
)

func TestPageMetadataInspectorReadsPragmasAndProjectionSingleton(t *testing.T) {
	path := filepath.Join(t.TempDir(), "pages.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	secret := "PRIVATE-SENTINEL-page-metadata"
	statements := []string{
		`PRAGMA journal_mode=WAL`,
		`CREATE TABLE events (id TEXT PRIMARY KEY, body TEXT)`,
		`INSERT INTO events VALUES ('evt-1', ?)`,
		`CREATE TABLE scratch (payload BLOB)`,
		`INSERT INTO scratch(payload) VALUES (zeroblob(65536))`,
		`DELETE FROM scratch`,
		`CREATE TABLE search_projection_state (
			singleton INTEGER PRIMARY KEY CHECK (singleton=1),
			active_generation_id TEXT,
			state TEXT,
			phase TEXT
		)`,
		`INSERT INTO search_projection_state(singleton, active_generation_id, state, phase)
		 VALUES (1, 'gen-o1', 'complete', 'idle')`,
	}
	for index, statement := range statements {
		var execErr error
		if strings.Contains(statement, "?") {
			_, execErr = db.Exec(statement, strings.Repeat(secret, 20))
		} else {
			_, execErr = db.Exec(statement)
		}
		if execErr != nil {
			t.Fatalf("exec fixture statement %d: %v", index, execErr)
		}
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	meta, err := infra.NewPageMetadataInspector().InspectPageMetadata(context.Background(), path)
	if err != nil {
		t.Fatalf("InspectPageMetadata() error = %v", err)
	}
	if meta.PageSizeBytes <= 0 || meta.PageCount <= 0 {
		t.Fatalf("page accounting missing: %+v", meta)
	}
	if meta.DatabaseBytes != meta.PageSizeBytes*meta.PageCount {
		t.Fatalf("database bytes = %d, want %d*%d", meta.DatabaseBytes, meta.PageSizeBytes, meta.PageCount)
	}
	if meta.ReclaimableBytes != meta.PageSizeBytes*meta.FreePages {
		t.Fatalf("reclaimable = %d, want %d*%d", meta.ReclaimableBytes, meta.PageSizeBytes, meta.FreePages)
	}
	if meta.FreePages == 0 || meta.ReclaimableBytes == 0 {
		t.Fatalf("want a non-zero freelist after delete, got %+v", meta)
	}
	if !meta.ProjectionPresent || meta.ProjectionGenerationID != "gen-o1" || meta.ProjectionState != "complete" || meta.ProjectionPhase != "idle" {
		t.Fatalf("projection singleton = %+v", meta)
	}
}

func TestPageMetadataInspectorOmitsMissingProjectionTable(t *testing.T) {
	path := filepath.Join(t.TempDir(), "no-projection.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TABLE sample (value TEXT)`); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	meta, err := infra.NewPageMetadataInspector().InspectPageMetadata(context.Background(), path)
	if err != nil {
		t.Fatalf("InspectPageMetadata() error = %v", err)
	}
	if meta.ProjectionPresent || meta.ProjectionGenerationID != "" {
		t.Fatalf("want absent projection, got %+v", meta)
	}
	if meta.PageCount == 0 {
		t.Fatalf("want pragma page_count, got %+v", meta)
	}
}

func TestPageMetadataInspectorRejectsInvalidSQLiteFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "not-sqlite.db")
	if err := os.WriteFile(path, []byte("not a database"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := infra.NewPageMetadataInspector().InspectPageMetadata(context.Background(), path)
	if err == nil {
		t.Fatal("InspectPageMetadata() error = nil, want invalid-file failure")
	}
}
