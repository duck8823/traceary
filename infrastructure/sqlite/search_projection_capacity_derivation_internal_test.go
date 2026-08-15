package sqlite

import (
	"context"
	"database/sql"
	"strings"
	"testing"
	"time"

	apptypes "github.com/duck8823/traceary/application/types"
)

func driveToEviction(t *testing.T, store *Database, db *sql.DB, b apptypes.SearchProjectionBudget, now time.Time) {
	t.Helper()
	ctx := context.Background()
	if _, err := store.Start(ctx, b, now); err != nil {
		t.Fatal(err)
	}
	var phase string
	for i := 0; i < 80; i++ {
		p, err := resumeProjection(ctx, store, b, now)
		if err != nil {
			t.Fatalf("resume %d: %v", i, err)
		}
		if err = db.QueryRow(`SELECT phase FROM search_projection_state WHERE singleton=1`).Scan(&phase); err != nil {
			t.Fatal(err)
		}
		if phase != "source" {
			return
		}
		if p.Completed {
			t.Fatal("completed while still expecting eviction")
		}
	}
	t.Fatal("did not leave source phase")
}

// TestSearchProjectionCapacityRederiveRetriesAfterMissedTransition is red if
// re-derivation stays a one-shot source→eviction hook: after the phase is
// already eviction, a crashed write (capacity_rederived=0 + pinned ceiling)
// would never be retried.
func TestSearchProjectionCapacityRederiveRetriesAfterMissedTransition(t *testing.T) {
	store, db := newCapacityTestStore(t, []struct{ id, body, created string }{
		{"e1", strings.Repeat("retry rederive corpus word ", 80), "2026-06-01T12:00:00Z"},
		{"e2", strings.Repeat("retry rederive second word ", 80), "2026-06-02T12:00:00Z"},
	})
	now := time.Date(2026, 6, 3, 0, 0, 0, 0, time.UTC)
	b := capacityBudget(64 << 20)
	driveToEviction(t, store, db, b, now)

	ctx := context.Background()
	if _, err := db.ExecContext(ctx, `UPDATE search_projection_state SET capacity_rederived=0, recent_source_ceiling_bytes=999999 WHERE singleton=1`); err != nil {
		t.Fatal(err)
	}
	if _, err := resumeProjection(ctx, store, b, now); err != nil {
		t.Fatal(err)
	}
	var ceiling int64
	var rederived int
	var phase string
	if err := db.QueryRow(`SELECT recent_source_ceiling_bytes,capacity_rederived,phase FROM search_projection_state`).Scan(&ceiling, &rederived, &phase); err != nil {
		t.Fatal(err)
	}
	if phase == "source" {
		t.Fatal("returned to source after eviction resume")
	}
	if ceiling == 999999 {
		t.Fatal("ceiling still Start-time pin; missed re-derivation was not retried")
	}
	if rederived != 1 {
		t.Fatalf("capacity_rederived=%d, want 1", rederived)
	}
}

// TestSearchProjectionCapacityRederiveRetriesAfterCleanupTransition is red
// if re-derivation is only invoked from eviction-phase applies: a crash
// after the eviction→cleanup commit leaves capacity_rederived=0 and
// later cleanup batches would never retry.
func TestSearchProjectionCapacityRederiveRetriesAfterCleanupTransition(t *testing.T) {
	store, db := newCapacityTestStore(t, []struct{ id, body, created string }{
		{"e1", strings.Repeat("cleanup retry corpus word ", 80), "2026-06-01T12:00:00Z"},
	})
	now := time.Date(2026, 6, 3, 0, 0, 0, 0, time.UTC)
	b := capacityBudget(64 << 20)
	driveToEviction(t, store, db, b, now)
	ctx := context.Background()
	if _, err := db.ExecContext(ctx, `UPDATE search_projection_state SET phase='cleanup', capacity_rederived=0, recent_source_ceiling_bytes=999999 WHERE singleton=1`); err != nil {
		t.Fatal(err)
	}
	if _, err := resumeProjection(ctx, store, b, now); err != nil {
		t.Fatal(err)
	}
	var ceiling int64
	var rederived int
	if err := db.QueryRow(`SELECT recent_source_ceiling_bytes,capacity_rederived FROM search_projection_state`).Scan(&ceiling, &rederived); err != nil {
		t.Fatal(err)
	}
	if ceiling == 999999 {
		t.Fatal("ceiling still Start-time pin after cleanup-phase retry")
	}
	if rederived != 1 {
		t.Fatalf("capacity_rederived=%d, want 1", rederived)
	}
}

