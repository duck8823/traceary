package sqlite_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	_ "modernc.org/sqlite"

	infra "github.com/duck8823/traceary/infrastructure/sqlite"
)

func TestCapacityInspectorReturnsMetadataOnlyAggregates(t *testing.T) {
	path := filepath.Join(t.TempDir(), "capacity.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	secret := "PRIVATE-SENTINEL-event-identifier"
	statements := []string{
		`PRAGMA journal_mode=WAL`,
		`CREATE TABLE events (id TEXT PRIMARY KEY, kind TEXT, agent TEXT, session_id TEXT, body TEXT, created_at TEXT)`,
		`CREATE INDEX idx_events_created_at ON events(created_at)`,
		`INSERT INTO events VALUES (?, 'prompt', 'agent', 'session-secret', ?, '2026-01-01T00:00:00Z')`,
	}
	for index, statement := range statements {
		var execErr error
		if index == len(statements)-1 {
			_, execErr = db.Exec(statement, secret, strings.Repeat(secret, 100))
		} else {
			_, execErr = db.Exec(statement)
		}
		if execErr != nil {
			t.Fatalf("exec fixture statement %d: %v", index, execErr)
		}
	}
	report, err := infra.NewCapacityInspector(infra.NewDatabase(path, nil)).InspectCapacity(context.Background())
	if err != nil {
		t.Fatalf("InspectCapacity() error = %v", err)
	}
	encoded, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), secret) || strings.Contains(string(encoded), "session-secret") {
		t.Fatalf("capacity report leaked stored content: %s", encoded)
	}
	if report.SchemaVersion != "traceary.capacity/v1" || report.PageCount == 0 {
		t.Fatalf("unexpected report: %+v", report)
	}
	if len(report.PayloadClasses) != 1 || report.PayloadClasses[0].Rows != 1 {
		t.Fatalf("unexpected payload classes: %+v", report.PayloadClasses)
	}
	if report.Evidence.Status != "complete" && report.Evidence.Status != "partial" {
		t.Fatalf("unexpected evidence: %+v", report.Evidence)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestCapacityInspectorHandlesStoreWithoutEvents(t *testing.T) {
	path := filepath.Join(t.TempDir(), "empty.db")
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
	report, err := infra.NewCapacityInspector(infra.NewDatabase(path, nil)).InspectCapacity(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(report.PayloadClasses) != 0 {
		t.Fatalf("PayloadClasses = %+v, want empty", report.PayloadClasses)
	}
}
