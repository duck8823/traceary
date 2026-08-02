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
	if elapsed := time.Since(started); elapsed >= time.Second {
		t.Fatalf("schema-only projection migration took %s, want <1s", elapsed)
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
	if sequenceRows != 0 || requires != 1 {
		t.Fatalf("source sequence rows=%d requires_inventory=%d", sequenceRows, requires)
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
	b := projectionBudget()
	b.Rows = 1
	if _, err = store.Start(ctx, b, time.Now()); err != nil {
		t.Fatal(err)
	}
	snapshot, err := store.SelectInventory(ctx, b)
	if err != nil || len(snapshot.Items) != 1 || snapshot.Done {
		t.Fatalf("first inventory=%+v err=%v", snapshot, err)
	}
	plan := apptypes.SearchProjectionInventoryPlan{GenerationID: snapshot.Generation.GenerationID, ExpectedRevision: snapshot.Generation.SourceRevision, ExpectedCursor: snapshot.Cursor, NextCursor: snapshot.Items[0].EventID, Items: snapshot.Items}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err = store.ApplyInventoryBatch(canceled, plan, b.LockTime, time.Now()); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled apply error=%T %v", err, err)
	}
	mutationDB, err := sql.Open("sqlite", sqliteDSN(path))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = mutationDB.Exec(`UPDATE events SET body='changed' WHERE id='historical-a'`); err != nil {
		t.Fatal(err)
	}
	_ = mutationDB.Close()
	if _, err = usecase.NewSearchProjectionUsecase(store).Resume(ctx, b, time.Now()); err == nil {
		t.Fatal("canonical mutation did not invalidate historical inventory")
	} else {
		var drift *apptypes.SearchProjectionDriftError
		if !errors.As(err, &drift) {
			t.Fatalf("mutation error=%T %v, want drift", err, err)
		}
	}
	if _, err = store.Start(ctx, b, time.Now()); err != nil {
		t.Fatalf("restart drifted inventory: %v", err)
	}

	// A new use case/store value resumes solely from the durable state.
	fresh := NewDatabase(path, all)
	workflow := usecase.NewSearchProjectionUsecase(fresh)
	for i := 0; i < 3; i++ {
		progress, resumeErr := workflow.Resume(ctx, b, time.Now())
		if resumeErr != nil || progress.Written != 1 {
			t.Fatalf("inventory batch %d progress=%+v err=%v", i, progress, resumeErr)
		}
	}
	status, err := fresh.SearchProjectionStatus(ctx)
	if err != nil || status.Phase != "source" || status.HighWater != 3 {
		t.Fatalf("post-inventory status=%+v err=%v", status, err)
	}
}

//nolint:wrapcheck // Test helper preserves the public usecase error.
func resumeProjection(ctx context.Context, store *Database, budget apptypes.SearchProjectionBudget, now time.Time) (apptypes.SearchProjectionProgress, error) {
	return usecase.NewSearchProjectionUsecase(store).Resume(ctx, budget, now)
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
	budget := apptypes.SearchProjectionBudget{Rows: 1, WallTime: time.Minute, LockTime: time.Second, StoredBytes: 1 << 20, DecodedBytes: 1 << 20, WriteBytes: 1 << 20, RecentAge: 90 * time.Minute, RecentBytes: 25}
	if _, err = store.Start(ctx, budget, now); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		got, e := resumeProjection(ctx, store, budget, now)
		if e != nil {
			t.Fatal(e)
		}
		if got.Selected != 1 {
			t.Fatalf("batch %d selected=%d", i, got.Selected)
		}
	}
	var got apptypes.SearchProjectionProgress
	for i := 0; i < 10 && !got.Completed; i++ {
		got, err = resumeProjection(ctx, store, budget, now)
		if err != nil || got.WrittenBytes > budget.WriteBytes || got.Cleaned > budget.Rows {
			t.Fatalf("cleanup batch=%+v err=%v", got, err)
		}
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
	b.Rows = 2
	second, err := store.Start(ctx, b, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if second.GenerationID == first.GenerationID || second.ConfigHash == first.ConfigHash {
		t.Fatalf("restart did not create immutable generation: %+v %+v", first, second)
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
	if _, err = resumeProjection(ctx, store, b, now); err != nil {
		t.Fatal(err)
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
	_, err = resumeProjection(ctx, store, b, now)
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
	if strings.Contains(bodies+summaries, "PRIVATE-THOUGHT") || !strings.Contains(bodies+summaries, "PUBLIC-TEXT") {
		t.Fatalf("visible projection=%q %q", bodies, summaries)
	}
	var matches int
	if err = db.QueryRow(`SELECT count(*) FROM search_projection_recent_fts WHERE search_projection_recent_fts MATCH ?`, `"PUBLIC-TEXT"`).Scan(&matches); err != nil || matches != 2 {
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

func TestSearchProjectionOversizeFailureAllowsLargerGeneration(t *testing.T) {
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
	_, err := resumeProjection(ctx, store, b, now)
	var oversized *apptypes.SearchProjectionOversizeError
	if !errors.As(err, &oversized) {
		t.Fatalf("error=%T %v", err, err)
	}
	var state string
	if err = db.QueryRow(`SELECT state FROM search_projection_state`).Scan(&state); err != nil || state != "failed" {
		t.Fatalf("state=%q err=%v", state, err)
	}
	b.StoredBytes = 1 << 20
	if _, err = store.Start(ctx, b, now); err != nil {
		t.Fatalf("larger start: %v", err)
	}
}

func TestSearchProjectionWritePlusCheckpointOversizeIsRecoverable(t *testing.T) {
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
	b.WriteBytes = 250
	if _, err := store.Start(ctx, b, now); err != nil {
		t.Fatal(err)
	}
	_, err := resumeProjection(ctx, store, b, now)
	var oversized *apptypes.SearchProjectionOversizeError
	if !errors.As(err, &oversized) || oversized.Bytes <= b.WriteBytes {
		t.Fatalf("error=%T %+v", err, oversized)
	}
	var state string
	if err = db.QueryRow(`SELECT state FROM search_projection_state`).Scan(&state); err != nil || state != "failed" {
		t.Fatalf("state=%q err=%v", state, err)
	}
	b.WriteBytes = 1000
	if _, err = store.Start(ctx, b, now); err != nil {
		t.Fatalf("larger start: %v", err)
	}
	p, err := resumeProjection(ctx, store, b, now)
	if err != nil || p.Written != 1 {
		t.Fatalf("larger resume=%+v err=%v", p, err)
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
	b.RecentBytes = 10
	b.WriteBytes = 1 << 20
	if _, err := store.Start(ctx, b, now); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 8; i++ {
		p, err := resumeProjection(ctx, store, b, now)
		if err != nil {
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
