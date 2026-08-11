package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"testing/fstest"
	"time"

	"github.com/google/go-cmp/cmp"

	apptypes "github.com/duck8823/traceary/application/types"
	"github.com/duck8823/traceary/application/usecase"
)

func migrationsBeforeSearchProjection(t *testing.T) fs.FS {
	t.Helper()
	entries, err := os.ReadDir("../../schema/sqlite/migrations")
	if err != nil {
		t.Fatal(err)
	}
	out := fstest.MapFS{}
	for _, entry := range entries {
		if entry.IsDir() || entry.Name() >= "000038" {
			continue
		}
		data, readErr := os.ReadFile(filepath.Join("../../schema/sqlite/migrations", entry.Name()))
		if readErr != nil {
			t.Fatal(readErr)
		}
		out[entry.Name()] = &fstest.MapFile{Data: data}
	}
	return out
}

func TestSearchProjectionMigrationIsSchemaOnlyUnderOneSecond(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "store.db")
	legacy := NewDatabase(path, migrationsBeforeSearchProjection(t))
	if err := legacy.initialize(ctx); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", sqliteDSN(path))
	if err != nil {
		t.Fatal(err)
	}
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	stmt, err := tx.Prepare(`INSERT INTO events(id,kind,agent,session_id,body,created_at,client,workspace) VALUES(?,'note','agent','session','body','2026-08-03T00:00:00Z','client','workspace')`)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 20_000; i++ {
		if _, err = stmt.Exec(fmt.Sprintf("historical-%08d", i)); err != nil {
			t.Fatal(err)
		}
	}
	_ = stmt.Close()
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	_ = db.Close()

	all, err := fs.Sub(os.DirFS("../.."), "schema/sqlite/migrations")
	if err != nil {
		t.Fatal(err)
	}
	started := time.Now()
	if err = NewDatabase(path, all).initialize(ctx); err != nil {
		t.Fatal(err)
	}
	// Schema install stays cheap; initialize also runs one bounded catch-up
	// unit (same 128-row budget as the CLI default). That must not scan the
	// full 20k historical set in one open.
	if elapsed := time.Since(started); elapsed >= 5*time.Second {
		t.Fatalf("bounded projection initialize took %s, want <5s", elapsed)
	}
	db, err = sql.Open("sqlite", sqliteDSN(path))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	var sequenceRows, requires int
	if err = db.QueryRow(`SELECT COUNT(*),(SELECT requires_inventory FROM search_projection_inventory_compat) FROM search_projection_source_sequence`).Scan(&sequenceRows, &requires); err != nil {
		t.Fatal(err)
	}
	if sequenceRows != 128 || requires != 1 {
		t.Fatalf("source sequence rows=%d requires_inventory=%d, want one catch-up batch of 128 with inventory still required", sequenceRows, requires)
	}
}

