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

func TestPageMetadataInspectorReadsPragmas(t *testing.T) {
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
