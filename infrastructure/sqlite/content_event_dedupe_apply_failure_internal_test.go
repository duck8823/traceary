package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"testing"
	"time"

	apptypes "github.com/duck8823/traceary/application/types"
	"github.com/duck8823/traceary/application/usecase"
)

// seedApplyFailureFixture builds a store with one proximity cluster of three
// near-simultaneous codex prompt duplicates (evt-a1 kept, evt-a2/evt-a3
// archivable) and one independent transcript pair (evt-c1 kept, evt-c2
// archivable), so a BatchSize:1 apply commits the atomic group A in its own
// (oversized) transaction before it ever reaches group C.
func seedApplyFailureFixture(t *testing.T) (string, *StoreManagementDatasource) {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "traceary", "traceary.db")
	database := NewDatabase(dbPath, preparedMigrations(t))
	storeManager := NewStoreManagementDatasource(database)
	if err := storeManager.Initialize(context.Background()); err != nil {
		t.Fatalf("Initialize() error = %v", err)
	}

	db, err := sql.Open("sqlite", directSQLiteRWDSN(dbPath))
	if err != nil {
		t.Fatalf("sql.Open() error = %v", err)
	}
	defer func() { _ = db.Close() }()

	type row struct{ id, kind, body, createdAt, sourceHook string }
	rows := []row{
		{"evt-a1", "prompt", "hello codex", "2026-04-10T00:00:00Z", "user_prompt_submit"},
		{"evt-a2", "prompt", "hello codex", "2026-04-10T00:00:03Z", "user_prompt_submit"},
		{"evt-a3", "prompt", "hello codex", "2026-04-10T00:00:05Z", "user_prompt_submit"},
		{"evt-c1", "transcript", "transcript body", "2026-04-10T00:00:00Z", "stop"},
		{"evt-c2", "transcript", "transcript body", "2026-04-10T00:00:01Z", "stop"},
	}
	for _, r := range rows {
		if _, err := db.Exec(
			`INSERT INTO events (id, kind, agent, session_id, workspace, body, created_at, source_hook, client)
			 VALUES (?, ?, 'codex', 's1', 'w1', ?, ?, ?, 'hook')`,
			r.id, r.kind, r.body, r.createdAt, r.sourceHook,
		); err != nil {
			t.Fatalf("insert %s error = %v", r.id, err)
		}
	}
	return dbPath, storeManager
}

func applyFailureArchivedIDs(t *testing.T, dbPath, runID string) []string {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+dbPath)
	if err != nil {
		t.Fatalf("sql.Open() error = %v", err)
	}
	defer func() { _ = db.Close() }()
	rows, err := db.Query(`SELECT id FROM event_content_dedupe_archive WHERE dedupe_run_id = ?`, runID)
	if err != nil {
		t.Fatalf("query archive error = %v", err)
	}
	defer func() { _ = rows.Close() }()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			t.Fatalf("scan archive row error = %v", err)
		}
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

func applyFailureEventExists(t *testing.T, dbPath, id string) bool {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+dbPath)
	if err != nil {
		t.Fatalf("sql.Open() error = %v", err)
	}
	defer func() { _ = db.Close() }()
	var exists int
	if err := db.QueryRow(`SELECT EXISTS(SELECT 1 FROM events WHERE id = ?)`, id).Scan(&exists); err != nil {
		t.Fatalf("check event %s error = %v", id, err)
	}
	return exists != 0
}

func equalStrings(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}

