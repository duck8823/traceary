package sqlite

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/duck8823/traceary/application/usecase"
	"github.com/duck8823/traceary/domain/model"
	"github.com/duck8823/traceary/domain/types"
)

// TestCompactPlanThenStatusLeavesEmptyWALPair is the #1848 scratch
// measurement. A read-only status leaving a 0-byte WAL + 32 KiB SHM only
// after a prior compact plan is SQLite creating reader sidecars on a
// WAL-mode file that plan just cleaned. #1845 already recovers that pair.
func TestCompactPlanThenStatusLeavesEmptyWALPair(t *testing.T) {
	t.Parallel()

	withoutPlan := prepareCompactPlanWALStore(t)
	measureCompactPlanWALStep(t, withoutPlan, "after search-retire")
	runReadOnlyPageMetadata(t, withoutPlan)
	without := measureCompactPlanWALStep(t, withoutPlan, "status after search-retire")

	withPlan := prepareCompactPlanWALStore(t)
	measureCompactPlanWALStep(t, withPlan, "after search-retire")
	runCompactPlan(t, withPlan)
	afterPlan := measureCompactPlanWALStep(t, withPlan, "after compact plan")
	runReadOnlyPageMetadata(t, withPlan)
	with := measureCompactPlanWALStep(t, withPlan, "status after compact plan")

	t.Logf("without_plan wal=%d shm=%d", without.wal, without.shm)
	t.Logf("after_plan wal=%d shm=%d", afterPlan.wal, afterPlan.shm)
	t.Logf("with_plan wal=%d shm=%d", with.wal, with.shm)

	// Status uses sqliteDSN journal_mode=WAL. The pair is a WAL sidecar, not a write.
	if with.wal != 0 || with.shm != 32*1024 {
		t.Fatalf("status after compact plan wal=%d shm=%d, want 0 and 32768", with.wal, with.shm)
	}
	if without.shm != with.shm {
		t.Fatalf("status after search-retire shm=%d, after compact-plan+status shm=%d; pair is not plan-only", without.shm, with.shm)
	}
	if afterPlan.wal != 0 || afterPlan.shm != 0 {
		t.Fatalf("compact plan left sidecars wal=%d shm=%d", afterPlan.wal, afterPlan.shm)
	}
}

type compactPlanWALSnapshot struct {
	wal int64
	shm int64
}

func prepareCompactPlanWALStore(t *testing.T) string {
	t.Helper()
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "traceary.db")
	store := NewDatabase(path, os.DirFS(filepath.Join("..", "..", "schema", "sqlite", "migrations")))
	if err := NewStoreManagementDatasource(store).Initialize(ctx); err != nil {
		t.Fatal(err)
	}
	events := NewEventDatasource(store)
	event := model.EventOf(
		mustEventID(t, "evt-1848"),
		types.EventKindNote,
		types.Client("cli"),
		mustAgent(t, "codex"),
		mustSessionID(t, "sess-1848"),
		types.Workspace("ws"),
		"compact plan wal probe",
		time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC),
	)
	if err := events.Save(ctx, event); err != nil {
		t.Fatal(err)
	}
	if _, err := store.RetireLegacySearchFamily(ctx); err != nil {
		t.Fatal(err)
	}
	return path
}

func runReadOnlyPageMetadata(t *testing.T, path string) {
	t.Helper()
	if _, err := NewPageMetadataInspector().InspectPageMetadata(context.Background(), path); err != nil {
		t.Fatal(err)
	}
}

func runCompactPlan(t *testing.T, path string) {
	t.Helper()
	dir := filepath.Dir(path)
	planner := usecase.NewStoreCompactionUsecase(
		path,
		&CompactionFileJournal{Dir: filepath.Join(dir, "journal")},
		SQLiteCompactionBuilder{},
		StoreReplacementFiles{CallerHoldsExclusiveLease: true},
		StoreLeaseCoordinator{},
	)
	if _, err := planner.Plan(context.Background(), path); err != nil {
		t.Fatal(err)
	}
}

func measureCompactPlanWALStep(t *testing.T, path, label string) compactPlanWALSnapshot {
	t.Helper()
	var snap compactPlanWALSnapshot
	if info, err := os.Stat(path + "-wal"); err == nil {
		snap.wal = info.Size()
	} else if !os.IsNotExist(err) {
		t.Fatal(err)
	}
	if info, err := os.Stat(path + "-shm"); err == nil {
		snap.shm = info.Size()
	} else if !os.IsNotExist(err) {
		t.Fatal(err)
	}
	// Do not open SQLite here: a read-only open of a WAL-mode file creates
	// the same sidecars we are measuring (#1848).
	t.Logf("%s wal=%d shm=%d", label, snap.wal, snap.shm)
	return snap
}
