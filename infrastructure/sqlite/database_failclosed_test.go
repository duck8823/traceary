package sqlite

import (
	"context"
	"database/sql"
	"path/filepath"
	"strings"
	"testing"
)

func TestVerifyStoreCompatibility_Pre36LineageIsRoutable(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "pre36.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	if _, err = db.Exec(`
		CREATE TABLE schema_migrations (version INTEGER PRIMARY KEY, name TEXT NOT NULL);
		INSERT INTO schema_migrations(version, name) VALUES (35, '000035.sql');
	`); err != nil {
		t.Fatal(err)
	}
	if err = VerifyStoreCompatibility(context.Background(), db); err != nil {
		t.Fatalf("pre-36 lineage must route to migration 36: %v", err)
	}
}

func TestVerifyStoreCompatibility_MissingStateWithoutLineageFailsClosed(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "foreign.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	if _, err = db.Exec(`CREATE TABLE unrelated(id INTEGER PRIMARY KEY)`); err != nil {
		t.Fatal(err)
	}
	err = VerifyStoreCompatibility(context.Background(), db)
	if err == nil {
		t.Fatal("expected fail-closed error")
	}
	if !strings.Contains(err.Error(), "store_format_state is missing") {
		t.Fatalf("error = %v", err)
	}
}

func TestVerifyStoreCompatibility_EmptyFileIsBootstrap(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "empty.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	if err = VerifyStoreCompatibility(context.Background(), db); err != nil {
		t.Fatalf("empty bootstrap must be allowed: %v", err)
	}
}

func TestVerifyStoreCompatibility_NegativeReaderRefused(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "neg.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	if _, err = db.Exec(`
		CREATE TABLE store_format_state (
			singleton INTEGER PRIMARY KEY CHECK (singleton = 1),
			minimum_reader_version INTEGER NOT NULL
		);
		INSERT INTO store_format_state(singleton, minimum_reader_version) VALUES (1, -1);
	`); err != nil {
		t.Fatal(err)
	}
	err = VerifyStoreCompatibility(context.Background(), db)
	if err == nil || !strings.Contains(err.Error(), "negative") {
		t.Fatalf("error = %v, want negative versions", err)
	}
}

func TestVerifyStoreCompatibility_NewerReaderRefused(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "future.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	if _, err = db.Exec(`
		CREATE TABLE store_format_state (
			singleton INTEGER PRIMARY KEY CHECK (singleton = 1),
			minimum_reader_version INTEGER NOT NULL
		);
		INSERT INTO store_format_state(singleton, minimum_reader_version) VALUES (1, 43);
	`); err != nil {
		t.Fatal(err)
	}
	err = VerifyStoreCompatibility(context.Background(), db)
	if err == nil || !strings.Contains(err.Error(), "this reader supports 42") {
		t.Fatalf("error = %v, want future-reader refusal", err)
	}
}
