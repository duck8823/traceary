package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
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
	if _, err = store.StartSearchProjectionGeneration(ctx, budget, now); err != nil {
		t.Fatal(err)
	}
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
	if err = db.QueryRow(`SELECT group_concat(event_id,',') FROM search_projection_recent_documents ORDER BY created_at_norm,event_rowid`).Scan(&ids); err != nil {
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

func projectionBudget() apptypes.SearchProjectionBudget {
	return apptypes.SearchProjectionBudget{Rows: 1, WallTime: time.Minute, LockTime: time.Second, StoredBytes: 1 << 20, DecodedBytes: 1 << 20, WriteBytes: 1 << 20, RecentAge: time.Hour, RecentBytes: 1 << 20}
}

func TestSearchProjectionGenerationFreezesInsertsAndDetectsMutation(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "store.db")
	migrations, _ := fs.Sub(os.DirFS("../.."), "schema/sqlite/migrations")
	store := NewDatabase(path, migrations)
	if err := store.initialize(ctx); err != nil {
		t.Fatal(err)
	}
	db, _ := sql.Open("sqlite", sqliteDSN(path))
	defer func() { _ = db.Close() }()
	now := time.Date(2026, 8, 2, 12, 0, 0, 0, time.UTC)
	insert := func(id string) {
		if _, err := db.Exec(`INSERT INTO events(id,kind,agent,session_id,body,created_at,client,workspace) VALUES(?,'note','a','s','visible 1234567890abcdef',?,'c','w')`, id, formatTimestamp(now)); err != nil {
			t.Fatal(err)
		}
	}
	insert("random-z")
	insert("random-y")
	b := projectionBudget()
	g, err := store.StartSearchProjectionGeneration(ctx, b, now)
	if err != nil {
		t.Fatal(err)
	}
	insert("random-a")
	p, err := store.RebuildSearchProjections(ctx, b, now)
	if err != nil || p.Completed || p.Written != 1 {
		t.Fatalf("progress=%+v err=%v", p, err)
	}
	var count int
	_ = db.QueryRow(`SELECT count(*) FROM search_projection_recent_documents WHERE generation_id=?`, g.GenerationID).Scan(&count)
	if count != 1 {
		t.Fatalf("documents=%d", count)
	}
	if _, err = db.Exec(`UPDATE events SET body='changed' WHERE id='random-z'`); err != nil {
		t.Fatal(err)
	}
	_, err = store.RebuildSearchProjections(ctx, b, now)
	var drift *apptypes.SearchProjectionDriftError
	if !errors.As(err, &drift) {
		t.Fatalf("error=%T %v", err, err)
	}
	ng, err := store.StartSearchProjectionGeneration(ctx, b, now)
	if err != nil || ng.GenerationID == g.GenerationID {
		t.Fatalf("new generation=%+v err=%v", ng, err)
	}
}

func TestSearchProjectionUnavailableBodyAndOversizeArePublicBehavior(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "store.db")
	migrations, _ := fs.Sub(os.DirFS("../.."), "schema/sqlite/migrations")
	store := NewDatabase(path, migrations)
	if err := store.initialize(ctx); err != nil {
		t.Fatal(err)
	}
	db, _ := sql.Open("sqlite", sqliteDSN(path))
	defer func() { _ = db.Close() }()
	now := time.Now().UTC()
	_, err := db.Exec(`INSERT INTO events(id,kind,agent,session_id,body,body_availability,created_at,client,workspace) VALUES('hidden','note','a','s','SECRET','unavailable_retention',?,'c','w')`, formatTimestamp(now))
	if err != nil {
		t.Fatal(err)
	}
	b := projectionBudget()
	if _, err = store.StartSearchProjectionGeneration(ctx, b, now); err != nil {
		t.Fatal(err)
	}
	if _, err = store.RebuildSearchProjections(ctx, b, now); err != nil {
		t.Fatal(err)
	}
	var body string
	_ = db.QueryRow(`SELECT body_text FROM search_projection_recent_documents`).Scan(&body)
	if strings.Contains(body, "SECRET") {
		t.Fatal("unavailable body leaked")
	}
	b.StoredBytes = 1
	if _, err = store.StartSearchProjectionGeneration(ctx, b, now); err != nil {
		t.Fatal(err)
	}
	_, err = store.RebuildSearchProjections(ctx, b, now)
	var oversized *apptypes.SearchProjectionOversizeError
	if !errors.As(err, &oversized) {
		t.Fatalf("error=%T %v", err, err)
	}
}

func TestSearchProjectionResumeSurvivesVacuumAndExcludesThinking(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "store.db")
	migrations, _ := fs.Sub(os.DirFS("../.."), "schema/sqlite/migrations")
	store := NewDatabase(path, migrations)
	if err := store.initialize(ctx); err != nil {
		t.Fatal(err)
	}
	db, _ := sql.Open("sqlite", sqliteDSN(path))
	defer func() { _ = db.Close() }()
	now := time.Now().UTC()
	envelope := `{"blocks":[{"type":"thinking","text":"PRIVATE-THOUGHT-123456"},{"type":"text","text":"PUBLIC-TEXT-123456"}]}`
	for _, id := range []string{"first", "second"} {
		if _, err := db.Exec(`INSERT INTO events(id,kind,agent,session_id,body,created_at,client,workspace) VALUES(?,'note','a','s',?,?,'c','w')`, id, envelope, formatTimestamp(now)); err != nil {
			t.Fatal(err)
		}
	}
	b := projectionBudget()
	if _, err := store.StartSearchProjectionGeneration(ctx, b, now); err != nil {
		t.Fatal(err)
	}
	p, err := store.RebuildSearchProjections(ctx, b, now)
	if err != nil || p.Completed {
		t.Fatalf("first=%+v %v", p, err)
	}
	if _, err = db.Exec(`VACUUM`); err != nil {
		t.Fatal(err)
	}
	p, err = store.RebuildSearchProjections(ctx, b, now)
	if err != nil || !p.Completed {
		t.Fatalf("resume=%+v %v", p, err)
	}
	var bodies, summaries string
	if err = db.QueryRow(`SELECT group_concat(body_text), (SELECT group_concat(summary_text) FROM search_projection_session_summaries) FROM search_projection_recent_documents`).Scan(&bodies, &summaries); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(bodies+summaries, "PRIVATE-THOUGHT") || !strings.Contains(bodies+summaries, "PUBLIC-TEXT") {
		t.Fatalf("visible projection=%q %q", bodies, summaries)
	}
	var matches int
	if err = db.QueryRow(`SELECT count(*) FROM search_projection_recent_fts WHERE search_projection_recent_fts MATCH ?`, `"PUBLIC-TEXT"`).Scan(&matches); err != nil || matches != 2 {
		t.Fatalf("matches=%d err=%v", matches, err)
	}
}
