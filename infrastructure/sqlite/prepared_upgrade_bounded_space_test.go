package sqlite_test

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/duck8823/traceary/application"
	"github.com/duck8823/traceary/application/usecase"
	"github.com/duck8823/traceary/domain"
	"github.com/duck8823/traceary/infrastructure/sqlite"
	sqliteschema "github.com/duck8823/traceary/schema/sqlite"
)

// TestUpgradeApplyLoopCheckpointsWALPerMigration pins the #2347 WAL bound:
// every applied migration folds its WAL with a TRUNCATE checkpoint, so the
// candidate WAL stays bounded by the largest single migration instead of the
// whole pending suffix.
func TestUpgradeApplyLoopCheckpointsWALPerMigration(t *testing.T) {
	if testing.Short() {
		t.Skip("upgrade e2e exercises clone+VACUUM+exchange")
	}
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
		t.Skip("advisory lease unsupported")
	}
	var cadence int
	sqlite.SetMigrationCadenceCheckpointHookForTest(func() { cadence++ })
	t.Cleanup(func() { sqlite.SetMigrationCadenceCheckpointHookForTest(nil) })
	ctx := context.Background()
	dir := t.TempDir()
	target := filepath.Join(dir, "store.db")
	seedLegacyStore(t, target, 35)
	insertSourceEvent(t, target, "e-cadence")
	all, err := sqliteschema.Migrations()
	if err != nil {
		t.Fatal(err)
	}
	if receipt := runUpgradeOn(ctx, t, dir, target, all); receipt.RunID == "" {
		t.Fatal("empty receipt")
	}
	// Seed 35 leaves migrations 36..86 pending; the catalog only grows, so a
	// lower bound keeps the wiring pinned without freezing the count.
	if cadence < 50 {
		t.Fatalf("cadence checkpoints = %d, want at least one per applied migration", cadence)
	}
}

// TestUpgradePlanReservesTripleSourceTransient pins the #2347 preflight
// number: source + migrated candidate + VACUUM INTO sibling.
func TestUpgradePlanReservesTripleSourceTransient(t *testing.T) {
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
		t.Skip("advisory lease unsupported")
	}
	dir := t.TempDir()
	target := filepath.Join(dir, "store.db")
	seedLegacyStore(t, target, 35)
	insertSourceEvent(t, target, "e-triple")
	all, err := sqliteschema.Migrations()
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(target)
	if err != nil {
		t.Fatal(err)
	}
	sqlite.SetSpoolReserveBytesForTest(func(string) uint64 { return 1 << 20 })
	t.Cleanup(func() { sqlite.SetSpoolReserveBytesForTest(nil) })
	journal := &sqlite.PreparedStoreUpgradeFileJournal{Dir: filepath.Join(dir, "journal")}
	recipe := &sqlite.PreparedUpgradeMigrationRecipe{PreparedMigrationCandidateRecipe: sqlite.PreparedMigrationCandidateRecipe{
		Migrations: all,
		Verifier:   sqlite.PreparedMigrationVerifier{Migrations: all},
	}}
	svc := usecase.NewPreparedStoreUpgradeUsecase(target, journal, sqlite.NewPreparedStoreUpgradeFilesForTest(true, nil), sqlite.StoreLeaseCoordinator{}, map[domain.PreparedStoreUpgradeOperation]application.PreparedCandidateRecipe{
		domain.PreparedStoreUpgradeOperationOfflineMigrationUpgrade: recipe,
	})
	run, err := svc.Plan(context.Background(), application.PreparedStoreUpgradeCommand{
		Operation:       domain.PreparedStoreUpgradeOperationOfflineMigrationUpgrade,
		TargetPath:      target,
		ConsumerBinding: "plan-triple",
		Budget:          upgradeBudget(uint64(info.Size())),
	})
	if err != nil {
		t.Fatal(err)
	}
	source := uint64(info.Size())
	if want := 3*source + (1 << 20); run.Resources.TemporaryBytes != want {
		t.Fatalf("temporary = %d, want 3x source + spool %d", run.Resources.TemporaryBytes, want)
	}
}

// TestUpgradeVacuumIntoLeavesNoResidue pins that the VACUUM INTO sibling is
// renamed over the candidate (not left beside it) and no SQLite sidecars
// survive the build on the published store.
func TestUpgradeVacuumIntoLeavesNoResidue(t *testing.T) {
	if testing.Short() {
		t.Skip("upgrade e2e exercises clone+VACUUM+exchange")
	}
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
		t.Skip("advisory lease unsupported")
	}
	ctx := context.Background()
	dir := t.TempDir()
	target := filepath.Join(dir, "store.db")
	seedLegacyStore(t, target, 35)
	insertSourceEvent(t, target, "e-vacuum-into")
	all, err := sqliteschema.Migrations()
	if err != nil {
		t.Fatal(err)
	}
	if receipt := runUpgradeOn(ctx, t, dir, target, all); receipt.RunID == "" {
		t.Fatal("empty receipt")
	}
	leftovers, err := filepath.Glob(filepath.Join(dir, "*.vacuum-*"))
	if err != nil {
		t.Fatal(err)
	}
	if len(leftovers) != 0 {
		t.Fatalf("vacuum siblings left behind: %v", leftovers)
	}
	for _, sidecar := range []string{target + "-wal", target + "-shm", target + "-journal"} {
		if _, err := os.Lstat(sidecar); !os.IsNotExist(err) {
			t.Fatalf("sidecar survives upgrade: %s", sidecar)
		}
	}
}

// TestUpgradeRestoreSpansMultiplePages forces the rowid-batched
// dedupe-archive restore across more than one 200-row page and pins that the
// conservation outcome matches the unbatched restore: every archived row
// returns to events and the archive table is dropped by the suffix.
func TestUpgradeRestoreSpansMultiplePages(t *testing.T) {
	if testing.Short() {
		t.Skip("upgrade e2e exercises clone+VACUUM+exchange")
	}
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
		t.Skip("advisory lease unsupported")
	}
	ctx := context.Background()
	dir := t.TempDir()
	source := filepath.Join(dir, "source.db")
	seedLegacyStore(t, source, 81)
	insertSourceEvent(t, source, "e-keep")
	const archived = 250
	for i := 0; i < archived; i++ {
		insertArchiveIdentityRow(t, source, fmt.Sprintf("arch-page-%03d", i), fmt.Sprintf("archived body %d", i), false)
	}
	all, err := sqliteschema.Migrations()
	if err != nil {
		t.Fatal(err)
	}
	if receipt := runUpgradeOn(ctx, t, dir, source, all); receipt.RunID == "" {
		t.Fatal("empty receipt")
	}
	db, err := sql.Open("sqlite", source)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	var restored int
	if err := db.QueryRow(`SELECT COUNT(*) FROM events WHERE id LIKE 'arch-page-%'`).Scan(&restored); err != nil {
		t.Fatal(err)
	}
	if restored != archived {
		t.Fatalf("restored events = %d, want %d", restored, archived)
	}
	var dropped string
	err = db.QueryRow(`SELECT name FROM sqlite_schema WHERE type='table' AND name='event_content_dedupe_archive'`).Scan(&dropped)
	if !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("archive table survives upgrade: %q, %v", dropped, err)
	}
}
