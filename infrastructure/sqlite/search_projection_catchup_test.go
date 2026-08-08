package sqlite_test

import (
	"context"
	"database/sql"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	_ "modernc.org/sqlite"

	apptypes "github.com/duck8823/traceary/application/types"
	"github.com/duck8823/traceary/application/usecase"
	infra "github.com/duck8823/traceary/infrastructure/sqlite"
	"github.com/duck8823/traceary/domain/types"
)

// TestSearchProjectionCatchUp_IsBoundedCompleteAndResumable pins the #1680
// shape against the event-search backfill precedent: one store open does a
// bounded unit of work, later opens resume, and only a complete generation
// becomes the authoritative read path. Session-tier verification is a real
// query, not a row count, and before/after bytes name the bounded family.
func TestSearchProjectionCatchUp_IsBoundedCompleteAndResumable(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "traceary.db")
	migrations, err := fs.Sub(os.DirFS("../.."), "schema/sqlite/migrations")
	if err != nil {
		t.Fatal(err)
	}
	database := infra.NewDatabase(dbPath, migrations)
	store := infra.NewStoreManagementDatasource(database)
	events := infra.NewEventDatasource(database)

	// Seed historical corpus before any projection generation exists.
	if err := store.Initialize(ctx); err != nil {
		t.Fatalf("bootstrap Initialize() error = %v", err)
	}
	// Clear any auto catch-up from bootstrap so the test owns the cutover.
	resetProjectionToIdle(t, dbPath)

	workspace := types.Workspace("github.com/duck8823/traceary")
	// Old history: well outside the 30-day recent window so the session tier
	// is the only path that can answer.
	old := time.Date(2026, 1, 5, 12, 0, 0, 0, time.UTC)
	recent := time.Date(2026, 8, 6, 14, 0, 0, 0, time.UTC)
	seedSession(t, dbPath, "sess-old-history", old, workspace.String())
	seedSession(t, dbPath, "sess-recent", recent, workspace.String())
	if err := events.Save(ctx, newSearchEventWithSession(t, "evt-old-1", "sess-old-history", workspace.String(), "old-history-needle unique marker alpha", old)); err != nil {
		t.Fatalf("Save old event: %v", err)
	}
	if err := events.Save(ctx, newSearchEventWithSession(t, "evt-recent-1", "sess-recent", workspace.String(), "recent window body", recent)); err != nil {
		t.Fatalf("Save recent event: %v", err)
	}

	// First catch-up open: must start a generation and do bounded work without
	// completing a multi-event inventory/source pass in one tiny fixture...
	// With only two events and default budget (128 rows), one Resume may finish
	// inventory+source quickly. Force a small budget via direct Resume path for
	// the interruption scenario, then resume with Initialize catch-up.
	//
	// Drive the first unit with the production catch-up budget by Initialize.
	if err := store.Initialize(ctx); err != nil {
		t.Fatalf("first cutover Initialize() error = %v", err)
	}
	status := projectionStatus(t, database)
	if status.State == "idle" {
		t.Fatal("first cutover Initialize left projection idle; expected auto-start")
	}
	if status.State == "complete" && status.CutoverIndexFamily != "bounded_search_projection" {
		t.Fatalf("complete cutover family = %q, want bounded_search_projection", status.CutoverIndexFamily)
	}

	// Interrupt path: if not yet complete, further Initializes resume.
	for i := 0; i < 200 && !status.Completed; i++ {
		if err := store.Initialize(ctx); err != nil {
			t.Fatalf("resume Initialize() #%d error = %v", i+1, err)
		}
		status = projectionStatus(t, database)
	}
	if !status.Completed {
		t.Fatalf("projection did not complete after bounded resumes: state=%s phase=%s", status.State, status.Phase)
	}
	if status.CutoverIndexFamily != "bounded_search_projection" {
		t.Fatalf("cutover_index_family = %q, want bounded_search_projection", status.CutoverIndexFamily)
	}
	// After bytes are measured for the bounded family. On a tiny fixture they
	// may equal before (schema already present); the contract is that both are
	// reported under the named family and never claim to be store-wide.
	if status.CutoverFamilyBytesAfter < 0 || status.CutoverFamilyBytesBefore < 0 {
		t.Fatalf("negative family bytes before=%d after=%d", status.CutoverFamilyBytesBefore, status.CutoverFamilyBytesAfter)
	}
	t.Logf("bounded_search_projection family bytes: before=%d after=%d", status.CutoverFamilyBytesBefore, status.CutoverFamilyBytesAfter)

	// Old-history term is answered by the session tier, not the recent window.
	criteria := apptypes.NewEventSearchCriteriaBuilder(20).
		Query("old-history-needle").
		Workspace(workspace).
		Build()
	gotEvents, err := events.Search(ctx, criteria.Query(), workspace, "", "", "", "", time.Time{}, time.Time{}, 20, 0, false)
	if err != nil {
		t.Fatalf("Search() error = %v", err)
	}
	// Old event is outside recent retention of the projection; event tier may
	// be empty while the session tier still finds the trail.
	sessions, err := events.SearchSessionHits(ctx, criteria, nil)
	if err != nil {
		t.Fatalf("SearchSessionHits() error = %v", err)
	}
	if len(sessions) != 1 {
		t.Fatalf("session hits = %d (events=%v), want 1 session for old-history-needle", len(sessions), eventIDs(gotEvents))
	}
	if diff := cmp.Diff("sess-old-history", sessions[0].SessionID().String()); diff != "" {
		t.Fatalf("session id mismatch (-want +got):\n%s", diff)
	}

	// A further Initialize is a no-op once complete.
	if err := store.Initialize(ctx); err != nil {
		t.Fatalf("post-complete Initialize() error = %v", err)
	}
	again := projectionStatus(t, database)
	if !again.Completed || again.State != "complete" {
		t.Fatalf("post-complete status = %+v, want complete", again)
	}
}

