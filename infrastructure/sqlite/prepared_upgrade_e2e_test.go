package sqlite_test

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/duck8823/traceary/application"
	"github.com/duck8823/traceary/application/usecase"
	"github.com/duck8823/traceary/domain"
	"github.com/duck8823/traceary/infrastructure/sqlite"
	sqliteschema "github.com/duck8823/traceary/schema/sqlite"
)

func TestUpgradeEndToEndFromFresh(t *testing.T) {
	t.Skip("WITHHELD-PENDING-OWNER-DECISION #2328: fresh-store bootstrap (options A/B/C) is undecided")
}

func TestUpgradeEndToEndFromV0481(t *testing.T) {
	if testing.Short() {
		t.Skip("upgrade e2e exercises clone+VACUUM+exchange")
	}
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
		t.Skip("advisory lease unsupported")
	}
	runUpgradeEndToEnd(t, 49, false)
}

func TestRollbackRetainedUntilSpoolReplayEndsWindow(t *testing.T) {
	if testing.Short() {
		t.Skip("rollback window")
	}
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
		t.Skip("advisory lease unsupported")
	}
	ctx := context.Background()
	dir := t.TempDir()
	target := filepath.Join(dir, "store.db")
	seedLegacyStore(t, target, 35)
	insertSourceEvent(t, target, "e-rollback")
	all, err := sqliteschema.Migrations()
	if err != nil {
		t.Fatal(err)
	}
	receipt := runUpgradeOn(ctx, t, dir, target, all)
	if _, err := os.Stat(receipt.RollbackPath); err != nil {
		t.Fatalf("rollback file missing: %v", err)
	}
	info, err := os.Stat(target)
	if err != nil {
		t.Fatal(err)
	}
	journal := &sqlite.PreparedStoreUpgradeFileJournal{Dir: filepath.Join(dir, "journal")}
	recipe := &sqlite.PreparedUpgradeMigrationRecipe{PreparedMigrationCandidateRecipe: sqlite.PreparedMigrationCandidateRecipe{
		Migrations: all,
		Verifier:   sqlite.PreparedMigrationVerifier{Migrations: all},
	}}
	svc := usecase.NewPreparedStoreUpgradeUsecase(target, journal, sqlite.NewPreparedStoreUpgradeFilesForTest(true, nil), sqlite.StoreLeaseCoordinator{}, map[domain.PreparedStoreUpgradeOperation]application.PreparedCandidateRecipe{
		domain.PreparedStoreUpgradeOperationOfflineMigrationUpgrade: recipe,
	})
	_, err = svc.Rollback(ctx, receipt.RunID)
	if err == nil || !strings.Contains(err.Error(), "forensic backup") {
		t.Fatalf("Rollback error = %v, want forensic-backup refusal", err)
	}
	_ = info
}

func TestUpgradeEndToEndFromRealSizedFixture(t *testing.T) {
	if testing.Short() {
		t.Skip("real-sized upgrade e2e")
	}
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
		t.Skip("advisory lease unsupported")
	}
	runUpgradeEndToEnd(t, 49, true)
}

func TestUpgradeCandidateAppliesEntireSuffixInCatalogOrder(t *testing.T) {
	if testing.Short() {
		t.Skip("suffix apply")
	}
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
		t.Skip("advisory lease unsupported")
	}
	ctx := context.Background()
	dir := t.TempDir()
	target := filepath.Join(dir, "store.db")
	seedLegacyStore(t, target, 35)
	insertSourceEvent(t, target, "e1")
	all, err := sqliteschema.Migrations()
	if err != nil {
		t.Fatal(err)
	}
	receipt := runUpgradeOn(ctx, t, dir, target, all)
	if receipt.RunID == "" {
		t.Fatal("empty receipt")
	}
	db, err := sql.Open("sqlite", target)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	rows, err := db.Query(`SELECT version,name FROM schema_migrations ORDER BY version`)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	var versions []int64
	for rows.Next() {
		var version int64
		var name string
		if err = rows.Scan(&version, &name); err != nil {
			t.Fatal(err)
		}
		versions = append(versions, version)
	}
	if err = rows.Err(); err != nil {
		t.Fatal(err)
	}
	if versions[len(versions)-1] != 83 {
		t.Fatalf("latest version = %d, want 83", versions[len(versions)-1])
	}
	for i := 1; i < len(versions); i++ {
		if versions[i] <= versions[i-1] {
			t.Fatalf("schema_migrations not in catalog order: %v", versions)
		}
	}
}

