package sqlite

import (
	"context"
	"database/sql"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
	"time"

	apptypes "github.com/duck8823/traceary/application/types"
)

func TestBundledSQLiteContentlessDeleteSemantics(t *testing.T) {
	t.Parallel()
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "probe.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	if _, err = db.Exec(`CREATE VIRTUAL TABLE probe USING fts5(value,content='',contentless_delete=1)`); err != nil {
		t.Fatalf("bundled SQLite lacks contentless_delete: %v", err)
	}
	if _, err = db.Exec(`INSERT INTO probe(rowid,value) VALUES(1,'alpha bravo'); UPDATE probe SET value='charlie delta' WHERE rowid=1; DELETE FROM probe WHERE rowid=1`); err != nil {
		t.Fatalf("contentless_delete mutation semantics: %v", err)
	}
	var integrity string
	if err = db.QueryRow(`PRAGMA integrity_check`).Scan(&integrity); err != nil || integrity != "ok" {
		t.Fatalf("integrity_check = %q, %v", integrity, err)
	}
	var count int
	if err = db.QueryRow(`SELECT count(*) FROM probe WHERE probe MATCH 'charlie'`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("deleted MATCH count = %d, %v", count, err)
	}
}

func TestSearchProjectionRebuildIsBoundedResumableAndEvictsDeterministically(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "store.db")
	migrations, err := fs.Sub(os.DirFS("../.."), "schema/sqlite/migrations")
	if err != nil {
		t.Fatal(err)
	}
	store := NewDatabase(dbPath, migrations)
	if err = store.initialize(ctx); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", sqliteDSN(dbPath))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	now := time.Date(2026, 8, 2, 12, 0, 0, 0, time.UTC)
	for i, row := range []struct{ id, session, body string }{
		{"a", "s1", "old 1234567890abcdef"}, {"b", "s1", "new 1234567890abcdef"}, {"c", "s2", "new abcdef1234567890"},
	} {
		created := now.Add(time.Duration(i-1) * time.Hour)
		if _, err = db.ExecContext(ctx, `INSERT INTO events(id,kind,agent,session_id,body,created_at,client,workspace) VALUES(?,'note','agent',?,?,?,'client','repo')`, row.id, row.session, row.body, formatTimestamp(created)); err != nil {
			t.Fatal(err)
		}
	}
	budget := apptypes.SearchProjectionBudget{Rows: 1, WallTime: time.Minute, LockTime: time.Second, StoredBytes: 1 << 20, DecodedBytes: 1 << 20, WriteBytes: 1 << 20, RecentAge: 90 * time.Minute, RecentBytes: 25}
	for i := 0; i < 3; i++ {
		got, e := rebuildSearchProjections(ctx, db, budget, now)
		if e != nil {
			t.Fatal(e)
		}
		if got.Selected != 1 {
			t.Fatalf("batch %d selected=%d", i, got.Selected)
		}
	}
	got, err := rebuildSearchProjections(ctx, db, budget, now)
	if err != nil || !got.Completed {
		t.Fatalf("idempotent completed rebuild=%+v err=%v", got, err)
	}
	var ids string
	if err = db.QueryRow(`SELECT group_concat(event_id,',') FROM search_projection_recent_documents ORDER BY created_at,event_id`).Scan(&ids); err != nil {
		t.Fatal(err)
	}
	if ids != "c" {
		t.Fatalf("deterministic retained IDs=%q, want c", ids)
	}
	var events, keywords int
	if err = db.QueryRow(`SELECT SUM(event_count),(SELECT COUNT(*) FROM search_projection_session_keywords) FROM search_projection_session_summaries`).Scan(&events, &keywords); err != nil {
		t.Fatal(err)
	}
	if events != 3 || keywords != 2 {
		t.Fatalf("aggregate events=%d keywords=%d", events, keywords)
	}
	var design string
	if err = db.QueryRow(`SELECT fts_design FROM search_projection_state`).Scan(&design); err != nil || design != "external_content" {
		t.Fatalf("design=%q err=%v", design, err)
	}
}