func TestSearchProjectionHistoricalInventoryIsBoundedCancelableAndFreshResumable(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "store.db")
	legacy := NewDatabase(path, migrationsBeforeSearchProjection(t))
	if err := legacy.initialize(ctx); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", sqliteDSN(path))
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"historical-a", "historical-b", "historical-c"} {
		if _, err = db.Exec(`INSERT INTO events(id,kind,agent,session_id,body,created_at,client,workspace) VALUES(?,'note','a','s','body','2026-08-03T00:00:00Z','c','w')`, id); err != nil {
			t.Fatal(err)
		}
	}
	_ = db.Close()
	all, _ := fs.Sub(os.DirFS("../.."), "schema/sqlite/migrations")
	store := NewDatabase(path, all)
	if err = store.initialize(ctx); err != nil {
		t.Fatal(err)
	}
	// Initialize may auto-start and even finish inventory for these three
	// events (#1680). Reset to a clean pre-inventory state so this test owns
	// the 1-row inventory budget and resume contract.
	resetProjectionForInventoryTest(t, path)
	b := projectionBudget()
	b.Rows = 1
	if _, err = store.Start(ctx, b, time.Now()); err != nil {
		t.Fatal(err)
	}
	snapshot, err := store.SelectInventory(ctx, b)
	if err != nil || len(snapshot.Items) != 1 || snapshot.Done {
		t.Fatalf("first inventory=%+v err=%v", snapshot, err)
	}
	plan := apptypes.SearchProjectionInventoryPlan{GenerationID: snapshot.Generation.GenerationID, ExpectedRevision: snapshot.Generation.SourceRevision, ExpectedCursor: snapshot.Cursor, ExpectedCursorStarted: snapshot.CursorStarted, NextCursor: snapshot.Items[0].EventID, NextCursorStarted: true, Items: snapshot.Items}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err = store.ApplyInventoryBatch(canceled, plan, b.LockTime, time.Now()); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled apply error=%T %v", err, err)
	}
	results := make(chan error, 2)
	for range 2 {
		go func() {
			_, applyErr := store.ApplyInventoryBatch(ctx, plan, b.LockTime, time.Now())
			results <- applyErr
		}()
	}
	winners, losers := 0, 0
	for range 2 {
		applyErr := <-results
		if applyErr == nil {
			winners++
			continue
		}
		var drift *apptypes.SearchProjectionDriftError
		if errors.As(applyErr, &drift) {
			losers++
			continue
		}
		t.Fatalf("concurrent inventory CAS leaked %T %v", applyErr, applyErr)
	}
	if winners != 1 || losers != 1 {
		t.Fatalf("concurrent inventory winner/loser=%d/%d", winners, losers)
	}
	next, err := store.SelectInventory(ctx, b)
	if err != nil {
		t.Fatal(err)
	}
	driftPlan := apptypes.SearchProjectionInventoryPlan{GenerationID: next.Generation.GenerationID, ExpectedRevision: next.Generation.SourceRevision, ExpectedCursor: next.Cursor, ExpectedCursorStarted: next.CursorStarted, NextCursor: next.Items[0].EventID, NextCursorStarted: true, Items: next.Items}
	mutationDB, err := sql.Open("sqlite", sqliteDSN(path))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = mutationDB.Exec(`UPDATE events SET body='changed' WHERE id='historical-a'`); err != nil {
		t.Fatal(err)
	}
	_ = mutationDB.Close()
	if _, err = store.ApplyInventoryBatch(ctx, driftPlan, b.LockTime, time.Now()); err == nil {
		t.Fatal("canonical mutation did not invalidate historical inventory")
	} else {
		var drift *apptypes.SearchProjectionDriftError
		if !errors.As(err, &drift) {
			t.Fatalf("mutation error=%T %v, want drift", err, err)
		}
	}
	var mainState, inventoryState string
	stateDB, err := sql.Open("sqlite", sqliteDSN(path))
	if err != nil {
		t.Fatal(err)
	}
	if err = stateDB.QueryRow(`SELECT s.state,i.state FROM search_projection_state s JOIN search_projection_inventory_state i ON i.singleton=s.singleton`).Scan(&mainState, &inventoryState); err != nil {
		t.Fatal(err)
	}
	_ = stateDB.Close()
	if mainState != "drifted" || inventoryState != "drifted" {
		t.Fatalf("persisted drift state=%q/%q", mainState, inventoryState)
	}
	if _, err = store.Start(ctx, b, time.Now()); err != nil {
		t.Fatalf("restart drifted inventory: %v", err)
	}

	// A new use case/store value resumes solely from the durable state.
	fresh := NewDatabase(path, all)
	workflow := usecase.NewSearchProjectionUsecase(fresh)
	totalWritten := 0
	for i := 0; i < 3; i++ {
		progress, resumeErr := workflow.Resume(ctx, b, time.Now())
		if resumeErr != nil || progress.Selected != 1 {
			t.Fatalf("inventory batch %d progress=%+v err=%v", i, progress, resumeErr)
		}
		totalWritten += progress.Written
	}
	status, err := fresh.SearchProjectionStatus(ctx)
	if err != nil || status.Phase != "source" || status.HighWater != 3 || totalWritten != 2 {
		t.Fatalf("post-inventory status=%+v err=%v", status, err)
	}
}

func TestSearchProjectionInventoryAdvancesAcrossRegisteredAndEmptyIdentities(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "store.db")
	legacy := NewDatabase(path, migrationsBeforeSearchProjection(t))
	if err := legacy.initialize(ctx); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", sqliteDSN(path))
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"", "already", "zmissing"} {
		if _, err = db.Exec(`INSERT INTO events(id,kind,agent,session_id,body,created_at,client,workspace) VALUES(?,'note','a','s','body','2026-08-03T00:00:00Z','c','w')`, id); err != nil {
			t.Fatal(err)
		}
	}
	_ = db.Close()
	all, _ := fs.Sub(os.DirFS("../.."), "schema/sqlite/migrations")
	store := NewDatabase(path, all)
	if err = store.initialize(ctx); err != nil {
		t.Fatal(err)
	}
	// Initialize may auto-start a catch-up generation and admit identities into
	// the source sequence. Clear that work so the test controls admission.
	if _, err = store.AbandonSearchProjection(ctx, time.Now()); err != nil {
		t.Fatal(err)
	}
	db, err = sql.Open("sqlite", sqliteDSN(path))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`DELETE FROM search_projection_source_sequence`); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`INSERT INTO search_projection_source_sequence(event_id) VALUES('already')`); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`UPDATE search_projection_state SET state='failed',phase='complete',generation_id=NULL,active_generation_id=NULL,config_hash='',checkpoint=0,high_water=0 WHERE singleton=1`); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`UPDATE search_projection_inventory_state SET generation_id='',cursor='',cursor_started=0,state='idle' WHERE singleton=1`); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`UPDATE search_projection_inventory_compat SET requires_inventory=1 WHERE singleton=1`); err != nil {
		t.Fatal(err)
	}
	_ = db.Close()
	b := projectionBudget()
	b.Rows = 1
	if _, err = store.Start(ctx, b, time.Now()); err != nil {
		t.Fatal(err)
	}
	workflow := usecase.NewSearchProjectionUsecase(store)
	first, err := workflow.Resume(ctx, b, time.Now())
	if err != nil || first.Selected != 1 || first.Written != 1 {
		t.Fatalf("empty identity batch=%+v err=%v", first, err)
	}
	snapshot, err := store.SelectInventory(ctx, b)
	if err != nil {
		t.Fatal(err)
	}
	if !snapshot.CursorStarted || snapshot.Cursor != "" || len(snapshot.Items) != 1 || snapshot.Items[0].EventID != "already" {
		t.Fatalf("empty cursor continuation=%+v", snapshot)
	}
	// The registered identity follows later lexically and is scanned in its own
	// bounded batch rather than causing an unbounded NOT EXISTS search.
	if snapshot.Items[0].Missing {
		t.Fatalf("missing identity classification=%+v", snapshot.Items[0])
	}
}

