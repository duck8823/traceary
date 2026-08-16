package sqlite

import (
	"context"
	"log/slog"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	apptypes "github.com/duck8823/traceary/application/types"
	"github.com/duck8823/traceary/application/usecase"
)

func TestSearchProjectionCleanup_PagedDiscoveryMakesProgressUnderTinyWall(t *testing.T) {
	store, db := newCapacityTestStore(t, nil)
	now := time.Date(2026, 6, 2, 0, 0, 0, 0, time.UTC)
	ctx := context.Background()
	generation, err := store.Start(ctx, defaultSearchProjectionCatchUpBudget(), now)
	if err != nil {
		t.Fatal(err)
	}
	const leftoverCount = 12000
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < leftoverCount; i++ {
		if _, err = tx.Exec(
			`INSERT INTO search_projection_session_keywords(generation_id,session_id,keyword,occurrences,keyword_version) VALUES(?,?,?,1,1)`,
			"old-generation", "sess-old", "leftover-"+strconv.Itoa(i),
		); err != nil {
			t.Fatal(err)
		}
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`UPDATE search_projection_state SET phase='cleanup',checkpoint=0,cleanup_scope='old' WHERE generation_id=?`, generation.GenerationID); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`UPDATE search_projection_inventory_state SET state='complete' WHERE singleton=1`); err != nil {
		t.Fatal(err)
	}

	budget := defaultSearchProjectionCatchUpBudget()
	budget.WallTime = 50 * time.Millisecond
	budget.Rows = 8
	selectCtx, selectCancel := context.WithTimeout(ctx, budget.WallTime)
	defer selectCancel()
	snapshot, err := store.SelectSnapshot(selectCtx, budget, now)
	if err != nil {
		t.Fatalf("SelectSnapshot: %v", err)
	}
	if len(snapshot.Cleanup) == 0 {
		t.Fatal("paged cleanup selected no leftovers")
	}
	if snapshot.CleanupDone {
		t.Fatal("CleanupDone=true with thousands of leftovers still present")
	}
	plan, err := usecase.PlanProjectionBatch(snapshot, budget)
	if err != nil {
		t.Fatal(err)
	}
	applyCtx, applyCancel := context.WithTimeout(ctx, budget.WallTime)
	defer applyCancel()
	progress, err := store.CleanupBatch(applyCtx, plan, budget.LockTime, now)
	if err != nil {
		t.Fatalf("CleanupBatch: %v", err)
	}
	if progress.Cleaned == 0 {
		t.Fatal("cleanup committed no rows under the tiny wall budget")
	}

	var remaining int
	if err = db.QueryRow(`SELECT COUNT(*) FROM search_projection_session_keywords WHERE generation_id='old-generation'`).Scan(&remaining); err != nil {
		t.Fatal(err)
	}
	if remaining != leftoverCount-progress.Cleaned {
		t.Fatalf("remaining leftovers=%d, want %d", remaining, leftoverCount-progress.Cleaned)
	}

	catchBudget := budget
	catchBudget.WallTime = 50 * time.Millisecond
	result, catchErr := usecase.NewSearchProjectionUsecase(store).CatchUp(ctx, catchBudget, now)
	if catchErr != nil {
		t.Fatalf("CatchUp: %v", catchErr)
	}
	if result.Action != "resume" {
		t.Fatalf("CatchUp action=%q, want resume", result.Action)
	}
	status, err := store.SearchProjectionControlStatus(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if status.State != "rebuilding" || status.Phase != "cleanup" {
		t.Fatalf("after CatchUp state=%s phase=%s, want rebuilding/cleanup", status.State, status.Phase)
	}
	var afterCatchUp int
	if err = db.QueryRow(`SELECT COUNT(*) FROM search_projection_session_keywords WHERE generation_id='old-generation'`).Scan(&afterCatchUp); err != nil {
		t.Fatal(err)
	}
	if afterCatchUp >= remaining {
		t.Fatalf("CatchUp leftovers=%d, want fewer than %d", afterCatchUp, remaining)
	}
}