func TestFixedOrderApplyVacuumCheckpointSyncReopenROVerifyFenceExchange(t *testing.T) {
	if testing.Short() {
		t.Skip("order e2e")
	}
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
		t.Skip("advisory lease unsupported")
	}
	var steps []string
	sqlite.SetPreparedUpgradeStepRecorderForTest(func(step string) { steps = append(steps, step) })
	t.Cleanup(func() { sqlite.SetPreparedUpgradeStepRecorderForTest(nil) })
	ctx := context.Background()
	dir := t.TempDir()
	target := filepath.Join(dir, "store.db")
	seedLegacyStore(t, target, 35)
	insertSourceEvent(t, target, "e-order")
	all, err := sqliteschema.Migrations()
	if err != nil {
		t.Fatal(err)
	}
	runUpgradeOn(ctx, t, dir, target, all)
	want := []string{"clone", "apply", "vacuum", "checkpoint", "sync", "reopen_ro", "verify", "fence", "exchange"}
	got := uniqueOrder(steps)
	if !containsSequence(got, want) {
		t.Fatalf("steps = %v, want sequence %v", got, want)
	}
	if !sqlite.PreparedVerifyOpenIsReadOnlyForTest() {
		t.Fatal("verify reopen was not read-only")
	}
}

func TestFailureAtCopyMigrateVerifyPreExchangeKeepsLiveIntact(t *testing.T) {
	if testing.Short() {
		t.Skip("injection")
	}
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
		t.Skip("advisory lease unsupported")
	}
	for _, point := range []string{"copy", "migration", "verification", "pre-exchange"} {
		t.Run(point, func(t *testing.T) {
			ctx := context.Background()
			dir := t.TempDir()
			target := filepath.Join(dir, "store.db")
			seedLegacyStore(t, target, 35)
			insertSourceEvent(t, target, "e-inject")
			before, err := os.ReadFile(target)
			if err != nil {
				t.Fatal(err)
			}
			info, err := os.Stat(target)
			if err != nil {
				t.Fatal(err)
			}
			all, err := sqliteschema.Migrations()
			if err != nil {
				t.Fatal(err)
			}
			hookPoint := point
			sqlite.SetPreparedUpgradeFailureHookForTest(func(got string) error {
				if got == hookPoint {
					return errors.New("injected " + hookPoint)
				}
				return nil
			})
			t.Cleanup(func() { sqlite.SetPreparedUpgradeFailureHookForTest(nil) })
			files := sqlite.NewPreparedStoreUpgradeFilesForTest(true, func(got string) error {
				if hookPoint == "pre-exchange" && got == "before_exchange" {
					return errors.New("injected " + hookPoint)
				}
				return nil
			})
			journal := &sqlite.PreparedStoreUpgradeFileJournal{Dir: filepath.Join(dir, "journal")}
			recipe := &sqlite.PreparedUpgradeMigrationRecipe{PreparedMigrationCandidateRecipe: sqlite.PreparedMigrationCandidateRecipe{
				Migrations: all,
				Verifier:   sqlite.PreparedMigrationVerifier{Migrations: all},
			}}
			svc := usecase.NewPreparedStoreUpgradeUsecase(target, journal, files, sqlite.StoreLeaseCoordinator{}, map[domain.PreparedStoreUpgradeOperation]application.PreparedCandidateRecipe{
				domain.PreparedStoreUpgradeOperationOfflineMigrationUpgrade: recipe,
			})
			_, err = svc.RunUpgrade(ctx, application.PreparedStoreUpgradeCommand{
				Operation:       domain.PreparedStoreUpgradeOperationOfflineMigrationUpgrade,
				TargetPath:      target,
				ConsumerBinding: "inject",
				Budget:          upgradeBudget(uint64(info.Size())),
			})
			if err == nil {
				t.Fatal("want injected failure")
			}
			after, err := os.ReadFile(target)
			if err != nil {
				t.Fatal(err)
			}
			if string(before) != string(after) {
				t.Fatal("live store mutated after injected failure")
			}
		})
	}
}