//nolint:wrapcheck // Test helper preserves the public usecase error.
func resumeProjection(ctx context.Context, store *Database, budget apptypes.SearchProjectionBudget, now time.Time) (apptypes.SearchProjectionProgress, error) {
	return usecase.NewSearchProjectionUsecase(store).Resume(ctx, budget, now)
}

func TestSearchProjectionApplyPathsTagLockDurationCap(t *testing.T) {
	tests := []struct {
		name  string
		apply func(*Database) error
	}{
		{
			name: "inventory batch",
			apply: func(store *Database) error {
				_, err := store.ApplyInventoryBatch(context.Background(), apptypes.SearchProjectionInventoryPlan{}, 0, time.Now())
				return err
			},
		},
		{
			name: "projection batch",
			apply: func(store *Database) error {
				_, err := store.ApplyBatch(context.Background(), apptypes.ProjectionBatchPlan{Phase: "source"}, 0, time.Now())
				return err
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			path := filepath.Join(t.TempDir(), "store.db")
			migrations, err := fs.Sub(os.DirFS("../.."), "schema/sqlite/migrations")
			if err != nil {
				t.Fatal(err)
			}
			store := NewDatabase(path, migrations)
			if err := store.initialize(ctx); err != nil {
				t.Fatal(err)
			}

			err = tt.apply(store)
			var noProgress *apptypes.SearchProjectionNoProgressError
			if !errors.As(err, &noProgress) {
				t.Fatalf("error=%T %v, want SearchProjectionNoProgressError", err, err)
			}
			if diff := cmp.Diff(apptypes.SearchProjectionNoProgressLockDurationCap, noProgress.Code); diff != "" {
				t.Errorf("no-progress code mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

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
	budget := apptypes.SearchProjectionBudget{Rows: 1, WallTime: time.Minute, LockTime: time.Second, StoredBytes: 1 << 20, DecodedBytes: 1 << 20, WriteBytes: 1 << 20, RecentAge: 90 * time.Minute, IndexFamilyBytes: 1 << 20}
	if _, err = store.Start(ctx, budget, now); err != nil {
		t.Fatal(err)
	}
	pinCeiling := func() {
		t.Helper()
		// IndexFamilyBytes is the physical index budget; eviction compares the
		// persisted source ceiling. Re-pin after each batch because the
		// source→eviction transition re-derives the column.
		if _, e := db.ExecContext(ctx, `UPDATE search_projection_state SET recent_source_ceiling_bytes=25 WHERE singleton=1`); e != nil {
			t.Fatal(e)
		}
	}
	pinCeiling()
	for i := 0; i < 3; i++ {
		got, e := resumeProjection(ctx, store, budget, now)
		if e != nil {
			t.Fatal(e)
		}
		if got.Selected != 1 {
			t.Fatalf("batch %d selected=%d", i, got.Selected)
		}
		pinCeiling()
	}
	var got apptypes.SearchProjectionProgress
	for i := 0; i < 10 && !got.Completed; i++ {
		got, err = resumeProjection(ctx, store, budget, now)
		if err != nil || got.WrittenBytes > budget.WriteBytes || got.Cleaned > budget.Rows {
			t.Fatalf("cleanup batch=%+v err=%v", got, err)
		}
		pinCeiling()
	}
	if !got.Completed {
		t.Fatalf("cleanup did not resume to completion: %+v", got)
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
	var fingerprintRows, distinctFingerprints int
	if err = db.QueryRow(`SELECT COUNT(*),COUNT(DISTINCT generation_id||':'||event_id||':'||hex(fingerprint)) FROM literal_search_fingerprints`).Scan(&fingerprintRows, &distinctFingerprints); err != nil {
		t.Fatal(err)
	}
	if fingerprintRows == 0 || fingerprintRows != distinctFingerprints {
		t.Fatalf("fingerprints rows/distinct=%d/%d", fingerprintRows, distinctFingerprints)
	}
	var literalState string
	if err = db.QueryRow(`SELECT state FROM literal_search_projection_state`).Scan(&literalState); err != nil || literalState != "complete" {
		t.Fatalf("literal state=%q err=%v", literalState, err)
	}
	status, statusErr := store.SearchProjectionStatus(ctx)
	if statusErr != nil {
		t.Fatal(statusErr)
	}
	if status.FingerprintVersion != 1 || status.FingerprintRows != int64(fingerprintRows) || status.FingerprintLogicalBytes <= 0 {
		t.Fatalf("fingerprint status=%+v", status)
	}
}

func TestSearchProjectionAbandonIsIdempotentAndRestartKeepsCanonicalHistory(t *testing.T) {
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
	if _, err = db.Exec(`INSERT INTO events(id,kind,agent,session_id,body,created_at,client,workspace) VALUES('canonical','note','agent','s','body','2026-08-03T00:00:00Z','client','repo')`); err != nil {
		t.Fatal(err)
	}
	b := projectionBudget()
	first, err := store.Start(ctx, b, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`INSERT INTO search_projection_exclusions(generation_id,source_sequence,event_id,class,measured_bytes,byte_limit) VALUES(?,?,?,?,?,?)`, first.GenerationID, 1, "canonical", "stored_bytes", 2, 1); err != nil {
		t.Fatal(err)
	}
	abandoned, err := store.AbandonSearchProjection(ctx, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	again, err := store.AbandonSearchProjection(ctx, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if abandoned.GenerationID != first.GenerationID || !again.AlreadyAbandoned {
		t.Fatalf("abandon=%+v again=%+v", abandoned, again)
	}
	var active sql.NullString
	var count int
	if err = db.QueryRow(`SELECT active_generation_id,(SELECT COUNT(*) FROM events) FROM search_projection_state`).Scan(&active, &count); err != nil {
		t.Fatal(err)
	}
	if active.Valid || count != 1 {
		t.Fatalf("active=%v canonical=%d", active, count)
	}
	var exclusions int
	if err = db.QueryRow(`SELECT COUNT(*) FROM search_projection_exclusions WHERE generation_id=?`, first.GenerationID).Scan(&exclusions); err != nil || exclusions != 0 {
		t.Fatalf("abandoned exclusions=%d err=%v, want 0", exclusions, err)
	}
	b.Rows = 2
	second, err := store.Start(ctx, b, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if second.GenerationID == first.GenerationID {
		t.Fatalf("restart did not create immutable generation: %+v %+v", first, second)
	}
}

func projectionBudget() apptypes.SearchProjectionBudget {
	return apptypes.SearchProjectionBudget{Rows: 1, WallTime: time.Minute, LockTime: time.Second, StoredBytes: 1 << 20, DecodedBytes: 1 << 20, WriteBytes: 1 << 20, RecentAge: time.Hour, IndexFamilyBytes: 1 << 20}
}

// resetProjectionForInventoryTest undoes auto catch-up so inventory-phase tests
// can Start with requires_inventory=1 and an empty source sequence.
func resetProjectionForInventoryTest(t *testing.T, path string) {
	t.Helper()
	db, err := sql.Open("sqlite", sqliteDSN(path))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	statements := []string{
		`DELETE FROM search_projection_source_sequence`,
		`DELETE FROM sqlite_sequence WHERE name='search_projection_source_sequence'`,
		`DELETE FROM search_projection_recent_documents`,
		`DELETE FROM search_projection_session_summaries`,
		`DELETE FROM search_projection_session_keywords`,
		`DELETE FROM search_projection_command_aggregates`,
		`DELETE FROM literal_search_fingerprints`,
		`DELETE FROM search_projection_generation_lifecycle`,
		`UPDATE search_projection_state SET generation_id=NULL,active_generation_id=NULL,config_hash='',source_revision=0,high_water=0,checkpoint=0,phase='source',cleanup_scope='old',failure_class='',state='idle' WHERE singleton=1`,
		`UPDATE search_projection_inventory_state SET generation_id='',cursor='',cursor_started=0,state='idle' WHERE singleton=1`,
		`UPDATE search_projection_inventory_compat SET requires_inventory=1 WHERE singleton=1`,
		`UPDATE literal_search_projection_state SET generation_id='',high_water=0,state='stale',updated_at=strftime('%Y-%m-%dT%H:%M:%fZ','now') WHERE singleton=1`,
	}
	for _, statement := range statements {
		if _, err := db.Exec(statement); err != nil {
			// sqlite_sequence is absent until the first AUTOINCREMENT insert.
			if strings.Contains(statement, "sqlite_sequence") && strings.Contains(err.Error(), "no such table") {
				continue
			}
			t.Fatalf("reset inventory fixture (%s): %v", statement, err)
		}
	}
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
	g, err := store.Start(ctx, b, now)
	if err != nil {
		t.Fatal(err)
	}
	insert("random-a")
	p, err := resumeProjection(ctx, store, b, now)
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
	_, err = resumeProjection(ctx, store, b, now)
	var drift *apptypes.SearchProjectionDriftError
	if !errors.As(err, &drift) {
		t.Fatalf("error=%T %v", err, err)
	}
	ng, err := store.Start(ctx, b, now)
	if err != nil || ng.GenerationID == g.GenerationID {
		t.Fatalf("new generation=%+v err=%v", ng, err)
	}
}

// TestSearchProjectionInventory_UpdateAndDeleteStillDrift pins the
// deliberately unchanged update/delete trigger behaviour during inventory:
// while requires_inventory=1 there is no reliable membership for historical
// rows the walk has not reached, so mutations still bump source_revision.
func TestSearchProjectionInventory_UpdateAndDeleteStillDrift(t *testing.T) {
	mutations := map[string]string{
		"event update": `UPDATE events SET body='mutated-during-inventory' WHERE id='historical-b'`,
		"event delete": `DELETE FROM events WHERE id='historical-c'`,
	}
	for name, mutation := range mutations {
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			path := filepath.Join(t.TempDir(), "store.db")
			legacy := NewDatabase(path, migrationsBeforeSearchProjection(t))
			if err := legacy.initialize(ctx); err != nil {
				t.Fatal(err)
			}
			db, err := sql.Open("sqlite", sqliteDSN(path))
			if err != nil {
				t.Fatal(err)
			}
			for _, id := range []string{"historical-a", "historical-b", "historical-c"} {
				if _, err = db.Exec(`INSERT INTO events(id,kind,agent,session_id,body,created_at,client,workspace) VALUES(?,'note','a','s','body','2026-08-03T00:00:00Z','c','w')`, id); err != nil {
					t.Fatal(err)
				}
			}
			_ = db.Close()

			all, err := fs.Sub(os.DirFS("../.."), "schema/sqlite/migrations")
			if err != nil {
				t.Fatal(err)
			}
			store := NewDatabase(path, all)
			if err = store.initialize(ctx); err != nil {
				t.Fatal(err)
			}
			resetProjectionForInventoryTest(t, path)

			b := projectionBudget()
			b.Rows = 1
			if _, err = store.Start(ctx, b, time.Now()); err != nil {
				t.Fatal(err)
			}
			// One inventory unit so the generation is live and still rebuilding.
			if _, err = resumeProjection(ctx, store, b, time.Now()); err != nil {
				t.Fatalf("inventory resume before mutation: %v", err)
			}

			db, err = sql.Open("sqlite", sqliteDSN(path))
			if err != nil {
				t.Fatal(err)
			}
			var beforeRevision int64
			if err = db.QueryRow(`SELECT revision FROM search_projection_source_revision WHERE singleton=1`).Scan(&beforeRevision); err != nil {
				t.Fatal(err)
			}
			if _, err = db.Exec(mutation); err != nil {
				t.Fatal(err)
			}
			var afterRevision int64
			var state string
			if err = db.QueryRow(`SELECT r.revision,s.state FROM search_projection_source_revision r, search_projection_state s WHERE r.singleton=1 AND s.singleton=1`).Scan(&afterRevision, &state); err != nil {
				t.Fatal(err)
			}
			_ = db.Close()
			if afterRevision <= beforeRevision {
				t.Fatalf("mutation %q did not bump source_revision: before=%d after=%d", name, beforeRevision, afterRevision)
			}

			_, err = resumeProjection(ctx, store, b, time.Now())
			var drift *apptypes.SearchProjectionDriftError
			if !errors.As(err, &drift) {
				t.Fatalf("resume after %q error=%T %v, want SearchProjectionDriftError", name, err, err)
			}
		})
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
	if _, err = store.Start(ctx, b, now); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 8; i++ {
		progress, resumeErr := resumeProjection(ctx, store, b, now)
		if resumeErr != nil {
			t.Fatal(resumeErr)
		}
		if progress.Completed {
			break
		}
	}
	for i := 0; i < 4; i++ {
		status, statusErr := store.SearchProjectionStatus(ctx)
		if statusErr != nil {
			t.Fatal(statusErr)
		}
		if status.Completed {
			break
		}
		if _, err = resumeProjection(ctx, store, b, now); err != nil {
			t.Fatal(err)
		}
	}
	var body string
	_ = db.QueryRow(`SELECT body_text FROM search_projection_recent_documents`).Scan(&body)
	if strings.Contains(body, "SECRET") {
		t.Fatal("unavailable body leaked")
	}
	b.StoredBytes = 1
	if _, err = store.Start(ctx, b, now); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 8; i++ {
		progress, resumeErr := resumeProjection(ctx, store, b, now)
		if resumeErr != nil {
			t.Fatal(resumeErr)
		}
		if progress.Completed {
			break
		}
	}
	status, err := store.SearchProjectionStatus(ctx)
	if err != nil || status.ExclusionCount != 1 || len(status.Exclusions) != 1 || status.Exclusions[0].EventID != "hidden" {
		t.Fatalf("status=%+v err=%v, want one hidden exclusion", status, err)
	}
	var fingerprints int
	if err = db.QueryRow(`SELECT COUNT(*) FROM literal_search_fingerprints WHERE event_id='hidden'`).Scan(&fingerprints); err != nil || fingerprints != 0 {
		t.Fatalf("excluded fingerprints=%d err=%v, want none", fingerprints, err)
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
	if _, err := store.Start(ctx, b, now); err != nil {
		t.Fatal(err)
	}
	p, err := resumeProjection(ctx, store, b, now)
	if err != nil || p.Completed {
		t.Fatalf("first=%+v %v", p, err)
	}
	if _, err = db.Exec(`VACUUM`); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 5 && !p.Completed; i++ {
		p, err = resumeProjection(ctx, store, b, now)
		if err != nil {
			t.Fatal(err)
		}
	}
	if !p.Completed {
		t.Fatalf("resume=%+v", p)
	}
	var bodies, summaries string
	if err = db.QueryRow(`SELECT group_concat(body_text), (SELECT group_concat(summary_text) FROM search_projection_session_summaries) FROM search_projection_recent_documents`).Scan(&bodies, &summaries); err != nil {
		t.Fatal(err)
	}
	// body_text is ASCII-folded for the case-sensitive trigram tokenizer, so
	// visibility is asserted case-insensitively and the MATCH uses the same
	// folded phrase the search path builds.
	visible := strings.ToLower(bodies + summaries)
	if strings.Contains(visible, "private-thought") || !strings.Contains(visible, "public-text") {
		t.Fatalf("visible projection=%q %q", bodies, summaries)
	}
	var matches int
	if err = db.QueryRow(`SELECT count(*) FROM search_projection_recent_fts WHERE search_projection_recent_fts MATCH ?`, eventSearchFTSPhrase("PUBLIC-TEXT")).Scan(&matches); err != nil || matches != 2 {
		t.Fatalf("matches=%d err=%v", matches, err)
	}
}

func TestSearchProjectionMutationBetweenSnapshotAndApplyCommitsOnlyDriftMarker(t *testing.T) {
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
	if _, err := db.Exec(`INSERT INTO events(id,kind,agent,session_id,body,created_at,client,workspace) VALUES('race','note','a','s','visible',?,'c','w')`, formatTimestamp(now)); err != nil {
		t.Fatal(err)
	}
	b := projectionBudget()
	if _, err := store.Start(ctx, b, now); err != nil {
		t.Fatal(err)
	}
	snapshot, err := store.SelectSnapshot(ctx, b, now)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := usecase.PlanProjectionBatch(snapshot, b)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`UPDATE events SET body='changed' WHERE id='race'`); err != nil {
		t.Fatal(err)
	}
	_, err = store.ApplyBatch(ctx, plan, b.LockTime, now)
	var drift *apptypes.SearchProjectionDriftError
	if !errors.As(err, &drift) {
		t.Fatalf("error=%T %v", err, err)
	}
	var checkpoint, rows int
	var phase string
	if err = db.QueryRow(`SELECT checkpoint,phase,(SELECT count(*) FROM search_projection_recent_documents) FROM search_projection_state`).Scan(&checkpoint, &phase, &rows); err != nil {
		t.Fatal(err)
	}
	if checkpoint != 0 || rows != 0 || phase != "cleanup" {
		t.Fatalf("checkpoint=%d rows=%d phase=%s", checkpoint, rows, phase)
	}
}

func TestConcurrentApplyBatchFencesSameCheckpoint(t *testing.T) {
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
	if _, err := db.Exec(`INSERT INTO events(id,kind,agent,session_id,body,created_at,client,workspace) VALUES('race','note','a','s','visible',?,'c','w')`, formatTimestamp(now)); err != nil {
		t.Fatal(err)
	}
	b := projectionBudget()
	b.LockTime = 3 * time.Second
	if _, err := store.Start(ctx, b, now); err != nil {
		t.Fatal(err)
	}
	snapshot, err := store.SelectSnapshot(ctx, b, now)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := usecase.PlanProjectionBatch(snapshot, b)
	if err != nil {
		t.Fatal(err)
	}
	start := make(chan struct{})
	errs := make(chan error, 2)
	var wg sync.WaitGroup
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			_, applyErr := store.ApplyBatch(ctx, plan, b.LockTime, now)
			errs <- applyErr
		}()
	}
	close(start)
	wg.Wait()
	close(errs)
	successes, losers := 0, 0
	for applyErr := range errs {
		if applyErr == nil {
			successes++
			continue
		}
		var drift *apptypes.SearchProjectionDriftError
		var stopped *apptypes.SearchProjectionNoProgressError
		if errors.As(applyErr, &drift) || errors.As(applyErr, &stopped) {
			losers++
			continue
		}
		t.Fatalf("untyped loser error: %T %v", applyErr, applyErr)
	}
	if successes != 1 || losers != 1 {
		t.Fatalf("successes=%d losers=%d", successes, losers)
	}
	var checkpoint, rows int
	if err = db.QueryRow(`SELECT checkpoint,(SELECT count(*) FROM search_projection_recent_documents) FROM search_projection_state`).Scan(&checkpoint, &rows); err != nil {
		t.Fatal(err)
	}
	if checkpoint != 1 || rows != 1 {
		t.Fatalf("checkpoint=%d rows=%d", checkpoint, rows)
	}
	if _, err = resumeProjection(ctx, store, b, now); err != nil {
		t.Fatalf("resume after fenced loser: %v", err)
	}
}

func TestSearchProjectionOversizeRowIsExcludedAndGenerationCompletes(t *testing.T) {
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
	if _, err := db.Exec(`INSERT INTO events(id,kind,agent,session_id,body,created_at,client,workspace) VALUES('large','note','a','s','payload-too-large',?,'c','w')`, formatTimestamp(now)); err != nil {
		t.Fatal(err)
	}
	b := projectionBudget()
	b.StoredBytes = 1
	if _, err := store.Start(ctx, b, now); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 4; i++ {
		progress, resumeErr := resumeProjection(ctx, store, b, now)
		if resumeErr != nil {
			t.Fatal(resumeErr)
		}
		if progress.Completed {
			break
		}
	}
	var err error
	var state string
	if err = db.QueryRow(`SELECT state FROM search_projection_state`).Scan(&state); err != nil || state != "complete" {
		t.Fatalf("state=%q err=%v", state, err)
	}
	var exclusions int
	if err = db.QueryRow(`SELECT COUNT(*) FROM search_projection_exclusions WHERE event_id='large'`).Scan(&exclusions); err != nil || exclusions != 1 {
		t.Fatalf("exclusions=%d err=%v", exclusions, err)
	}
}

func TestSearchProjectionWritePlusCheckpointOversizeIsExcluded(t *testing.T) {
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
	if _, err := db.Exec(`INSERT INTO events(id,kind,agent,session_id,body,created_at,client,workspace) VALUES('e','note','a','s','x',?,'c','w')`, formatTimestamp(now)); err != nil {
		t.Fatal(err)
	}
	b := projectionBudget()
	b.WriteBytes = 180
	if _, err := store.Start(ctx, b, now); err != nil {
		t.Fatal(err)
	}
	progress, err := resumeProjection(ctx, store, b, now)
	if err != nil || progress.Selected != 1 || progress.Written != 0 {
		t.Fatalf("progress=%+v err=%v, want one exclusion", progress, err)
	}
	var state string
	if err = db.QueryRow(`SELECT state FROM search_projection_state`).Scan(&state); err != nil || state == "failed" {
		t.Fatalf("state=%q err=%v", state, err)
	}
	var exclusions int
	if err = db.QueryRow(`SELECT COUNT(*) FROM search_projection_exclusions WHERE event_id='e' AND class='write_bytes'`).Scan(&exclusions); err != nil || exclusions != 1 {
		t.Fatalf("exclusions=%d err=%v", exclusions, err)
	}
}

func TestCompletedMutationDefersPayloadCleanupToBoundedPhase(t *testing.T) {
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
	for _, id := range []string{"one", "two"} {
		if _, err := db.Exec(`INSERT INTO events(id,kind,agent,session_id,body,created_at,client,workspace) VALUES(?,'note','a',?,'visible-text',?,'c','w')`, id, id, formatTimestamp(now)); err != nil {
			t.Fatal(err)
		}
	}
	b := projectionBudget()
	if _, err := store.Start(ctx, b, now); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 10; i++ {
		p, err := resumeProjection(ctx, store, b, now)
		if err != nil {
			t.Fatal(err)
		}
		if p.Completed {
			break
		}
	}
	var before int
	_ = db.QueryRow(`SELECT count(*) FROM search_projection_session_summaries`).Scan(&before)
	if _, err := db.Exec(`UPDATE events SET body='mutated' WHERE id='one'`); err != nil {
		t.Fatal(err)
	}
	var after int
	var phase string
	_ = db.QueryRow(`SELECT (SELECT count(*) FROM search_projection_session_summaries),phase FROM search_projection_state`).Scan(&after, &phase)
	if after != before || phase != "cleanup" {
		t.Fatalf("trigger deleted rows: before=%d after=%d phase=%s", before, after, phase)
	}
	p, err := resumeProjection(ctx, store, b, now)
	if err != nil {
		t.Fatal(err)
	}
	if p.Cleaned > b.Rows || p.WrittenBytes > b.WriteBytes {
		t.Fatalf("unbounded cleanup: %+v", p)
	}
	status, err := store.SearchProjectionStatus(ctx)
	if err != nil || status.MatchProbeMilliseconds < 0 {
		t.Fatalf("probe=%+v err=%v", status, err)
	}
}

func TestCompletedCanonicalMutationsDriftLifecycleAtomically(t *testing.T) {
	mutations := map[string]string{
		"event update": `UPDATE events SET body='changed' WHERE id='event-1'`,
		"event delete": `DELETE FROM events WHERE id='event-1'`,
		"audit insert": `INSERT INTO command_audits(event_id,command_text,input_text,output_text,exit_code,failed) VALUES('event-1','second','','',0,0)`,
		"audit update": `UPDATE command_audits SET command_text='changed' WHERE event_id='event-1'`,
		"audit delete": `DELETE FROM command_audits WHERE event_id='event-1'`,
	}
	for name, mutation := range mutations {
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			path := filepath.Join(t.TempDir(), "store.db")
			migrations, _ := fs.Sub(os.DirFS("../.."), "schema/sqlite/migrations")
			store := NewDatabase(path, migrations)
			if err := store.initialize(ctx); err != nil {
				t.Fatal(err)
			}
			db, err := sql.Open("sqlite", sqliteDSN(path))
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = db.Close() }()
			if _, err = db.Exec(`INSERT INTO events(id,kind,agent,session_id,body,created_at,client,workspace) VALUES('event-1','note','a','s','body','2026-08-03T00:00:00Z','c','w'); INSERT INTO command_audits(event_id,command_text,input_text,output_text,exit_code,failed) VALUES('event-1','first','','',0,0)`); err != nil {
				t.Fatal(err)
			}
			var highWater int64
			if err = db.QueryRow(`SELECT sequence FROM search_projection_source_sequence WHERE event_id='event-1'`).Scan(&highWater); err != nil {
				t.Fatal(err)
			}
			if name == "audit insert" {
				if _, err = db.Exec(`DELETE FROM command_audits WHERE event_id='event-1'`); err != nil {
					t.Fatal(err)
				}
			}
			if _, err = db.Exec(`UPDATE search_projection_state SET generation_id='complete-generation',active_generation_id='complete-generation',state='complete',phase='complete',high_water=? WHERE singleton=1; INSERT INTO search_projection_generation_lifecycle(generation_id,state,config_hash,source_revision,high_water) VALUES('complete-generation','complete','hash',0,?)`, highWater, highWater); err != nil {
				t.Fatal(err)
			}
			if _, err = db.Exec(mutation); err != nil {
				t.Fatal(err)
			}
			var state, lifecycle, active string
			if err = db.QueryRow(`SELECT s.state,l.state,COALESCE(s.active_generation_id,'') FROM search_projection_state s JOIN search_projection_generation_lifecycle l ON l.generation_id=s.generation_id WHERE s.singleton=1`).Scan(&state, &lifecycle, &active); err != nil {
				t.Fatal(err)
			}
			if state != "drifted" || lifecycle != "drifted" || active != "" {
				t.Fatalf("state=%q lifecycle=%q active=%q", state, lifecycle, active)
			}
		})
	}
}

func TestRecentEvictionDeletesOnlyStableMinimalOldestPrefix(t *testing.T) {
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
	// Reverse insertion proves the timestamp tie is resolved by event ID, not row ID.
	for _, id := range []string{"b", "a"} {
		if _, err := db.Exec(`INSERT INTO events(id,kind,agent,session_id,body,created_at,client,workspace) VALUES(?,'note','a',?,'0123456789',?,'c','w')`, id, id, formatTimestamp(now)); err != nil {
			t.Fatal(err)
		}
	}
	b := projectionBudget()
	b.Rows = 10
	b.WriteBytes = 1 << 20
	if _, err := store.Start(ctx, b, now); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 8; i++ {
		p, err := resumeProjection(ctx, store, b, now)
		if err != nil {
			t.Fatal(err)
		}
		// Eviction honours the persisted source ceiling. Re-pin after every
		// batch because the source→eviction transition re-derives the column
		// from the index-family budget (which is not the unit this test pins).
		if _, err := db.Exec(`UPDATE search_projection_state SET recent_source_ceiling_bytes=10 WHERE singleton=1`); err != nil {
			t.Fatal(err)
		}
		if p.Completed {
			break
		}
	}
	var ids string
	if err := db.QueryRow(`SELECT group_concat(event_id,',') FROM search_projection_recent_documents`).Scan(&ids); err != nil {
		t.Fatal(err)
	}
	if ids != "b" {
		t.Fatalf("retained IDs=%q, want newest stable tie b", ids)
	}
}

// The recent FTS is `trigram case_sensitive 1` and search folds the query to
// lower case before matching, so the projection must index folded text. If it
// stores raw text, every term containing an uppercase letter becomes
// unfindable through the projection while the legacy index still finds it.
func TestSearchProjectionRecentDocumentsAreASCIIFoldedForCaseSensitiveFTS(t *testing.T) {
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
	if _, err = db.ExecContext(ctx, `INSERT INTO events(id,kind,agent,session_id,body,created_at,client,workspace) VALUES('mixed','note','agent','s1',?,?,'client','repo')`,
		"Fixed the Timeout during Deploy", formatTimestamp(now),
	); err != nil {
		t.Fatal(err)
	}

	budget := apptypes.SearchProjectionBudget{
		Rows: 8, WallTime: time.Minute, LockTime: time.Second,
		StoredBytes: 1 << 20, DecodedBytes: 1 << 20, WriteBytes: 1 << 20,
		RecentAge: 90 * time.Minute, IndexFamilyBytes: 1 << 20,
	}
	if _, err = store.Start(ctx, budget, now); err != nil {
		t.Fatal(err)
	}
	var progress apptypes.SearchProjectionProgress
	for i := 0; i < 10 && !progress.Completed; i++ {
		progress, err = resumeProjection(ctx, store, budget, now)
		if err != nil {
			t.Fatal(err)
		}
	}
	if !progress.Completed {
		t.Fatalf("rebuild did not complete: %+v", progress)
	}

	var stored string
	if err = db.QueryRowContext(ctx, `SELECT body_text FROM search_projection_recent_documents WHERE event_id='mixed'`).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if diff := cmp.Diff("fixed the timeout during deploy", stored); diff != "" {
		t.Fatalf("stored body_text (-want +got):\n%s", diff)
	}

	// The query the search path actually issues for "Timeout".
	var matched int
	if err = db.QueryRowContext(ctx, `
		SELECT COUNT(*)
		  FROM search_projection_recent_fts
		  JOIN search_projection_recent_documents d
		    ON d.document_id = search_projection_recent_fts.rowid
		 WHERE search_projection_recent_fts MATCH ?`,
		eventSearchFTSPhrase("Timeout"),
	).Scan(&matched); err != nil {
		t.Fatal(err)
	}
	if matched != 1 {
		t.Fatalf("MATCH count = %d, want 1 (mixed-case text must be findable)", matched)
	}
}