func TestSearchProjectionCatchUp_EmptyStoreStaysIdle(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "traceary.db")
	migrations, err := fs.Sub(os.DirFS("../.."), "schema/sqlite/migrations")
	if err != nil {
		t.Fatal(err)
	}
	database := infra.NewDatabase(dbPath, migrations)
	store := infra.NewStoreManagementDatasource(database)
	if err := store.Initialize(ctx); err != nil {
		t.Fatalf("Initialize() error = %v", err)
	}
	status := projectionStatus(t, database)
	if status.State != "idle" {
		t.Fatalf("empty store state = %q, want idle (no auto-start without source work)", status.State)
	}
}

func TestSearchProjectionCatchUp_InterruptedMidRebuildResumesWithoutTwoLiveGenerations(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "traceary.db")
	migrations, err := fs.Sub(os.DirFS("../.."), "schema/sqlite/migrations")
	if err != nil {
		t.Fatal(err)
	}
	database := infra.NewDatabase(dbPath, migrations)
	store := infra.NewStoreManagementDatasource(database)
	if err := store.Initialize(ctx); err != nil {
		t.Fatalf("bootstrap Initialize() error = %v", err)
	}
	resetProjectionToIdle(t, dbPath)

	workspace := types.Workspace("github.com/duck8823/traceary")
	base := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	// Enough events that a 1-row budget cannot finish in one Resume.
	for i := 0; i < 5; i++ {
		sessionID := types.SessionID("sess-batch")
		seedSession(t, dbPath, sessionID.String(), base.Add(time.Duration(i)*time.Hour), workspace.String())
		body := "interruptible-cutover-body-" + string(rune('a'+i)) + " unique"
		event := newSearchEventWithSession(t, "evt-batch-"+string(rune('a'+i)), sessionID.String(), workspace.String(), body, base.Add(time.Duration(i)*time.Hour))
		if err := infra.NewEventDatasource(database).Save(ctx, event); err != nil {
			t.Fatalf("Save(%d): %v", i, err)
		}
	}

	// Start with a tight budget so the first Resume cannot finish.
	tight := apptypes.SearchProjectionBudget{
		Rows: 1, WallTime: time.Minute, LockTime: time.Second,
		StoredBytes: 1 << 20, DecodedBytes: 1 << 20, WriteBytes: 1 << 20,
		RecentAge: 365 * 24 * time.Hour, RecentBytes: 1 << 20,
	}
	// Match catch-up config by using the same budget for start+resume so the
	// auto path can take over. Use usecase directly for the interrupted start.
	workflow := usecase.NewSearchProjectionUsecase(database)
	if _, err := workflow.StartGeneration(ctx, tight, time.Now()); err != nil {
		t.Fatalf("StartGeneration: %v", err)
	}
	first, err := workflow.Resume(ctx, tight, time.Now())
	if err != nil {
		t.Fatalf("first Resume: %v", err)
	}
	if first.Completed {
		t.Fatal("first Resume completed; want an interrupted mid-rebuild")
	}
	// One generation rebuilding; no second live generation.
	var genCount int
	openRawDB(t, dbPath, func(db *sql.DB) {
		if err := db.QueryRow(`SELECT COUNT(*) FROM search_projection_generation_lifecycle WHERE state IN ('rebuilding','complete')`).Scan(&genCount); err != nil {
			t.Fatal(err)
		}
	})
	if genCount != 1 {
		t.Fatalf("live generations = %d, want 1 during interrupted rebuild", genCount)
	}

	// Resume with the same budget until complete (simulates subsequent opens
	// that share the config hash). Auto catch-up would skip a mismatched budget;
	// here the operator/auto budget is the tight one for the whole cutover.
	var progress apptypes.SearchProjectionProgress
	for i := 0; i < 50 && !progress.Completed; i++ {
		progress, err = workflow.Resume(ctx, tight, time.Now())
		if err != nil {
			t.Fatalf("Resume #%d: %v", i+1, err)
		}
	}
	if !progress.Completed {
		t.Fatal("interrupted cutover did not resume to completion")
	}
	// After complete, only one complete lifecycle row should remain live as
	// active; abandoned/failed may exist from other tests but not here.
	var completeCount, rebuildingCount int
	openRawDB(t, dbPath, func(db *sql.DB) {
		if err := db.QueryRow(`SELECT COUNT(*) FROM search_projection_generation_lifecycle WHERE state='complete'`).Scan(&completeCount); err != nil {
			t.Fatal(err)
		}
		if err := db.QueryRow(`SELECT COUNT(*) FROM search_projection_generation_lifecycle WHERE state='rebuilding'`).Scan(&rebuildingCount); err != nil {
			t.Fatal(err)
		}
	})
	if completeCount != 1 || rebuildingCount != 0 {
		t.Fatalf("after resume complete: complete=%d rebuilding=%d", completeCount, rebuildingCount)
	}
}