func TestCandidateVacuumedCheckpointedSyncedWithFullPreflight(t *testing.T) {
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
		t.Skip("advisory lease unsupported")
	}
	dir := t.TempDir()
	target := filepath.Join(dir, "store.db")
	seedLegacyStore(t, target, 35)
	insertSourceEvent(t, target, "e-space")
	all, err := sqliteschema.Migrations()
	if err != nil {
		t.Fatal(err)
	}
	sqlite.SetPlanAvailableBytesForTest(func(string) (uint64, error) { return 1, nil })
	t.Cleanup(func() { sqlite.SetPlanAvailableBytesForTest(nil) })
	info, err := os.Stat(target)
	if err != nil {
		t.Fatal(err)
	}
	journal := &sqlite.PreparedStoreUpgradeFileJournal{Dir: filepath.Join(dir, "journal")}
	recipe := &sqlite.PreparedUpgradeMigrationRecipe{PreparedMigrationCandidateRecipe: sqlite.PreparedMigrationCandidateRecipe{
		Migrations: all,
		Verifier:   sqlite.PreparedMigrationVerifier{Migrations: all},
	}}
	svc := usecase.NewPreparedStoreUpgradeUsecase(target, journal, sqlite.NewPreparedStoreUpgradeFilesForTest(true, nil), sqlite.StoreLeaseCoordinator{}, map[domain.PreparedStoreUpgradeOperation]application.PreparedCandidateRecipe{
		domain.PreparedStoreUpgradeOperationOfflineMigrationUpgrade: recipe,
	})
	_, err = svc.RunUpgrade(context.Background(), application.PreparedStoreUpgradeCommand{
		Operation:       domain.PreparedStoreUpgradeOperationOfflineMigrationUpgrade,
		TargetPath:      target,
		ConsumerBinding: "preflight",
		Budget:          upgradeBudget(uint64(info.Size())),
	})
	if err == nil || !strings.Contains(err.Error(), "insufficient free space") {
		t.Fatalf("error = %v, want preflight refusal", err)
	}
	matches, _ := filepath.Glob(target + ".upgrade-*")
	if len(matches) != 0 {
		t.Fatalf("candidate created despite preflight: %v", matches)
	}
}

func TestUpgradePlanReservesSourceSizedDestination(t *testing.T) {
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
		t.Skip("advisory lease unsupported")
	}
	dir := t.TempDir()
	target := filepath.Join(dir, "store.db")
	seedLegacyStore(t, target, 35)
	insertSourceEvent(t, target, "e-plan")
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
		ConsumerBinding: "plan-size",
		Budget:          upgradeBudget(uint64(info.Size())),
	})
	if err != nil {
		t.Fatal(err)
	}
	if run.Resources.DestinationBytes != uint64(info.Size()) {
		t.Fatalf("destination = %d, want source size %d", run.Resources.DestinationBytes, info.Size())
	}
	if !strings.Contains(run.CandidatePath, ".upgrade-") {
		t.Fatalf("candidate path = %q, want .upgrade- suffix", run.CandidatePath)
	}
	tiny := upgradeBudget(uint64(info.Size()))
	tiny.PublishLockLimit = time.Second
	_, err = svc.Plan(context.Background(), application.PreparedStoreUpgradeCommand{
		Operation:       domain.PreparedStoreUpgradeOperationOfflineMigrationUpgrade,
		TargetPath:      target,
		ConsumerBinding: "plan-size-1s",
		Budget:          tiny,
	})
	if err != nil {
		t.Fatalf("upgrade must accept a 1s PublishLockLimit (no rehearsal 1s cap): %v", err)
	}
}

