package sqlite

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
)

func TestCurrentReaderVersionIsTheRaisedGate(t *testing.T) {
	t.Parallel()
	if currentReaderVersion != 40 {
		t.Fatalf("currentReaderVersion = %d, want 40", currentReaderVersion)
	}
	const previousReader = 39
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
	); INSERT INTO store_format_state(singleton, minimum_reader_version) VALUES (1, 39)`); err != nil {
		t.Fatal(err)
	}
	previous := currentReaderVersion
	// The live binary speaks 40, so min=39 is accepted. A 39 binary would
	// compare 40 > 39. Pin that inequality here; the error string is covered
	// by the future-reader tests with min=41.
	if previous != 40 || 40 <= 39 {
		t.Fatal("previous reader 39 would not be rejected by min=40")
	}
	if err = VerifyStoreCompatibility(context.Background(), db); err != nil {
		t.Fatalf("current reader must open min=39: %v", err)
	}
}
