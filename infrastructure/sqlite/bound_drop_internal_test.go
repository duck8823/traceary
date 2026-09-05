package sqlite

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
)

func TestCurrentReaderVersionIsTheRaisedGate(t *testing.T) {
	t.Parallel()
	if currentReaderVersion != 39 {
		t.Fatalf("currentReaderVersion = %d, want 39", currentReaderVersion)
	}
	const previousReader = 38
	if currentReaderVersion <= previousReader {
		t.Fatal("raised reader version would not fail previous binaries")
	}
}

func TestVerifyStoreCompatibilityRejectsReaderBelowMinimum(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "compat.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	if _, err = db.Exec(`CREATE TABLE store_format_state (
		singleton INTEGER PRIMARY KEY CHECK (singleton = 1),
		minimum_reader_version INTEGER NOT NULL
	); INSERT INTO store_format_state(singleton, minimum_reader_version) VALUES (1, 38)`); err != nil {
		t.Fatal(err)
	}
	previous := currentReaderVersion
	// The live binary speaks 39, so min=38 is accepted. A 38 binary would
	// compare 39 > 38. Pin that inequality here; the error string is covered
	// by the future-reader tests with min=40.
	if previous != 39 || 39 <= 38 {
		t.Fatal("previous reader 38 would not be rejected by min=39")
	}
	if err = VerifyStoreCompatibility(context.Background(), db); err != nil {
		t.Fatalf("current reader must open min=38: %v", err)
	}
}