func TestPrePublishVerificationCoversFiveTablesPlusIntegrityPlusLaws(t *testing.T) {
	if testing.Short() {
		t.Skip("verifier")
	}
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
		t.Skip("advisory lease unsupported")
	}
	ctx := context.Background()
	dir := t.TempDir()
	source := filepath.Join(dir, "source.db")
	seedLegacyStore(t, source, 76)
	insertSourceEvent(t, source, "e-verify")
	seedObservation(t, source)
	all, err := sqliteschema.Migrations()
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(source)
	if err != nil {
		t.Fatal(err)
	}
	recipe := &sqlite.PreparedUpgradeMigrationRecipe{PreparedMigrationCandidateRecipe: sqlite.PreparedMigrationCandidateRecipe{
		Migrations: all,
		Verifier:   sqlite.PreparedMigrationVerifier{Migrations: all},
	}}
	journal := &sqlite.PreparedStoreUpgradeFileJournal{Dir: filepath.Join(dir, "journal")}
	svc := usecase.NewPreparedStoreUpgradeUsecase(source, journal, sqlite.NewPreparedStoreUpgradeFilesForTest(true, nil), sqlite.StoreLeaseCoordinator{}, map[domain.PreparedStoreUpgradeOperation]application.PreparedCandidateRecipe{
		domain.PreparedStoreUpgradeOperationOfflineMigrationUpgrade: recipe,
	})
	run, err := svc.Plan(ctx, application.PreparedStoreUpgradeCommand{
		Operation:       domain.PreparedStoreUpgradeOperationOfflineMigrationUpgrade,
		TargetPath:      source,
		ConsumerBinding: "verify",
		Budget:          upgradeBudget(uint64(info.Size())),
	})
	if err != nil {
		t.Fatal(err)
	}
	run, err = svc.Prepare(ctx, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	sourceDB, err := sql.Open("sqlite", source)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = sourceDB.Close() }()
	candidateDB, err := sql.Open("sqlite", run.CandidatePath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = candidateDB.Close() }()
	if err := sqlite.IdenticalObservationCountsForTest(ctx, sourceDB, candidateDB); err == nil {
		t.Fatal("identical-counts comparison must reject a legitimate 76 collapse")
	}
	verifier := sqlite.PreparedMigrationVerifier{Migrations: all}
	if _, err := verifier.VerifyUpgradePair(ctx, source, run.CandidatePath, run.PlanDigest); err != nil {
		t.Fatalf("good candidate: %v", err)
	}

	t.Run("dropped events", func(t *testing.T) {
		corrupt := copyCandidate(t, run.CandidatePath)
		db, err := sql.Open("sqlite", corrupt)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(`DELETE FROM events`); err != nil {
			t.Fatal(err)
		}
		_ = db.Close()
		if _, err := verifier.VerifyUpgradePair(ctx, source, corrupt, run.PlanDigest); err == nil {
			t.Fatal("dropped events must fail verification")
		}
	})
	t.Run("rewritten body", func(t *testing.T) {
		corrupt := copyCandidate(t, run.CandidatePath)
		db, err := sql.Open("sqlite", corrupt)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(`UPDATE events SET kind = 'tampered'`); err != nil {
			t.Fatal(err)
		}
		_ = db.Close()
		if _, err := verifier.VerifyUpgradePair(ctx, source, corrupt, run.PlanDigest); err == nil {
			t.Fatal("rewritten event body must fail verification")
		}
	})
	t.Run("truncated ledger", func(t *testing.T) {
		corrupt := copyCandidate(t, run.CandidatePath)
		db, err := sql.Open("sqlite", corrupt)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(`DELETE FROM schema_migrations WHERE version = (SELECT max(version) FROM schema_migrations)`); err != nil {
			t.Fatal(err)
		}
		_ = db.Close()
		if _, err := verifier.VerifyUpgradePair(ctx, source, corrupt, run.PlanDigest); err == nil {
			t.Fatal("truncated ledger must fail verification")
		}
	})
	t.Run("page corruption keeps integrity_check", func(t *testing.T) {
		corrupt := copyCandidate(t, run.CandidatePath)
		data, err := os.ReadFile(corrupt)
		if err != nil {
			t.Fatal(err)
		}
		if len(data) < 200 {
			t.Fatal("candidate too small to corrupt")
		}
		data[100] ^= 0xff
		data[101] ^= 0xff
		data[102] ^= 0xff
		if err := os.WriteFile(corrupt, data, 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := verifier.VerifyUpgradePair(ctx, source, corrupt, run.PlanDigest); err == nil {
			t.Fatal("page corruption must fail integrity_check")
		}
	})

	t.Run("v81 restore conservation", func(t *testing.T) {
		dir := t.TempDir()
		v81Source := filepath.Join(dir, "source.db")
		seedLegacyStore(t, v81Source, 81)
		insertSourceEvent(t, v81Source, "e-keep")
		insertArchiveIdentityRow(t, v81Source, "arch-1", "archived one", false)
		insertArchiveIdentityRow(t, v81Source, "arch-2", "archived two", false)
		info, err := os.Stat(v81Source)
		if err != nil {
			t.Fatal(err)
		}
		recipe := &sqlite.PreparedUpgradeMigrationRecipe{PreparedMigrationCandidateRecipe: sqlite.PreparedMigrationCandidateRecipe{
			Migrations: all,
			Verifier:   sqlite.PreparedMigrationVerifier{Migrations: all},
		}}
		journal := &sqlite.PreparedStoreUpgradeFileJournal{Dir: filepath.Join(dir, "journal")}
		svc := usecase.NewPreparedStoreUpgradeUsecase(v81Source, journal, sqlite.NewPreparedStoreUpgradeFilesForTest(true, nil), sqlite.StoreLeaseCoordinator{}, map[domain.PreparedStoreUpgradeOperation]application.PreparedCandidateRecipe{
			domain.PreparedStoreUpgradeOperationOfflineMigrationUpgrade: recipe,
		})
		v81Run, err := svc.Plan(ctx, application.PreparedStoreUpgradeCommand{
			Operation:       domain.PreparedStoreUpgradeOperationOfflineMigrationUpgrade,
			TargetPath:      v81Source,
			ConsumerBinding: "v81-law",
			Budget:          upgradeBudget(uint64(info.Size())),
		})
		if err != nil {
			t.Fatal(err)
		}
		v81Run, err = svc.Prepare(ctx, v81Run.ID)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := verifier.VerifyUpgradePair(ctx, v81Source, v81Run.CandidatePath, v81Run.PlanDigest); err != nil {
			t.Fatalf("restored candidate: %v", err)
		}
		corrupt := copyCandidate(t, v81Run.CandidatePath)
		db, err := sql.Open("sqlite", corrupt)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(`DELETE FROM events WHERE id = 'arch-1'`); err != nil {
			t.Fatal(err)
		}
		_ = db.Close()
		if _, err := verifier.VerifyUpgradePair(ctx, v81Source, corrupt, v81Run.PlanDigest); err == nil {
			t.Fatal("silently dropped archived row must fail the restore conservation law")
		}
	})
}

