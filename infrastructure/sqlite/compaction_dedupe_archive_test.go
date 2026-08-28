package sqlite

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/duck8823/traceary/application"
)

const createDedupeArchiveTableSQL = `
CREATE TABLE event_content_dedupe_archive (
    id TEXT NOT NULL,
    kind TEXT NOT NULL,
    client TEXT NOT NULL,
    agent TEXT NOT NULL,
    session_id TEXT NOT NULL,
    workspace TEXT NOT NULL,
    body TEXT NOT NULL,
    created_at TEXT NOT NULL,
    source_hook TEXT,
    kept_event_id TEXT NOT NULL,
    dedupe_run_id TEXT NOT NULL,
    archived_at TEXT NOT NULL,
    group_key TEXT NOT NULL,
    reason TEXT NOT NULL,
    PRIMARY KEY (dedupe_run_id, id)
)`

func insertDedupeArchiveRow(t *testing.T, db *sql.DB, id string, archivedAt time.Time) {
	t.Helper()
	insertDedupeArchiveRowForRun(t, db, "run-1", id, archivedAt)
}

func insertDedupeArchiveRowForRun(t *testing.T, db *sql.DB, runID, id string, archivedAt time.Time) {
	t.Helper()
	if _, err := db.Exec(`
INSERT INTO event_content_dedupe_archive
    (id, kind, client, agent, session_id, workspace, body, created_at, source_hook, kept_event_id, dedupe_run_id, archived_at, group_key, reason)
VALUES (?, 'transcript', 'claude', 'claude', 'session-1', 'workspace-1', 'body', ?, NULL, 'kept-1', ?, ?, 'group-1', 'duplicate')`,
		id, formatTimestamp(archivedAt), runID, formatTimestamp(archivedAt)); err != nil {
		t.Fatal(err)
	}
}

func openCandidateArchiveIDs(t *testing.T, candidate string) []string {
	t.Helper()
	db, err := sql.Open("sqlite", directSQLiteRWDSN(candidate))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	rows, err := db.Query(`SELECT id FROM event_content_dedupe_archive ORDER BY id`)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			t.Fatal(err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return ids
}

func openCandidateArchiveRunIDs(t *testing.T, candidate string) []string {
	t.Helper()
	db, err := sql.Open("sqlite", directSQLiteRWDSN(candidate))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	rows, err := db.Query(`SELECT DISTINCT dedupe_run_id FROM event_content_dedupe_archive ORDER BY dedupe_run_id`)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			t.Fatal(err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return ids
}

func TestSQLiteCompactionBuildPreservesRecentDedupeArchive(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	source := filepath.Join(dir, "source.db")
	candidate := filepath.Join(dir, "candidate.db")

	db, err := sql.Open("sqlite", directSQLiteRWDSNCreate(source))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(createDedupeArchiveTableSQL); err != nil {
		t.Fatal(err)
	}
	insertDedupeArchiveRow(t, db, "recent-1", time.Now().UTC().Add(-24*time.Hour))
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(candidate, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	builder := SQLiteCompactionBuilder{Filter: application.CompactFilter{
		ArchiveCutoff: time.Now().UTC().Add(-application.DedupeArchiveRetention),
	}}
	if err := builder.Build(ctx, source, candidate); err != nil {
		t.Fatal(err)
	}

	got := openCandidateArchiveIDs(t, candidate)
	if len(got) != 1 || got[0] != "recent-1" {
		t.Fatalf("expected recent archive row to survive compact, got %v", got)
	}
}

func TestSQLiteCompactionBuildDiscardsOverRetentionDedupeArchive(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	source := filepath.Join(dir, "source.db")
	candidate := filepath.Join(dir, "candidate.db")

	db, err := sql.Open("sqlite", directSQLiteRWDSNCreate(source))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(createDedupeArchiveTableSQL); err != nil {
		t.Fatal(err)
	}
	insertDedupeArchiveRow(t, db, "recent-1", time.Now().UTC().Add(-24*time.Hour))
	insertDedupeArchiveRow(t, db, "old-1", time.Now().UTC().Add(-application.DedupeArchiveRetention-24*time.Hour))
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(candidate, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	builder := SQLiteCompactionBuilder{Filter: application.CompactFilter{
		ArchiveCutoff: time.Now().UTC().Add(-application.DedupeArchiveRetention),
	}}
	if err := builder.Build(ctx, source, candidate); err != nil {
		t.Fatal(err)
	}

	got := openCandidateArchiveIDs(t, candidate)
	if len(got) != 1 || got[0] != "recent-1" {
		t.Fatalf("expected only the recent archive row to survive compact, got %v", got)
	}
}

func TestSQLiteCompactionBuildArchiveAbsentNoOp(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	source := filepath.Join(dir, "source.db")
	candidate := filepath.Join(dir, "candidate.db")

	db, err := sql.Open("sqlite", directSQLiteRWDSNCreate(source))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TABLE sample(id TEXT PRIMARY KEY, body BLOB)`); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(candidate, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	builder := SQLiteCompactionBuilder{}
	if err := builder.Build(ctx, source, candidate); err != nil {
		t.Fatal(err)
	}

	candidateDB, err := sql.Open("sqlite", directSQLiteRWDSN(candidate))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = candidateDB.Close() }()
	exists, err := tableExists(ctx, candidateDB, "event_content_dedupe_archive")
	if err != nil {
		t.Fatal(err)
	}
	if exists {
		t.Fatal("expected no dedupe archive table when source predates it")
	}
}