// A mid-apply failure must not drop the run id that the batches committed
// before the failure were archived under. This drives the full shipped call
// path: the usecase mints the run id and wraps the failure, the real
// datasource commits per batch, and a context cancellation between the first
// and second batch stands in for an interrupted apply.
func TestStoreManagementUsecase_DedupeContentEvents_MidApplyFailureRecoversByRunID(t *testing.T) {
	t.Parallel()
	dbPath, storeManager := seedApplyFailureFixture(t)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	storeManager.onAfterDedupeBatchCommit = func() { cancel() }

	uc := usecase.NewStoreManagementUsecase(storeManager)
	_, err := uc.DedupeContentEvents(ctx, apptypes.ContentEventDedupeParams{
		Agent: "codex", Apply: true, BatchSize: 1,
	})
	if err == nil {
		t.Fatalf("DedupeContentEvents() error = nil, want failure from the canceled context")
	}
	var applyErr *apptypes.ContentEventDedupeApplyError
	if !errors.As(err, &applyErr) {
		t.Fatalf("errors.As() = false, want *ContentEventDedupeApplyError; got %v", err)
	}
	if applyErr.RunID == "" {
		t.Fatalf("applyErr.RunID is empty")
	}

	// Group A (evt-a2, evt-a3) is the atomic cluster committed before the
	// cancellation fired; group C (evt-c2) never got its transaction.
	archived := applyFailureArchivedIDs(t, dbPath, applyErr.RunID)
	if want := []string{"evt-a2", "evt-a3"}; !equalStrings(archived, want) {
		t.Fatalf("archived under %q = %v, want %v", applyErr.RunID, archived, want)
	}
	if applyFailureEventExists(t, dbPath, "evt-a2") || applyFailureEventExists(t, dbPath, "evt-a3") {
		t.Fatalf("evt-a2/evt-a3 still present in events after being archived")
	}
	// Kept row and the unprocessed group survive untouched.
	for _, id := range []string{"evt-a1", "evt-c1", "evt-c2"} {
		if !applyFailureEventExists(t, dbPath, id) {
			t.Fatalf("event %s missing after mid-apply failure, want it untouched", id)
		}
	}

	// The failed run's id restores exactly the committed batch, verbatim.
	restore, err := uc.RestoreContentEventDedupeRun(context.Background(), applyErr.RunID)
	if err != nil {
		t.Fatalf("RestoreContentEventDedupeRun(%q) error = %v", applyErr.RunID, err)
	}
	if restore.RestoredCount != 2 {
		t.Fatalf("RestoredCount = %d, want 2", restore.RestoredCount)
	}
	if !applyFailureEventExists(t, dbPath, "evt-a2") || !applyFailureEventExists(t, dbPath, "evt-a3") {
		t.Fatalf("restore did not bring evt-a2/evt-a3 back into events")
	}
	if len(applyFailureArchivedIDs(t, dbPath, applyErr.RunID)) != 0 {
		t.Fatalf("archive still holds rows for %q after restore", applyErr.RunID)
	}
}

// A dry-run failure must never claim a run id: none was minted.
func TestStoreManagementUsecase_DedupeContentEvents_DryRunFailureCarriesNoRunID(t *testing.T) {
	t.Parallel()
	_, storeManager := seedApplyFailureFixture(t)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	uc := usecase.NewStoreManagementUsecase(storeManager)
	_, err := uc.DedupeContentEvents(ctx, apptypes.ContentEventDedupeParams{Agent: "codex", Apply: false})
	if err == nil {
		t.Fatalf("DedupeContentEvents() error = nil, want failure from the canceled context")
	}
	var applyErr *apptypes.ContentEventDedupeApplyError
	if errors.As(err, &applyErr) {
		t.Fatalf("dry-run failure claimed a run id: %#v", applyErr)
	}
}

func TestNewCompactCopyFilterRunID_UniquePerCall(t *testing.T) {
	t.Parallel()

	first, err := newCompactCopyFilterRunID()
	if err != nil {
		t.Fatalf("newCompactCopyFilterRunID() error = %v", err)
	}
	second, err := newCompactCopyFilterRunID()
	if err != nil {
		t.Fatalf("newCompactCopyFilterRunID() error = %v", err)
	}
	if first == second {
		t.Fatalf("two executions minted the same id: %q", first)
	}
	if first == "compact-copy-filter" || second == "compact-copy-filter" {
		t.Fatalf("id reused the old fixed literal: %q, %q", first, second)
	}
}