func TestUpgradePublishesOnlyAfterVerificationViaAtomicExchange(t *testing.T) {
	if testing.Short() {
		t.Skip("publish gate")
	}
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
		t.Skip("advisory lease unsupported")
	}
	ctx := context.Background()
	dir := t.TempDir()
	target := filepath.Join(dir, "store.db")
	seedLegacyStore(t, target, 35)
	insertSourceEvent(t, target, "e-gate")
	before, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(target)
	if err != nil {
		t.Fatal(err)
	}
	all, err := sqliteschema.Migrations()
	if err != nil {
		t.Fatal(err)
	}
	sqlite.SetPreparedUpgradeFailureHookForTest(func(got string) error {
		if got == "verification" {
			return errors.New("injected verification")
		}
		return nil
	})
	t.Cleanup(func() { sqlite.SetPreparedUpgradeFailureHookForTest(nil) })
	var hooks []string
	files := sqlite.NewPreparedStoreUpgradeFilesForTest(true, func(got string) error {
		hooks = append(hooks, got)
		return nil
	})
	journal := &sqlite.PreparedStoreUpgradeFileJournal{Dir: filepath.Join(dir, "journal")}
	recipe := &sqlite.PreparedUpgradeMigrationRecipe{PreparedMigrationCandidateRecipe: sqlite.PreparedMigrationCandidateRecipe{
		Migrations: all,
		Verifier:   sqlite.PreparedMigrationVerifier{Migrations: all},
	}}
	svc := usecase.NewPreparedStoreUpgradeUsecase(target, journal, files, sqlite.StoreLeaseCoordinator{}, map[domain.PreparedStoreUpgradeOperation]application.PreparedCandidateRecipe{
		domain.PreparedStoreUpgradeOperationOfflineMigrationUpgrade: recipe,
	})
	_, err = svc.RunUpgrade(ctx, application.PreparedStoreUpgradeCommand{
		Operation:       domain.PreparedStoreUpgradeOperationOfflineMigrationUpgrade,
		TargetPath:      target,
		ConsumerBinding: "publish-gate",
		Budget:          upgradeBudget(uint64(info.Size())),
	})
	if err == nil {
		t.Fatal("want verification failure")
	}
	after, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Fatal("live store mutated after verification failure")
	}
	for _, hook := range hooks {
		if hook == "after_exchange" {
			t.Fatal("exchange must not run after verification failure")
		}
	}
}

