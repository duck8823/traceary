package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"testing"
)

func writeGuardTestStore(t *testing.T, path string, ids []string) {
	t.Helper()
	db, err := sql.Open("sqlite", directSQLiteRWDSNCreate(path))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	if _, err := db.Exec(`CREATE TABLE events(id TEXT PRIMARY KEY, kind TEXT NOT NULL, agent TEXT NOT NULL, session_id TEXT NOT NULL, body TEXT NOT NULL, created_at TEXT NOT NULL)`); err != nil {
		t.Fatal(err)
	}
	for _, id := range ids {
		if _, err := db.Exec(`INSERT INTO events(id, kind, agent, session_id, body, created_at) VALUES(?, 'prompt', 'muse', 's', 'b', '2026-09-06T00:00:00Z')`, id); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
}

func guardTestIDs(prefix string, n int) []string {
	ids := make([]string, n)
	for i := range ids {
		ids[i] = fmt.Sprintf("%s-%04d", prefix, i)
	}
	return ids
}

func TestSQLiteCompactionRollbackGuard(t *testing.T) {
	ctx := context.Background()
	guard := SQLiteCompactionRollbackGuard{}

	t.Run("identical stores report zero", func(t *testing.T) {
		dir := t.TempDir()
		ids := guardTestIDs("ev", 10)
		published := filepath.Join(dir, "published.db")
		rollback := filepath.Join(dir, "rollback.db")
		writeGuardTestStore(t, published, ids)
		writeGuardTestStore(t, rollback, ids)
		got, err := guard.CountRecordsAtRisk(ctx, published, rollback)
		if err != nil {
			t.Fatal(err)
		}
		if got != 0 {
			t.Fatalf("CountRecordsAtRisk = %d, want 0", got)
		}
	})

	t.Run("post-snapshot records are counted", func(t *testing.T) {
		dir := t.TempDir()
		base := guardTestIDs("ev", 10)
		published := filepath.Join(dir, "published.db")
		rollback := filepath.Join(dir, "rollback.db")
		writeGuardTestStore(t, published, append(append([]string{}, base...), "new-1", "new-2"))
		writeGuardTestStore(t, rollback, base)
		got, err := guard.CountRecordsAtRisk(ctx, published, rollback)
		if err != nil {
			t.Fatal(err)
		}
		if got != 2 {
			t.Fatalf("CountRecordsAtRisk = %d, want 2", got)
		}
	})

	t.Run("chunked scan stays exact past one chunk", func(t *testing.T) {
		dir := t.TempDir()
		ids := guardTestIDs("ev", 2*rollbackGuardLookupChunk+50)
		published := filepath.Join(dir, "published.db")
		rollback := filepath.Join(dir, "rollback.db")
		writeGuardTestStore(t, published, append(append([]string{}, ids...), "late-1"))
		writeGuardTestStore(t, rollback, ids)
		got, err := guard.CountRecordsAtRisk(ctx, published, rollback)
		if err != nil {
			t.Fatal(err)
		}
		if got != 1 {
			t.Fatalf("CountRecordsAtRisk = %d, want 1", got)
		}
	})

	t.Run("missing rollback inode fails closed", func(t *testing.T) {
		dir := t.TempDir()
		published := filepath.Join(dir, "published.db")
		writeGuardTestStore(t, published, guardTestIDs("ev", 3))
		if _, err := guard.CountRecordsAtRisk(ctx, published, filepath.Join(dir, "absent.db")); err == nil {
			t.Fatal("expected error for missing rollback inode")
		}
	})

	t.Run("missing events table means nothing at risk", func(t *testing.T) {
		dir := t.TempDir()
		published := filepath.Join(dir, "published.db")
		db, err := sql.Open("sqlite", directSQLiteRWDSNCreate(published))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(`CREATE TABLE other(id TEXT PRIMARY KEY)`); err != nil {
			t.Fatal(err)
		}
		if err := db.Close(); err != nil {
			t.Fatal(err)
		}
		rollback := filepath.Join(dir, "rollback.db")
		writeGuardTestStore(t, rollback, guardTestIDs("ev", 1))
		got, err := guard.CountRecordsAtRisk(ctx, published, rollback)
		if err != nil {
			t.Fatal(err)
		}
		if got != 0 {
			t.Fatalf("CountRecordsAtRisk = %d, want 0", got)
		}
	})
}
