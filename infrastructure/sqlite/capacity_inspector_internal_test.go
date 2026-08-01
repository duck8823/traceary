package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"

	apptypes "github.com/duck8823/traceary/application/types"
)

func TestCapacityInspectorDBStatFallbackClassification(t *testing.T) {
	path := filepath.Join(t.TempDir(), "fallback.db")
	raw, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := raw.Exec(`CREATE TABLE events(id TEXT, body TEXT); CREATE TABLE event_metadata_projection(id TEXT, body_stored_bytes INTEGER)`); err != nil {
		t.Fatal(err)
	}
	if err := raw.Close(); err != nil {
		t.Fatal(err)
	}

	unavailable := newCapacityInspectorWithDBStatQuery(NewDatabase(path, nil), func(context.Context, *sql.DB, int64) ([]apptypes.CapacityObject, error) {
		return nil, errors.New("no such table: dbstat")
	})
	report, err := unavailable.InspectCapacity(context.Background())
	if err != nil {
		t.Fatalf("unavailable fallback error = %v", err)
	}
	if report.Evidence.Status != "unavailable" {
		t.Fatalf("Evidence = %+v", report.Evidence)
	}

	unexpected := newCapacityInspectorWithDBStatQuery(NewDatabase(path, nil), func(context.Context, *sql.DB, int64) ([]apptypes.CapacityObject, error) {
		return nil, errors.New("disk I/O error")
	})
	if _, err := unexpected.InspectCapacity(context.Background()); err == nil {
		t.Fatal("unexpected dbstat error was swallowed")
	}
}