func TestCrashAfterMarkerBeforeCandidateConverges(t *testing.T) {
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
		t.Skip("advisory lease unsupported")
	}
	path := filepath.Join(t.TempDir(), "store.db")
	if err := os.WriteFile(path, []byte("store"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := sqlite.WriteCompactPendingMarkerForTest(path); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(path + ".traceary.compact-pending")
	if err != nil {
		t.Fatal(err)
	}
	_ = os.WriteFile(path+".traceary.compact-pending", []byte("99999999\n"), 0o600)
	_ = raw
	if sqlite.CompactPendingActive(path) {
		t.Fatal("dead-pid marker must be reaped")
	}
}

func TestSpoolReserveExhaustedMidRunConverges(t *testing.T) {
	if testing.Short() {
		t.Skip("spool reserve mid-run")
	}
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
		t.Skip("advisory lease unsupported")
	}
	ctx := context.Background()
	dir := t.TempDir()
	target := filepath.Join(dir, "store.db")
	seedLegacyStore(t, target, 35)
	insertSourceEvent(t, target, "e-enospc")
	before, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(target)
	if err != nil {
		t.Fatal(err)
	}
	all, err := sqliteschema.Migrations()
	if err != nil {
		t.Fatal(err)
	}
	sqlite.SetPreparedUpgradeFailureHookForTest(func(got string) error {
		if got == "migration" {
			return errors.New("no space left on device")
		}
		return nil
	})
	t.Cleanup(func() { sqlite.SetPreparedUpgradeFailureHookForTest(nil) })
	coord := sqlite.StoreLeaseCoordinator{}
	journal := &sqlite.PreparedStoreUpgradeFileJournal{Dir: filepath.Join(dir, "journal")}
	recipe := &sqlite.PreparedUpgradeMigrationRecipe{PreparedMigrationCandidateRecipe: sqlite.PreparedMigrationCandidateRecipe{
		Migrations: all,
		Verifier:   sqlite.PreparedMigrationVerifier{Migrations: all},
	}}
	svc := usecase.NewPreparedStoreUpgradeUsecase(target, journal, sqlite.NewPreparedStoreUpgradeFilesForTest(true, nil), coord, map[domain.PreparedStoreUpgradeOperation]application.PreparedCandidateRecipe{
		domain.PreparedStoreUpgradeOperationOfflineMigrationUpgrade: recipe,
	})
	_, err = svc.RunUpgrade(ctx, application.PreparedStoreUpgradeCommand{
		Operation:       domain.PreparedStoreUpgradeOperationOfflineMigrationUpgrade,
		TargetPath:      target,
		ConsumerBinding: "enospc",
		Budget:          upgradeBudget(uint64(info.Size())),
	})
	if err == nil {
		t.Fatal("want mid-run space exhaustion")
	}
	after, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Fatal("live store mutated after mid-run ENOSPC")
	}
	if sqlite.CompactPendingActive(target) {
		t.Fatal("compact-pending marker must be released after ENOSPC")
	}
	if coord.HoldsExclusive(target) {
		t.Fatal("exclusive lease must be released after ENOSPC")
	}
}

func TestCrashAfterExchangeConverges(t *testing.T) {
	if testing.Short() {
		t.Skip("crash-after-exchange")
	}
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
		t.Skip("advisory lease unsupported")
	}
	ctx := context.Background()
	dir := t.TempDir()
	target := filepath.Join(dir, "store.db")
	seedLegacyStore(t, target, 35)
	insertSourceEvent(t, target, "e-crash")
	all, err := sqliteschema.Migrations()
	if err != nil {
		t.Fatal(err)
	}
	injected := false
	files := sqlite.NewPreparedStoreUpgradeFilesForTest(true, func(got string) error {
		if got == "after_exchange" && !injected {
			injected = true
			return errors.New("crash after exchange")
		}
		return nil
	})
	info, err := os.Stat(target)
	if err != nil {
		t.Fatal(err)
	}
	journal := &sqlite.PreparedStoreUpgradeFileJournal{Dir: filepath.Join(dir, "journal")}
	recipe := &sqlite.PreparedUpgradeMigrationRecipe{PreparedMigrationCandidateRecipe: sqlite.PreparedMigrationCandidateRecipe{
		Migrations: all,
		Verifier:   sqlite.PreparedMigrationVerifier{Migrations: all},
	}}
	svc := usecase.NewPreparedStoreUpgradeUsecase(target, journal, files, sqlite.StoreLeaseCoordinator{}, map[domain.PreparedStoreUpgradeOperation]application.PreparedCandidateRecipe{
		domain.PreparedStoreUpgradeOperationOfflineMigrationUpgrade: recipe,
	})
	cmd := application.PreparedStoreUpgradeCommand{
		Operation:       domain.PreparedStoreUpgradeOperationOfflineMigrationUpgrade,
		TargetPath:      target,
		ConsumerBinding: "crash-exchange",
		Budget:          upgradeBudget(uint64(info.Size())),
	}
	_, err = svc.RunUpgrade(ctx, cmd)
	if err == nil {
		t.Fatal("want crash after exchange")
	}
	receipt, err := svc.RunUpgrade(ctx, cmd)
	if err != nil {
		t.Fatalf("resume after exchange crash: %v", err)
	}
	if receipt.RunID == "" {
		t.Fatal("empty resume receipt")
	}
}

