package sqlite

import (
	"context"
	"database/sql"
	"path/filepath"
	"strings"
	"testing"

	_ "modernc.org/sqlite"
)

func TestVerifyDropEncodedPayloadsSkipsRetentionIndexWhen083Pends(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dir := t.TempDir()
	candidate := filepath.Join(dir, "candidate.db")
	mustOpenEmptySQLite(t, candidate)
	seedEncodedPayloadSurvivorSchema(t, candidate, false)

	candidateDB, err := sql.Open("sqlite", candidate)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = candidateDB.Close() }()

	// verifyDropEncodedPayloads threads dropRetentionPending into
	// verifyEncodedPayloadSurvivors, which is the candidate-missing-index check.
	if err := verifyEncodedPayloadSurvivors(ctx, candidateDB, true); err != nil {
		t.Fatalf("verifyEncodedPayloadSurvivors(dropRetentionPending=true) = %v, want nil", err)
	}
	err = verifyEncodedPayloadSurvivors(ctx, candidateDB, false)
	if err == nil {
		t.Fatal("verifyEncodedPayloadSurvivors(dropRetentionPending=false) = nil, want missing-index")
	}
	if !strings.Contains(err.Error(), bodyRetentionCandidateIndex) {
		t.Fatalf("error = %v, want candidate-missing-index for %s", err, bodyRetentionCandidateIndex)
	}
}

func TestEvaluateConservationLawSkipsRawBodyRetentionWhen083Pends(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dir := t.TempDir()
	source := filepath.Join(dir, "source.db")
	candidate := filepath.Join(dir, "candidate.db")
	mustOpenEmptySQLite(t, source)
	mustOpenEmptySQLite(t, candidate)

	sourceDB, err := sql.Open("sqlite", source)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = sourceDB.Close() }()
	candidateDB, err := sql.Open("sqlite", candidate)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = candidateDB.Close() }()

	if err := evaluateConservationLaw(ctx, sourceDB, candidateDB, 45, false, true); err != nil {
		t.Fatalf("evaluateConservationLaw(45, dropRetentionPending=true) = %v, want nil", err)
	}
	err = evaluateConservationLaw(ctx, sourceDB, candidateDB, 45, false, false)
	if err == nil {
		t.Fatal("evaluateConservationLaw(45, dropRetentionPending=false) = nil, want missing-index")
	}
	if !strings.Contains(err.Error(), "idx_raw_body_retention_entries_event_id") {
		t.Fatalf("error = %v, want missing idx_raw_body_retention_entries_event_id", err)
	}
}

func mustOpenEmptySQLite(t *testing.T, path string) {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`SELECT 1`); err != nil {
		_ = db.Close()
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
}

func seedEncodedPayloadSurvivorSchema(t *testing.T, path string, withRetentionIndex bool) {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	stmts := []string{
		`CREATE TABLE events(id TEXT PRIMARY KEY, kind TEXT, session_id TEXT, created_at TEXT, agent TEXT, client TEXT, workspace TEXT, source_hook TEXT, created_at_norm TEXT, body BLOB)`,
		`CREATE TABLE command_audits(event_id TEXT PRIMARY KEY, command_text TEXT, input_text TEXT, output_text TEXT)`,
		`CREATE TABLE store_format_state(singleton INTEGER PRIMARY KEY CHECK (singleton = 1), minimum_reader_version INTEGER NOT NULL)`,
		`INSERT INTO store_format_state(singleton, minimum_reader_version) VALUES (1, 38)`,
		`CREATE TABLE event_content_dedupe_archive(event_id TEXT PRIMARY KEY)`,
	}
	for _, name := range survivingEventsTriggerNames {
		stmts = append(stmts, `CREATE TRIGGER `+name+` AFTER INSERT ON events BEGIN SELECT 1; END`)
	}
	for _, name := range survivingAuditTriggerNames {
		stmts = append(stmts, `CREATE TRIGGER `+name+` AFTER INSERT ON command_audits BEGIN SELECT 1; END`)
	}
	for _, name := range survivingEventsIndexNames {
		if name == bodyRetentionCandidateIndex && !withRetentionIndex {
			continue
		}
		stmts = append(stmts, `CREATE INDEX `+name+` ON events(id)`)
	}
	for _, stmt := range stmts {
		if _, err := db.Exec(stmt); err != nil {
			t.Fatalf("seed %s: %v\n%s", path, err, stmt)
		}
	}
}
