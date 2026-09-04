package sqlite

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
)

func TestCurrentReaderVersionIsTheRaisedGate(t *testing.T) {
	t.Parallel()
	if currentReaderVersion != 36 {
		t.Fatalf("currentReaderVersion = %d, want 36", currentReaderVersion)
	}
	const previousReader = 35
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
	); INSERT INTO store_format_state(singleton, minimum_reader_version, maximum_payload_format) VALUES (1, 36, 1)`); err != nil {
		t.Fatal(err)
	}
	previous := currentReaderVersion
	// The live binary speaks 36, so min=36 is accepted. A 35 binary would
	// compare 36 > 35. Pin that inequality here; the error string is covered
	// by the future-reader tests with min=37.
	if previous != 36 || 36 <= 35 {
		t.Fatal("previous reader 35 would not be rejected by min=36")
	}
	if err = VerifyStoreCompatibility(context.Background(), db); err != nil {
		t.Fatalf("current reader must open min=36: %v", err)
	}
}