func runUpgradeEndToEnd(t *testing.T, beforeVersion int, realSized bool) {
	t.Helper()
	ctx := context.Background()
	dir := t.TempDir()
	target := filepath.Join(dir, "store.db")
	seedLegacyStore(t, target, beforeVersion)
	insertSourceEvent(t, target, "e-e2e")
	if realSized {
		db, err := sql.Open("sqlite", target)
		if err != nil {
			t.Fatal(err)
		}
		for i := 0; i < 200; i++ {
			if _, err := db.Exec(`INSERT INTO events(id,kind,agent,session_id,body,created_at,client,workspace) VALUES(?,?,?,?,?,?,?,?)`,
				fmt.Sprintf("e-%d", i), "note", "a", "s", strings.Repeat("x", 1024), time.Now().UTC().Format(time.RFC3339Nano), "c", "w"); err != nil {
				t.Fatal(err)
			}
		}
		_ = db.Close()
	}
	all, err := sqliteschema.Migrations()
	if err != nil {
		t.Fatal(err)
	}
	runUpgradeOn(ctx, t, dir, target, all)
	db, err := sql.Open("sqlite", target)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	var maxVersion int
	if err := db.QueryRow(`SELECT max(version) FROM schema_migrations`).Scan(&maxVersion); err != nil || maxVersion != 83 {
		t.Fatalf("published version=%d err=%v", maxVersion, err)
	}
}

func runUpgradeOn(ctx context.Context, t *testing.T, dir, target string, all fs.FS) application.PreparedStoreUpgradeReceipt {
	t.Helper()
	info, err := os.Stat(target)
	if err != nil {
		t.Fatal(err)
	}
	return runUpgradeOnWithBudget(ctx, t, dir, target, all, upgradeBudget(uint64(info.Size())))
}

func runUpgradeOnWithBudget(ctx context.Context, t *testing.T, dir, target string, all fs.FS, budget domain.PreparedStoreUpgradeBudget) application.PreparedStoreUpgradeReceipt {
	t.Helper()
	journal := &sqlite.PreparedStoreUpgradeFileJournal{Dir: filepath.Join(dir, "journal")}
	recipe := &sqlite.PreparedUpgradeMigrationRecipe{PreparedMigrationCandidateRecipe: sqlite.PreparedMigrationCandidateRecipe{
		Migrations: all,
		Verifier:   sqlite.PreparedMigrationVerifier{Migrations: all},
	}}
	svc := usecase.NewPreparedStoreUpgradeUsecase(target, journal, sqlite.NewPreparedStoreUpgradeFilesForTest(true, nil), sqlite.StoreLeaseCoordinator{}, map[domain.PreparedStoreUpgradeOperation]application.PreparedCandidateRecipe{
		domain.PreparedStoreUpgradeOperationOfflineMigrationUpgrade: recipe,
	})
	receipt, err := svc.RunUpgrade(ctx, application.PreparedStoreUpgradeCommand{
		Operation:       domain.PreparedStoreUpgradeOperationOfflineMigrationUpgrade,
		TargetPath:      target,
		ConsumerBinding: "e2e",
		Budget:          budget,
	})
	if err != nil {
		t.Fatal(err)
	}
	return receipt
}

func upgradeBudget(size uint64) domain.PreparedStoreUpgradeBudget {
	if size < 1<<20 {
		size = 1 << 20
	}
	return domain.PreparedStoreUpgradeBudget{
		WallTimeLimit:      time.Hour,
		PublishLockLimit:   time.Hour,
		OwnedDiskByteLimit: size*16 + 1<<30,
		WALByteLimit:       size*4 + 1<<30,
		TemporaryByteLimit: size*8 + 1<<30,
		SafetyMarginBytes:  64 << 20,
	}
}