func TestSearchProjectionCleanup_ParksAfterConsecutiveZeroProgressAttempts(t *testing.T) {
	store, db := newCapacityTestStore(t, nil)
	now := time.Date(2026, 6, 2, 0, 0, 0, 0, time.UTC)
	ctx := context.Background()
	generation, err := store.Start(ctx, defaultSearchProjectionCatchUpBudget(), now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`
		INSERT INTO search_projection_session_keywords(generation_id,session_id,keyword,occurrences,keyword_version)
		VALUES('old-generation','sess-old','stale',1,1);
		UPDATE search_projection_state SET phase='cleanup',checkpoint=0,cleanup_scope='old' WHERE generation_id=?;
		UPDATE search_projection_inventory_state SET state='complete' WHERE singleton=1;
	`, generation.GenerationID); err != nil {
		t.Fatal(err)
	}

	for injected := 1; injected < apptypes.SearchProjectionCleanupNoProgressLimit; injected++ {
		attempts, parked, recErr := store.RecordCleanupNoProgressAttempt(ctx, generation.GenerationID, now)
		if recErr != nil {
			t.Fatalf("inject attempt %d: %v", injected, recErr)
		}
		if parked {
			t.Fatalf("inject attempt %d parked early (attempts=%d)", injected, attempts)
		}
	}
	store.afterProjectionLockHeld = func(holdCtx context.Context) error {
		select {
		case <-holdCtx.Done():
			return holdCtx.Err()
		case <-time.After(80 * time.Millisecond):
			return context.DeadlineExceeded
		}
	}
	zeroBudget := defaultSearchProjectionCatchUpBudget()
	zeroBudget.WallTime = 20 * time.Millisecond
	result, catchErr := usecase.NewSearchProjectionUsecase(store).CatchUp(ctx, zeroBudget, now)
	if catchErr != nil {
		t.Fatalf("parking CatchUp: %v", catchErr)
	}
	if result.Action != "skipped" {
		t.Fatalf("parking action=%q, want skipped", result.Action)
	}
	if !strings.Contains(result.SkippedReason, apptypes.SearchProjectionFailureCleanupNoProgress) {
		t.Fatalf("park reason %q does not name %s", result.SkippedReason, apptypes.SearchProjectionFailureCleanupNoProgress)
	}

	var state, class string
	if err = db.QueryRow(`SELECT state,failure_class FROM search_projection_state WHERE singleton=1`).Scan(&state, &class); err != nil {
		t.Fatal(err)
	}
	if state != "failed" || class != apptypes.SearchProjectionFailureCleanupNoProgress {
		t.Fatalf("state=%q class=%q, want failed/%s", state, class, apptypes.SearchProjectionFailureCleanupNoProgress)
	}

	next, nextErr := catchUpSearchProjection(ctx, store)
	if nextErr != nil {
		t.Fatalf("catchUpSearchProjection after park: %v", nextErr)
	}
	if next.Action != "skipped" {
		t.Fatalf("after park action=%q, want skipped", next.Action)
	}
	if !strings.Contains(next.SkippedReason, apptypes.SearchProjectionFailureCleanupNoProgress) {
		t.Fatalf("after park reason %q does not name parked cleanup_no_progress", next.SkippedReason)
	}
}

func TestSearchProjectionCleanup_ProgressResetsNoProgressAttempts(t *testing.T) {
	store, db := newCapacityTestStore(t, nil)
	now := time.Date(2026, 6, 2, 0, 0, 0, 0, time.UTC)
	ctx := context.Background()
	generation, err := store.Start(ctx, defaultSearchProjectionCatchUpBudget(), now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`
		INSERT INTO search_projection_session_keywords(generation_id,session_id,keyword,occurrences,keyword_version)
		VALUES('old-generation','sess-old','stale',1,1);
		UPDATE search_projection_state SET phase='cleanup',checkpoint=0,cleanup_scope='old',cleanup_no_progress_attempts=2 WHERE generation_id=?;
	`, generation.GenerationID); err != nil {
		t.Fatal(err)
	}

	snapshot, err := store.SelectSnapshot(ctx, defaultSearchProjectionCatchUpBudget(), now)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := usecase.PlanProjectionBatch(snapshot, defaultSearchProjectionCatchUpBudget())
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.CleanupBatch(ctx, plan, time.Second, now); err != nil {
		t.Fatal(err)
	}
	var attempts int
	if err = db.QueryRow(`SELECT cleanup_no_progress_attempts FROM search_projection_state WHERE singleton=1`).Scan(&attempts); err != nil {
		t.Fatal(err)
	}
	if attempts != 0 {
		t.Fatalf("attempts=%d, want 0 after a cleanup that made progress", attempts)
	}
}

func TestLogSearchProjectionCatchUp_RepeatIncompleteIsDebug(t *testing.T) {
	handler := &catchUpLogHandler{}
	previous := slog.Default()
	slog.SetDefault(slog.New(handler))
	t.Cleanup(func() { slog.SetDefault(previous) })

	logSearchProjectionCatchUp(apptypes.SearchProjectionCatchUpResult{
		Action: "resume", State: "rebuilding", Phase: "cleanup",
	}, context.DeadlineExceeded)

	if diff := cmp.Diff(1, len(handler.records)); diff != "" {
		t.Fatalf("unexpected record count (-want +got):\n%s", diff)
	}
	if handler.records[0].Message != genericCatchUpIncompleteLine {
		t.Fatalf("message=%q, want the generic incomplete line", handler.records[0].Message)
	}
	if handler.records[0].Level != slog.LevelDebug {
		t.Fatalf("level=%s, want DEBUG", handler.records[0].Level)
	}
}