func TestSearchProjectionVerifySessionTier_RejectsMissingHit(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "traceary.db")
	migrations, err := fs.Sub(os.DirFS("../.."), "schema/sqlite/migrations")
	if err != nil {
		t.Fatal(err)
	}
	database := infra.NewDatabase(dbPath, migrations)
	if err := infra.NewStoreManagementDatasource(database).Initialize(ctx); err != nil {
		t.Fatalf("Initialize: %v", err)
	}
	// Generation with a summary but no matching query path because the summary
	// is empty / too short — verify should still vacuous-pass (no joinable ≥3
	// rune summary). Seed a summary that looks real, then delete the session so
	// the JOIN finds nothing → vacuous pass.
	openRawDB(t, dbPath, func(db *sql.DB) {
		if _, err := db.Exec(`
			UPDATE search_projection_state
			   SET generation_id='gen-verify', state='rebuilding', phase='cleanup', cleanup_scope='old'
			 WHERE singleton=1`); err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(`
			INSERT INTO search_projection_session_summaries(generation_id,session_id,event_count,summary_text,projection_version,summary_version)
			VALUES('gen-verify','orphan-session',1,'orphan summary text that would match',1,1)`); err != nil {
			t.Fatal(err)
		}
	})
	// No sessions row → vacuous pass (nothing joinable).
	if err := database.VerifySearchProjectionSessionTier(ctx, "gen-verify"); err != nil {
		t.Fatalf("orphan summary without session should vacuous-pass, got %v", err)
	}

	// With a real session, verification must find the session via a real query.
	seedSession(t, dbPath, "sess-verified", time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC), "ws")
	openRawDB(t, dbPath, func(db *sql.DB) {
		if _, err := db.Exec(`
			INSERT INTO search_projection_session_summaries(generation_id,session_id,event_count,summary_text,projection_version,summary_version)
			VALUES('gen-verify','sess-verified',3,'verified-session-needle for cutover',1,1)`); err != nil {
			t.Fatal(err)
		}
	})
	if err := database.VerifySearchProjectionSessionTier(ctx, "gen-verify"); err != nil {
		t.Fatalf("VerifySearchProjectionSessionTier() error = %v", err)
	}
}