// The copy-filter's internal apply wraps a mid-apply failure with the same
// typed error the usecase leg uses, carrying the run id that the committed
// batch was archived under.
func TestDeleteNonCanonicalDuplicateEvents_WrapsFailureWithRunID(t *testing.T) {
	t.Parallel()
	dbPath := filepath.Join(t.TempDir(), "traceary", "traceary.db")
	database := NewDatabase(dbPath, preparedMigrations(t))
	storeManager := NewStoreManagementDatasource(database)
	if err := storeManager.Initialize(context.Background()); err != nil {
		t.Fatalf("Initialize() error = %v", err)
	}

	db, err := sql.Open("sqlite", directSQLiteRWDSN(dbPath))
	if err != nil {
		t.Fatalf("sql.Open() error = %v", err)
	}
	defer func() { _ = db.Close() }()

	// Two proximity clusters in distinct sessions, sized so partitioning at
	// the default batch size (1000, apptypes.DefaultContentEventDedupeBatchSize)
	// splits them into two transactions: cluster "s1" alone (1000 duplicates)
	// fills the first batch exactly, so adding cluster "s2" (1 duplicate)
	// overflows it and forces a flush. That gives the hook a real second
	// BeginTx to fail on when it cancels the context after the first batch
	// commits — the same wrap path a mid-batch failure exercises elsewhere,
	// now that compact-copy-filter no longer runs a DROP TABLE after a
	// successful apply (compact preserves event_content_dedupe_archive).
	base := time.Date(2026, 4, 10, 0, 0, 0, 0, time.UTC)
	tx, err := db.Begin()
	if err != nil {
		t.Fatalf("db.Begin() error = %v", err)
	}
	stmt, err := tx.Prepare(
		`INSERT INTO events (id, kind, agent, session_id, workspace, body, created_at, source_hook, client)
		 VALUES (?, 'prompt', 'codex', ?, 'w1', 'hi', ?, 'user_prompt_submit', 'hook')`)
	if err != nil {
		t.Fatalf("tx.Prepare() error = %v", err)
	}
	for i := 0; i <= 1000; i++ {
		createdAt := base.Add(time.Duration(i) * time.Second).Format(time.RFC3339)
		if _, err := stmt.Exec(fmt.Sprintf("evt-s1-%d", i), "s1", createdAt); err != nil {
			t.Fatalf("insert evt-s1-%d error = %v", i, err)
		}
	}
	for i := 0; i <= 1; i++ {
		createdAt := base.Add(time.Duration(i) * time.Second).Format(time.RFC3339)
		if _, err := stmt.Exec(fmt.Sprintf("evt-s2-%d", i), "s2", createdAt); err != nil {
			t.Fatalf("insert evt-s2-%d error = %v", i, err)
		}
	}
	if err := stmt.Close(); err != nil {
		t.Fatalf("stmt.Close() error = %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("tx.Commit() error = %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Identification runs to completion (it is read-only, ahead of the
	// cancellation). The first batch (cluster "s1", 1000 duplicates) commits
	// and the hook cancels the context; the second batch (cluster "s2", 1
	// duplicate) then fails to begin its transaction, exercising the wrap
	// path.
	datasource := &StoreManagementDatasource{onAfterDedupeBatchCommit: func() { cancel() }}
	err = deleteNonCanonicalDuplicateEvents(ctx, db, datasource)
	if err == nil {
		t.Fatalf("deleteNonCanonicalDuplicateEvents() error = nil, want failure from the canceled context")
	}
	var applyErr *apptypes.ContentEventDedupeApplyError
	if !errors.As(err, &applyErr) {
		t.Fatalf("errors.As() = false, want *ContentEventDedupeApplyError; got %v", err)
	}
	if applyErr.RunID == "" {
		t.Fatalf("applyErr.RunID is empty")
	}
}
