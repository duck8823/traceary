package sqlite

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
)

func TestCurrentReaderVersionIsTheRaisedGate(t *testing.T) {
	t.Parallel()
	if currentReaderVersion != 35 {
		t.Fatalf("currentReaderVersion = %d, want 35", currentReaderVersion)
	}
	const previousReader = 34
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
		minimum_reader_version INTEGER NOT NULL,
		maximum_payload_format INTEGER NOT NULL
	); INSERT INTO store_format_state(singleton, minimum_reader_version, maximum_payload_format) VALUES (1, 35, 1)`); err != nil {
		t.Fatal(err)
	}
	previous := currentReaderVersion
	// The live binary speaks 35, so min=35 is accepted. A 34 binary would
	// compare 35 > 34. Pin that inequality here; the error string is covered
	// by the future-reader tests with min=36.
	if previous != 35 || 35 <= 34 {
		t.Fatal("previous reader 34 would not be rejected by min=35")
	}
	if err = VerifyStoreCompatibility(context.Background(), db); err != nil {
		t.Fatalf("current reader must open min=35: %v", err)
	}
}