// TestSearchProjectionCapacityDerivationUsesConsistentSnapshot is red if
// dbstat and SUM(decoded_bytes) are separate statements: a concurrent delete
// between them inflates PPM above the consistent snapshot value.
func TestSearchProjectionCapacityDerivationUsesConsistentSnapshot(t *testing.T) {
	events := []struct{ id, body, created string }{
		{"keep", strings.Repeat("snapshot keep token ", 400), "2026-06-01T12:00:00Z"},
		{"drop", strings.Repeat("snapshot drop token ", 400), "2026-06-02T12:00:00Z"},
	}
	now := time.Date(2026, 6, 3, 0, 0, 0, 0, time.UTC)
	ctx := context.Background()
	prepare := func(t *testing.T) (*Database, *sql.DB) {
		t.Helper()
		store, db := newCapacityTestStore(t, events)
		driveToCompletion(t, store, capacityBudget(64<<20), now)
		var dropLeft int
		if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM search_projection_recent_documents WHERE event_id='drop'`).Scan(&dropLeft); err != nil {
			t.Fatal(err)
		}
		if dropLeft != 1 {
			t.Fatalf("drop document count=%d, want 1 after completion", dropLeft)
		}
		// Force the measured-PPM path: the live SUM is far below 8 MiB on this
		// fixture, so raise decoded_bytes without changing physical pages.
		if _, err := db.ExecContext(ctx, `UPDATE search_projection_recent_documents SET decoded_bytes=9000000`); err != nil {
			t.Fatal(err)
		}
		return store, db
	}

	t.Cleanup(func() {
		searchProjectionAfterFamilySplitForTest = nil
		searchProjectionSkipCapacitySnapshotForTest = false
	})

	storeSnap, dbSnap := prepare(t)
	searchProjectionSkipCapacitySnapshotForTest = false
	searchProjectionAfterFamilySplitForTest = func() {
		if _, err := dbSnap.ExecContext(ctx, `DELETE FROM search_projection_recent_documents WHERE event_id='drop'`); err != nil {
			t.Errorf("hook delete: %v", err)
		}
	}
	consistent := storeSnap.deriveSearchProjectionCapacity(ctx, dbSnap, 64<<20, 0, "")
	if consistent.Evidence.Status != searchProjectionEvidenceMeasured {
		t.Fatalf("snapshot derivation evidence=%+v", consistent.Evidence)
	}

	storeRacy, dbRacy := prepare(t)
	searchProjectionSkipCapacitySnapshotForTest = true
	searchProjectionAfterFamilySplitForTest = func() {
		if _, err := dbRacy.ExecContext(ctx, `DELETE FROM search_projection_recent_documents WHERE event_id='drop'`); err != nil {
			t.Errorf("hook delete: %v", err)
		}
	}
	racy := storeRacy.deriveSearchProjectionCapacity(ctx, dbRacy, 64<<20, 0, "")
	if racy.Evidence.Status != searchProjectionEvidenceMeasured {
		t.Fatalf("racy derivation evidence=%+v", racy.Evidence)
	}
	// PPM is floored at 1× on this fixture (physical pages ≪ the forced
	// decoded_bytes). The snapshot contract is the SUM: a concurrent delete
	// must not change the snapshot sample, and must shrink the unsynchronized
	// one. Removing the read-only transaction makes both samples match (red).
	if consistent.SampleSourceBytes < 18_000_000 {
		t.Fatalf("snapshot sample=%d, want both 9MiB rows", consistent.SampleSourceBytes)
	}
	if racy.SampleSourceBytes >= consistent.SampleSourceBytes {
		t.Fatalf("racy sample %d >= snapshot sample %d; hook delete was not visible to the unsynchronized SUM",
			racy.SampleSourceBytes, consistent.SampleSourceBytes)
	}
	if racy.SampleSourceBytes != 9_000_000 {
		t.Fatalf("racy sample=%d, want the remaining 9MiB row", racy.SampleSourceBytes)
	}
}

// TestSearchProjectionCapacityRatchetShrinksAfterFTSReclaim records the
// #1751 item 3 ratchet on a scratch fixture: unmerged FTS deletes inflate
// PPM, and reclaim brings it back down. Generation-scoped SUM is not claimed.
func TestSearchProjectionCapacityRatchetShrinksAfterFTSReclaim(t *testing.T) {
	store, db := newCapacityTestStore(t, []struct{ id, body, created string }{
		{"a", strings.Repeat("ratchet unique alpha ", 300), "2026-06-01T12:00:00Z"},
		{"b", strings.Repeat("ratchet unique bravo ", 300), "2026-06-02T12:00:00Z"},
		{"c", strings.Repeat("ratchet unique charlie ", 300), "2026-06-03T12:00:00Z"},
	})
	now := time.Date(2026, 6, 4, 0, 0, 0, 0, time.UTC)
	driveToCompletion(t, store, capacityBudget(64<<20), now)
	ctx := context.Background()
	if _, err := db.ExecContext(ctx, `UPDATE search_projection_recent_documents SET decoded_bytes=4000000`); err != nil {
		t.Fatal(err)
	}
	before := store.deriveSearchProjectionCapacity(ctx, db, 64<<20, 0, "")
	if before.RecentBytes <= 0 || before.SampleSourceBytes <= 0 {
		t.Fatalf("before split missing: recent=%d sample=%d evidence=%+v", before.RecentBytes, before.SampleSourceBytes, before.Evidence)
	}

	// The AFTER DELETE trigger writes FTS5 inverse postings; they survive
	// until incremental optimize (reclaim).
	if _, err := db.ExecContext(ctx, `DELETE FROM search_projection_recent_documents WHERE event_id IN ('b','c')`); err != nil {
		t.Fatal(err)
	}

	if _, err := db.ExecContext(ctx, `UPDATE search_projection_recent_documents SET decoded_bytes=4000000`); err != nil {
		t.Fatal(err)
	}
	afterDelete := store.deriveSearchProjectionCapacity(ctx, db, 64<<20, 0, "")
	if err := reclaimSearchProjectionFTS(ctx, db, time.Second); err != nil {
		t.Fatalf("reclaim: %v", err)
	}
	afterReclaim := store.deriveSearchProjectionCapacity(ctx, db, 64<<20, 0, "")

	t.Logf("capacity derivation ratchet: before_ppm=%d after_delete_ppm=%d after_reclaim_ppm=%d before_recent=%d after_delete_recent=%d after_reclaim_recent=%d",
		before.AmplificationPPM, afterDelete.AmplificationPPM, afterReclaim.AmplificationPPM,
		before.RecentBytes, afterDelete.RecentBytes, afterReclaim.RecentBytes)

	if afterReclaim.RecentBytes > afterDelete.RecentBytes {
		t.Fatalf("reclaim grew recent family %d -> %d", afterDelete.RecentBytes, afterReclaim.RecentBytes)
	}
}

// TestSearchProjectionCutoffSlackCoversFourfoldCeilingRaise pins that the
// prefilter walk already includes a 4× source-text raise, so item 5 does not
// ship a re-projection pass.
func TestSearchProjectionCutoffSlackCoversFourfoldCeilingRaise(t *testing.T) {
	if searchProjectionCutoffSlackFactor != 4 {
		t.Fatalf("slack=%d, want 4; item 5 closed against this factor", searchProjectionCutoffSlackFactor)
	}
	startCeiling := int64(1000)
	walk := mulDiv(startCeiling, searchProjectionCutoffSlackFactor, 1)
	raised := startCeiling * searchProjectionCutoffSlackFactor
	if walk < raised {
		t.Fatalf("walk ceiling %d < 4× raise %d; prefilter would drop recoverable docs", walk, raised)
	}
}

func TestSearchProjectionControlStatusReportsBudgetVerdict(t *testing.T) {
	store, db := newCapacityTestStore(t, []struct{ id, body, created string }{
		{"e1", "verdict body", "2026-06-01T12:00:00Z"},
	})
	now := time.Date(2026, 6, 2, 0, 0, 0, 0, time.UTC)
	driveToCompletion(t, store, capacityBudget(64<<20), now)
	if _, err := db.Exec(`UPDATE search_projection_state SET index_family_within_budget=0 WHERE singleton=1`); err != nil {
		t.Fatal(err)
	}
	status, err := store.SearchProjectionControlStatus(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if status.State != "complete" {
		t.Fatalf("state=%q", status.State)
	}
	if status.IndexFamilyWithinBudget != 0 {
		t.Fatalf("IndexFamilyWithinBudget=%d, want 0", status.IndexFamilyWithinBudget)
	}
}