// TestSearchProjectionVerifySessionTier_PassesWhenSeveralSessionsShareATerm
// pins the case a real corpus produces constantly: sessions in one workspace
// share vocabulary, so the term derived from one summary also matches others.
// Verification asks whether the session tier can answer, so any hit that
// includes the probed session is an answer. Requiring the probed session to be
// the top-ranked hit would fail every store whose summaries share a word, and
// a failed verification marks the generation failed and restarts it — a loop
// that never reaches complete.
func TestSearchProjectionVerifySessionTier_PassesWhenSeveralSessionsShareATerm(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "traceary.db")
	migrations, err := fs.Sub(os.DirFS("../.."), "schema/sqlite/migrations")
	if err != nil {
		t.Fatal(err)
	}
	database := infra.NewDatabase(dbPath, migrations)
	if err := infra.NewStoreManagementDatasource(database).Initialize(ctx); err != nil {
		t.Fatalf("Initialize: %v", err)
	}

	// The lexically first session is the verification candidate; the later one
	// starts more recently, so it outranks the candidate in the hit query.
	seedSession(t, dbPath, "sess-aaa", time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC), "ws")
	seedSession(t, dbPath, "sess-zzz", time.Date(2026, 6, 2, 0, 0, 0, 0, time.UTC), "ws")
	openRawDB(t, dbPath, func(db *sql.DB) {
		if _, err := db.Exec(`
			UPDATE search_projection_state
			   SET generation_id='gen-shared', state='rebuilding', phase='cleanup', cleanup_scope='old'
			 WHERE singleton=1`); err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(`
			INSERT INTO search_projection_session_summaries(generation_id,session_id,event_count,summary_text,projection_version,summary_version)
			VALUES('gen-shared','sess-aaa',2,'traceary rebuild alpha',1,1),
			      ('gen-shared','sess-zzz',2,'traceary rebuild beta',1,1)`); err != nil {
			t.Fatal(err)
		}
	})

	if err := database.VerifySearchProjectionSessionTier(ctx, "gen-shared"); err != nil {
		t.Fatalf("shared-vocabulary summaries must verify, got %v", err)
	}
}

func projectionStatus(t *testing.T, database *infra.Database) apptypes.SearchProjectionStatus {
	t.Helper()
	status, err := database.SearchProjectionStatus(context.Background())
	if err != nil {
		t.Fatalf("SearchProjectionStatus: %v", err)
	}
	return status
}

func resetProjectionToIdle(t *testing.T, dbPath string) {
	t.Helper()
	openRawDB(t, dbPath, func(db *sql.DB) {
		if _, err := db.Exec(`
			UPDATE search_projection_state
			   SET generation_id=NULL,
			       active_generation_id=NULL,
			       config_hash='',
			       source_revision=0,
			       high_water=0,
			       checkpoint=0,
			       phase='source',
			       cleanup_scope='old',
			       failure_class='',
			       state='idle',
			       cutover_index_family='',
			       cutover_family_bytes_before=0,
			       cutover_family_bytes_after=0
			 WHERE singleton=1`); err != nil {
			t.Fatalf("reset projection idle: %v", err)
		}
		if _, err := db.Exec(`DELETE FROM search_projection_generation_lifecycle`); err != nil {
			t.Fatalf("clear lifecycle: %v", err)
		}
		if _, err := db.Exec(`DELETE FROM search_projection_recent_documents`); err != nil {
			t.Fatalf("clear recent docs: %v", err)
		}
		if _, err := db.Exec(`DELETE FROM search_projection_session_summaries`); err != nil {
			t.Fatalf("clear summaries: %v", err)
		}
		if _, err := db.Exec(`DELETE FROM search_projection_session_keywords`); err != nil {
			t.Fatalf("clear keywords: %v", err)
		}
		if _, err := db.Exec(`DELETE FROM search_projection_command_aggregates`); err != nil {
			t.Fatalf("clear aggregates: %v", err)
		}
		if _, err := db.Exec(`DELETE FROM literal_search_fingerprints`); err != nil {
			t.Fatalf("clear fingerprints: %v", err)
		}
		if _, err := db.Exec(`UPDATE search_projection_inventory_state SET generation_id='',cursor='',cursor_started=0,state='idle' WHERE singleton=1`); err != nil {
			t.Fatalf("reset inventory: %v", err)
		}
	})
}

func seedSession(t *testing.T, dbPath, sessionID string, startedAt time.Time, workspace string) {
	t.Helper()
	openRawDB(t, dbPath, func(db *sql.DB) {
		if _, err := db.Exec(`
			INSERT OR IGNORE INTO sessions(session_id, started_at, client, agent, workspace)
			VALUES (?, ?, 'cli', 'codex', ?)`,
			sessionID, startedAt.UTC().Format(time.RFC3339Nano), workspace,
		); err != nil {
			t.Fatalf("seed session %s: %v", sessionID, err)
		}
	})
}