func seedLegacyStore(t *testing.T, path string, beforeVersion int) {
	t.Helper()
	legacy := newStoreManagementDatasource(t, path, onDiskSQLiteMigrationsBefore(t, beforeVersion))
	if err := legacy.Initialize(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func seedObservation(t *testing.T, path string) {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	_, err = db.Exec(`
		INSERT INTO sessions (session_id, started_at, client, agent, workspace)
		VALUES ('session-1', '2026-07-22T00:00:00Z', 'hook', 'codex', '/repo')
		ON CONFLICT(session_id) DO NOTHING;
		INSERT INTO session_workspace_observations (
			observation_id, session_id, workspace, raw_workspace, observation_kind,
			observation_origin, observed_relationship, observed_event_id,
			delivery_record_id, attribution_fingerprint, diagnostic_reason,
			observed_at, source_client, source_hook
		) VALUES
			('o1', 'session-1', '/repo', NULL, 'primary', 'runtime', 'exact', 'event-a1', 'd1', 'fp1', '', '2026-07-22T00:00:00Z', 'codex', 'user_prompt_submit'),
			('o2', 'session-1', '/repo', NULL, 'primary', 'runtime', 'exact', 'event-a2', NULL, 'fp2', '', '2026-07-22T00:00:01Z', 'codex', 'user_prompt_submit')
	`)
	if err != nil {
		t.Fatal(err)
	}
}

func copyCandidate(t *testing.T, src string) string {
	t.Helper()
	dst := src + ".corrupt-" + fmt.Sprintf("%d", time.Now().UnixNano())
	if err := copyFileForTest(src, dst); err != nil {
		t.Fatal(err)
	}
	return dst
}

func copyFileForTest(src, dst string) error {
	data, err := os.ReadFile(src)
	if err != nil {
		return fmt.Errorf("read candidate copy source: %w", err)
	}
	if err := os.WriteFile(dst, data, 0o600); err != nil {
		return fmt.Errorf("write candidate copy: %w", err)
	}
	return nil
}

func uniqueOrder(steps []string) []string {
	var out []string
	seen := map[string]bool{}
	for _, step := range steps {
		if seen[step] {
			continue
		}
		seen[step] = true
		out = append(out, step)
	}
	return out
}

func containsSequence(got, want []string) bool {
	i := 0
	for _, step := range got {
		if i < len(want) && step == want[i] {
			i++
		}
	}
	return i == len(want)
}

type countingUpgradeLease struct {
	inner    sqlite.StoreLeaseCoordinator
	acquires int
}

func (l *countingUpgradeLease) AcquireExclusive(ctx context.Context, path string) (func(), error) {
	l.acquires++
	release, err := l.inner.AcquireExclusive(ctx, path)
	if err != nil {
		return nil, fmt.Errorf("acquire exclusive for upgrade hold: %w", err)
	}
	return release, nil
}

func (l *countingUpgradeLease) HoldsExclusive(path string) bool {
	return l.inner.HoldsExclusive(path)
}

func TestUpgradeHoldsExclusiveAcrossPlanPreparePublish(t *testing.T) {
	if testing.Short() {
		t.Skip("hold e2e")
	}
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
		t.Skip("advisory lease unsupported")
	}
	ctx := context.Background()
	dir := t.TempDir()
	target := filepath.Join(dir, "store.db")
	seedLegacyStore(t, target, 35)
	insertSourceEvent(t, target, "e-hold")
	all, err := sqliteschema.Migrations()
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(target)
	if err != nil {
		t.Fatal(err)
	}
	lease := &countingUpgradeLease{}
	journal := &sqlite.PreparedStoreUpgradeFileJournal{Dir: filepath.Join(dir, "journal")}
	recipe := &sqlite.PreparedUpgradeMigrationRecipe{PreparedMigrationCandidateRecipe: sqlite.PreparedMigrationCandidateRecipe{
		Migrations: all,
		Verifier:   sqlite.PreparedMigrationVerifier{Migrations: all},
	}}
	svc := usecase.NewPreparedStoreUpgradeUsecase(target, journal, sqlite.NewPreparedStoreUpgradeFilesForTest(true, nil), lease, map[domain.PreparedStoreUpgradeOperation]application.PreparedCandidateRecipe{
		domain.PreparedStoreUpgradeOperationOfflineMigrationUpgrade: recipe,
	})
	if _, err := svc.RunUpgrade(ctx, application.PreparedStoreUpgradeCommand{
		Operation:       domain.PreparedStoreUpgradeOperationOfflineMigrationUpgrade,
		TargetPath:      target,
		ConsumerBinding: "hold",
		Budget:          upgradeBudget(uint64(info.Size())),
	}); err != nil {
		t.Fatal(err)
	}
	if lease.acquires != 1 {
		t.Fatalf("AcquireExclusive calls = %d, want 1 (inner acquires skipped while held)", lease.acquires)
	}
}
