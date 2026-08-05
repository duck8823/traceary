package sqlite

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	apptypes "github.com/duck8823/traceary/application/types"
	"github.com/duck8823/traceary/domain"
)

func migrationTestBudget() apptypes.SegmentMigrationBudget {
	return apptypes.SegmentMigrationBudget{PageRows: 1, MaxSteps: 32, WallTime: time.Minute, LockTime: time.Second, MaxPlainBytes: 1 << 20, MaxStoredBytes: 1 << 20, MaxValuePlainBytes: 1 << 20, MaxValueStoredBytes: 1 << 20, MaxFileBytes: 2 << 20, MaxSummaryBytes: 1 << 20, MaxSummaryRows: 1000, MaxWALBytes: 16 << 20, MinFreeDiskBytes: 2}
}

func TestSegmentMigrationStartEnvelopeRejectsEveryExpansion(t *testing.T) {
	database, run, envelope, _ := newMigrationTestRun(t, 2)
	ctx := context.Background()
	tightened := envelope
	tightened.MaxSteps--
	tightened.WallTime--
	tightened.LockTime--
	tightened.MaxPlainBytes--
	tightened.MaxStoredBytes--
	tightened.MaxValuePlainBytes--
	tightened.MaxValueStoredBytes--
	tightened.MaxFileBytes--
	tightened.MaxSummaryBytes--
	tightened.MaxWALBytes--
	tightened.MaxSummaryRows--
	tightened.MinFreeDiskBytes++
	if _, err := database.AdvanceSegmentMigration(ctx, run.ID, tightened); err != nil {
		t.Fatalf("tightened envelope rejected: %v", err)
	}

	tests := map[string]func(*apptypes.SegmentMigrationBudget){
		"page rows":              func(b *apptypes.SegmentMigrationBudget) { b.PageRows++ },
		"steps":                  func(b *apptypes.SegmentMigrationBudget) { b.MaxSteps++ },
		"wall time":              func(b *apptypes.SegmentMigrationBudget) { b.WallTime++ },
		"lock time":              func(b *apptypes.SegmentMigrationBudget) { b.LockTime++ },
		"plain bytes":            func(b *apptypes.SegmentMigrationBudget) { b.MaxPlainBytes++ },
		"stored bytes":           func(b *apptypes.SegmentMigrationBudget) { b.MaxStoredBytes++ },
		"value plain bytes":      func(b *apptypes.SegmentMigrationBudget) { b.MaxValuePlainBytes++ },
		"value stored bytes":     func(b *apptypes.SegmentMigrationBudget) { b.MaxValueStoredBytes++ },
		"file bytes":             func(b *apptypes.SegmentMigrationBudget) { b.MaxFileBytes++ },
		"summary bytes":          func(b *apptypes.SegmentMigrationBudget) { b.MaxSummaryBytes++ },
		"WAL bytes":              func(b *apptypes.SegmentMigrationBudget) { b.MaxWALBytes++ },
		"free disk safety floor": func(b *apptypes.SegmentMigrationBudget) { b.MinFreeDiskBytes-- },
		"summary rows":           func(b *apptypes.SegmentMigrationBudget) { b.MaxSummaryRows++ },
	}
	for name, expand := range tests {
		t.Run(name, func(t *testing.T) {
			budget := envelope
			expand(&budget)
			if _, err := database.AdvanceSegmentMigration(ctx, run.ID, budget); !errors.Is(err, apptypes.ErrSegmentMigrationEnvelopeExpansion) {
				t.Fatalf("error = %v", err)
			}
		})
	}
}

func TestSegmentMigrationBootstrapRejectsAbsoluteOversizedWAL(t *testing.T) {
	database, run, budget, _ := newMigrationTestRun(t, 1)
	wal := database.Path() + "-wal"
	file, err := os.OpenFile(wal, os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if err = file.Truncate(segmentMigrationBootstrapMaxWAL + 1); err != nil {
		_ = file.Close()
		t.Fatal(err)
	}
	_ = file.Close()
	if _, err = database.LoadSegmentMigration(context.Background(), run.ID); !errors.Is(err, apptypes.ErrSegmentMigrationLimit) {
		t.Fatalf("load error = %v", err)
	}
	budget.MaxWALBytes = segmentMigrationBootstrapMaxWAL * 2
	if _, err = database.ExecuteSegmentMigrationAction(context.Background(), run.ID, domain.SegmentMigrationActionBeginCopy, budget); !errors.Is(err, apptypes.ErrSegmentMigrationLimit) {
		t.Fatalf("execute error = %v", err)
	}
}

func TestSegmentMigrationStartRejectsAbsoluteOversizedWAL(t *testing.T) {
	ctx := context.Background()
	database, raw := newActivatedCatalogStore(t, 1)
	t.Cleanup(func() { _ = raw.Close() })
	initial, err := database.CurrentCatalogRanges(ctx, catalogTestBudget())
	if err != nil {
		t.Fatal(err)
	}
	plan, err := database.PlanAndReserveCatalogTarget(ctx, plannerRequest(initial.Head, 1, "migration-start-wal", 1))
	if err != nil {
		t.Fatal(err)
	}
	var storeID string
	if err = raw.QueryRow(`SELECT store_id FROM archive_store_lineage WHERE singleton=1`).Scan(&storeID); err != nil {
		t.Fatal(err)
	}
	wal, err := os.OpenFile(database.Path()+"-wal", os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if err = wal.Truncate(segmentMigrationBootstrapMaxWAL + 1); err != nil {
		_ = wal.Close()
		t.Fatal(err)
	}
	_ = wal.Close()
	budget := migrationTestBudget()
	budget.MaxWALBytes = segmentMigrationBootstrapMaxWAL * 2
	_, err = database.StartSegmentMigration(ctx, apptypes.SegmentMigrationStart{RunID: "oversized-start", StoreID: storeID, ReservationID: plan.ReservationID, PlanDigest: plan.PlanDigest, SoftwareCommit: "test", Range: plan.Range, CandidateRoot: t.TempDir(), ArchiveRoot: t.TempDir(), CompressionFloor: 32, Budget: budget})
	if !errors.Is(err, apptypes.ErrSegmentMigrationLimit) {
		t.Fatalf("start error = %v", err)
	}
}

func TestSegmentMigrationSerializesExternalActionsUnderLease(t *testing.T) {
	actions := []domain.SegmentMigrationAction{
		domain.SegmentMigrationActionBuildCandidate,
		domain.SegmentMigrationActionInstall,
		domain.SegmentMigrationActionSeal,
		domain.SegmentMigrationActionVerify,
	}
	for _, target := range actions {
		t.Run(string(target), func(t *testing.T) {
			database, run, budget, _ := newMigrationTestRun(t, 1)
			for steps := 0; steps < 20; steps++ {
				action, ok := run.NextAction()
				if ok && action == target {
					break
				}
				var err error
				run, err = database.AdvanceSegmentMigration(context.Background(), run.ID, budget)
				if err != nil {
					t.Fatal(err)
				}
			}
			lease, err := acquireAdvisoryLease(context.Background(), database.Path()+".segment-migration.lock", true)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = lease.Close() }()
			budget.LockTime = 5 * time.Millisecond
			got, err := database.ExecuteSegmentMigrationAction(context.Background(), run.ID, target, budget)
			if err == nil || got.Revision != run.Revision {
				t.Fatalf("got=%+v err=%v", got, err)
			}
		})
	}

	t.Run("recover", func(t *testing.T) {
		database, run, budget, _ := newMigrationTestRun(t, 1)
		for run.Phase != domain.SegmentMigrationInstallIntent {
			var err error
			run, err = database.AdvanceSegmentMigration(context.Background(), run.ID, budget)
			if err != nil {
				t.Fatal(err)
			}
		}
		lease, err := acquireAdvisoryLease(context.Background(), database.Path()+".segment-migration.lock", true)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = lease.Close() }()
		budget.LockTime = 5 * time.Millisecond
		got, err := database.RecoverSegmentMigration(context.Background(), run.ID, budget)
		if err == nil || got.Revision != run.Revision {
			t.Fatalf("got=%+v err=%v", got, err)
		}
	})
}

func newMigrationTestRun(t *testing.T, events int) (*Database, domain.SegmentMigrationRun, apptypes.SegmentMigrationBudget, string) {
	t.Helper()
	ctx := context.Background()
	database, raw := newActivatedCatalogStore(t, events)
	t.Cleanup(func() { _ = raw.Close() })
	initial, err := database.CurrentCatalogRanges(ctx, catalogTestBudget())
	if err != nil {
		t.Fatal(err)
	}
	plan, err := database.PlanAndReserveCatalogTarget(ctx, plannerRequest(initial.Head, int64(events), "migration-target", events))
	if err != nil {
		t.Fatal(err)
	}
	var storeID string
	if err = raw.QueryRow(`SELECT store_id FROM archive_store_lineage WHERE singleton=1`).Scan(&storeID); err != nil {
		t.Fatal(err)
	}
	candidate, archive := t.TempDir(), t.TempDir()
	budget := migrationTestBudget()
	run, err := database.StartSegmentMigration(ctx, apptypes.SegmentMigrationStart{RunID: "migration-run", StoreID: storeID, ReservationID: plan.ReservationID, PlanDigest: plan.PlanDigest, SoftwareCommit: "test-commit", Range: plan.Range, CandidateRoot: candidate, ArchiveRoot: archive, CompressionFloor: 32, Budget: budget})
	if err != nil {
		t.Fatal(err)
	}
	return database, run, budget, archive
}

func TestSegmentMigrationResumesPagesToVerifiedShadowWithoutAuthorityCutover(t *testing.T) {
	database, run, budget, archive := newMigrationTestRun(t, 2)
	ctx := context.Background()
	var err error
	for i := 0; i < 20 && run.Phase != domain.SegmentMigrationVerifiedShadow; i++ {
		run, err = database.AdvanceSegmentMigration(ctx, run.ID, budget)
		if err != nil {
			t.Fatalf("phase %s revision %d: %v", run.Phase, run.Revision, err)
		}
	}
	if run.Phase != domain.SegmentMigrationVerifiedShadow {
		t.Fatalf("run=%+v", run)
	}
	snapshot, err := database.CurrentCatalogRanges(ctx, catalogTestBudget())
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Ranges) != 1 || snapshot.Ranges[0].Placement != domain.CatalogPlacementVerifiedShadow {
		t.Fatalf("ranges=%+v", snapshot.Ranges)
	}
	if _, err = os.Stat(filepath.Join(archive, run.SegmentID)); err != nil {
		t.Fatal(err)
	}
	proofDB, err := database.openReadOnly(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer database.release(proofDB)
	var evidenceDigest string
	var aggregate []byte
	if err = proofDB.QueryRowContext(ctx, `SELECT evidence_digest,aggregate_json FROM archive_segment_migration_evidence WHERE run_id=?`, run.ID).Scan(&evidenceDigest, &aggregate); err != nil {
		t.Fatal(err)
	}
	if !domain.ValidCatalogDigest(evidenceDigest) || bytes.Contains(aggregate, []byte(archive)) {
		t.Fatalf("invalid or path-bearing evidence: digest=%q", evidenceDigest)
	}
	if _, err = database.AdvanceSegmentMigration(ctx, run.ID, budget); err != nil {
		t.Fatal(err)
	}
	db, err := database.open(ctx)
	if err != nil {
		t.Fatal(err)
	}
	_, mutationErr := db.ExecContext(ctx, `UPDATE events SET body='after-shadow' WHERE id='catalog-event-b'`)
	database.release(db)
	if mutationErr == nil {
		t.Fatal("verified-shadow source mutation succeeded")
	}
}

func TestSegmentMigrationRollbackRetainsBoundFileAndRestoresReserved(t *testing.T) {
	database, run, budget, archive := newMigrationTestRun(t, 1)
	ctx := context.Background()
	var err error
	for run.Phase != domain.SegmentMigrationVerifiedShadow {
		run, err = database.AdvanceSegmentMigration(ctx, run.ID, budget)
		if err != nil {
			t.Fatal(err)
		}
	}
	bound := filepath.Join(archive, run.SegmentID)
	run, err = database.AdvanceSegmentMigrationRollback(ctx, run.ID, budget)
	if err != nil {
		t.Fatal(err)
	}
	run, err = database.AdvanceSegmentMigrationRollback(ctx, run.ID, budget)
	if err != nil {
		t.Fatal(err)
	}
	if run.Phase != domain.SegmentMigrationRolledBack {
		t.Fatal(run.Phase)
	}
	if _, err = os.Stat(bound); err != nil {
		t.Fatal(err)
	}
	snapshot, err := database.CurrentCatalogRanges(ctx, catalogTestBudget())
	if err != nil || snapshot.Ranges[0].Placement != domain.CatalogPlacementReserved {
		t.Fatalf("snapshot=%+v err=%v", snapshot, err)
	}
}

func TestSegmentMigrationRejectsSecondActiveRunAndReservedMutation(t *testing.T) {
	database, run, budget, _ := newMigrationTestRun(t, 1)
	ctx := context.Background()
	loaded, err := database.LoadSegmentMigration(ctx, run.ID)
	if err != nil || loaded.ID != run.ID {
		t.Fatalf("load=%+v err=%v", loaded, err)
	}
	db, err := database.open(ctx)
	if err != nil {
		t.Fatal(err)
	}
	_, mutationErr := db.ExecContext(ctx, `UPDATE events SET body='changed'`)
	database.release(db)
	if mutationErr == nil {
		t.Fatal("reserved source mutation succeeded")
	}
	checkDB, openErr := sql.Open("sqlite", sqliteDSN(database.Path()))
	if openErr != nil {
		t.Fatal(openErr)
	}
	if stableErr := requireStaticSearchState(ctx, checkDB); stableErr == nil {
		t.Fatal("compaction accepted active segment migration")
	}
	_ = checkDB.Close()
	_, err = database.StartSegmentMigration(ctx, apptypes.SegmentMigrationStart{RunID: "other", StoreID: run.StoreID, ReservationID: run.ReservationID, PlanDigest: run.PlanDigest, SoftwareCommit: "test-commit", Range: run.Range, CandidateRoot: t.TempDir(), ArchiveRoot: t.TempDir(), CompressionFloor: 1, Budget: budget})
	if !errors.Is(err, apptypes.ErrSegmentMigrationConflict) {
		t.Fatalf("error=%v", err)
	}
}

func TestSegmentMigrationRecoversInstalledFileAfterIntentCrash(t *testing.T) {
	database, run, budget, _ := newMigrationTestRun(t, 1)
	ctx := context.Background()
	for run.Phase != domain.SegmentMigrationInstallIntent {
		var err error
		run, err = database.AdvanceSegmentMigration(ctx, run.ID, budget)
		if err != nil {
			t.Fatal(err)
		}
	}
	_, cfg, err := database.loadMigrationConfig(ctx, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(migrationRunDir(cfg.candidateRoot, run.ID), run.CandidateBasename)
	if err = copyPinnedNoReplace(source, cfg.archiveRoot, run.CandidateBasename, budget.MaxFileBytes, cfg.archiveDevice, cfg.archiveInode, nil, nil); err != nil {
		t.Fatal(err)
	}
	reopened := NewDatabase(database.Path(), preparedMigrations(t))
	recovered, err := reopened.RecoverSegmentMigration(ctx, run.ID, budget)
	if err != nil {
		t.Fatal(err)
	}
	if recovered.Phase != domain.SegmentMigrationInstalled {
		t.Fatalf("phase=%s", recovered.Phase)
	}
}

func TestSegmentMigrationRemovesOnlyOwnedPartialCandidateOnResume(t *testing.T) {
	database, run, budget, _ := newMigrationTestRun(t, 1)
	ctx := context.Background()
	var err error
	run, err = database.AdvanceSegmentMigration(ctx, run.ID, budget)
	if err != nil {
		t.Fatal(err)
	}
	run, err = database.AdvanceSegmentMigration(ctx, run.ID, budget)
	if err != nil {
		t.Fatal(err)
	}
	_, cfg, err := database.loadMigrationConfig(ctx, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	dir := migrationRunDir(cfg.candidateRoot, run.ID)
	if err = os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	partial := filepath.Join(dir, ".segment-v1-0123456789abcdef.candidate")
	if err = os.WriteFile(partial, []byte("interrupted"), 0o600); err != nil {
		t.Fatal(err)
	}
	run, err = database.AdvanceSegmentMigration(ctx, run.ID, budget)
	if err != nil {
		t.Fatal(err)
	}
	if run.Phase != domain.SegmentMigrationCandidateBuilt {
		t.Fatal(run.Phase)
	}
	if _, err = os.Stat(partial); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("partial remains: %v", err)
	}
}

func TestSegmentMigrationResumesAcrossProcessAtEveryDurableBoundary(t *testing.T) {
	database, run, budget, _ := newMigrationTestRun(t, 2)
	ctx := context.Background()
	for steps := 0; steps < 20 && run.Phase != domain.SegmentMigrationVerifiedShadow; steps++ {
		reopened := NewDatabase(database.Path(), preparedMigrations(t))
		var err error
		run, err = reopened.AdvanceSegmentMigration(ctx, run.ID, budget)
		if err != nil {
			t.Fatalf("step %d phase %s: %v", steps, run.Phase, err)
		}
	}
	if run.Phase != domain.SegmentMigrationVerifiedShadow {
		t.Fatalf("run=%+v", run)
	}
}

func TestSegmentMigrationKeepsConcurrentBackdatedAppendHot(t *testing.T) {
	database, run, budget, _ := newMigrationTestRun(t, 1)
	ctx := context.Background()
	raw, err := sql.Open("sqlite", sqliteDSN(database.Path()))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = raw.Close() }()
	if _, err = raw.Exec(`INSERT INTO events(id,kind,agent,session_id,body,created_at,client,workspace) VALUES('migration-late','note','a','s','body','2020-01-01T00:00:00Z','c','w')`); err != nil {
		t.Fatal(err)
	}
	for run.Phase != domain.SegmentMigrationVerifiedShadow {
		run, err = database.AdvanceSegmentMigration(ctx, run.ID, budget)
		if err != nil {
			t.Fatal(err)
		}
	}
	snapshot, err := database.CurrentCatalogRanges(ctx, catalogTestBudget())
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Ranges) != 2 || snapshot.Ranges[0].Placement != domain.CatalogPlacementVerifiedShadow || snapshot.Ranges[1].Placement != domain.CatalogPlacementHot || snapshot.Ranges[1].Range.Start != 2 {
		t.Fatalf("ranges=%+v", snapshot.Ranges)
	}
}

func TestSegmentMigrationEnforcesLockWALDiskAndStaleFileCaps(t *testing.T) {
	t.Run("lock deadline", func(t *testing.T) {
		database, run, budget, _ := newMigrationTestRun(t, 1)
		lease, err := acquireAdvisoryLease(context.Background(), database.Path()+".segment-migration.lock", true)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = lease.Close() }()
		budget.LockTime = 20 * time.Millisecond
		_, err = database.AdvanceSegmentMigration(context.Background(), run.ID, budget)
		if err == nil {
			t.Fatal("lock cap was ignored")
		}
	})
	t.Run("wal cap", func(t *testing.T) {
		database, run, budget, _ := newMigrationTestRun(t, 1)
		var err error
		run, err = database.AdvanceSegmentMigration(context.Background(), run.ID, budget)
		if err != nil {
			t.Fatal(err)
		}
		if err = os.WriteFile(database.Path()+"-wal", make([]byte, 1024), 0o600); err != nil {
			t.Fatal(err)
		}
		budget.MaxWALBytes = 1
		_, err = database.AdvanceSegmentMigration(context.Background(), run.ID, budget)
		if !errors.Is(err, ErrSegmentLimit) {
			t.Fatalf("error=%v", err)
		}
	})
	t.Run("disk reserve", func(t *testing.T) {
		database, run, budget, _ := newMigrationTestRun(t, 1)
		run, _ = database.AdvanceSegmentMigration(context.Background(), run.ID, budget)
		budget.MinFreeDiskBytes = 1 << 62
		_, err := database.AdvanceSegmentMigration(context.Background(), run.ID, budget)
		if !errors.Is(err, ErrSegmentLimit) {
			t.Fatalf("error=%v", err)
		}
	})
	t.Run("candidate file cap", func(t *testing.T) {
		database, run, budget, _ := newMigrationTestRun(t, 1)
		run, _ = database.AdvanceSegmentMigration(context.Background(), run.ID, budget)
		budget.MaxFileBytes = 1
		got, err := database.AdvanceSegmentMigration(context.Background(), run.ID, budget)
		if !errors.Is(err, ErrSegmentLimit) {
			t.Fatalf("error=%v", err)
		}
		if got.Revision != run.Revision || got.NextSequence != run.NextSequence {
			t.Fatalf("journal advanced: before=%+v after=%+v", run, got)
		}
	})
	t.Run("stale installed file", func(t *testing.T) {
		database, run, budget, archive := newMigrationTestRun(t, 1)
		ctx := context.Background()
		var err error
		for run.Phase != domain.SegmentMigrationInstalled {
			run, err = database.AdvanceSegmentMigration(ctx, run.ID, budget)
			if err != nil {
				t.Fatal(err)
			}
		}
		path := filepath.Join(archive, run.SegmentID)
		if err = os.Chmod(path, 0o600); err != nil {
			t.Fatal(err)
		}
		file, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = file.Write([]byte("damage"))
		_ = file.Close()
		run, err = database.AdvanceSegmentMigration(ctx, run.ID, budget)
		if err != nil {
			t.Fatal(err)
		}
		_, err = database.AdvanceSegmentMigration(ctx, run.ID, budget)
		if err == nil {
			t.Fatal("stale file was sealed")
		}
	})
}

func TestCopyPinnedNoReplaceRejectsCompetingDestination(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(t.TempDir(), "source")
	if err := os.WriteFile(source, []byte("candidate"), 0o600); err != nil {
		t.Fatal(err)
	}
	device, inode, err := segmentRootIdentity(root)
	if err != nil {
		t.Fatal(err)
	}
	finalName := "segment-v1-race.db"
	err = copyPinnedNoReplaceWithHook(source, root, finalName, 1024, device, inode, func(point string) error {
		if point == "before_install_rename" {
			return os.WriteFile(filepath.Join(root, finalName), []byte("competitor"), 0o400)
		}
		return nil
	}, nil)
	if err == nil {
		t.Fatal("competing destination was replaced")
	}
	content, readErr := os.ReadFile(filepath.Join(root, finalName))
	if readErr != nil {
		t.Fatal(readErr)
	}
	if string(content) != "competitor" {
		t.Fatalf("destination content = %q", content)
	}
}

func TestSegmentMigrationRollbackIsIdempotentFromEveryForwardPhase(t *testing.T) {
	phases := []domain.SegmentMigrationPhase{
		domain.SegmentMigrationPlanned,
		domain.SegmentMigrationCopying,
		domain.SegmentMigrationCandidateBuilt,
		domain.SegmentMigrationInstallIntent,
		domain.SegmentMigrationInstalled,
		domain.SegmentMigrationSealIntent,
		domain.SegmentMigrationSealed,
		domain.SegmentMigrationVerifyIntent,
		domain.SegmentMigrationVerifiedShadow,
	}
	for _, target := range phases {
		t.Run(string(target), func(t *testing.T) {
			database, run, budget, _ := newMigrationTestRun(t, 1)
			for run.Phase != target {
				var err error
				run, err = database.AdvanceSegmentMigration(context.Background(), run.ID, budget)
				if err != nil {
					t.Fatal(err)
				}
			}
			for run.Phase != domain.SegmentMigrationRolledBack {
				var err error
				run, err = database.AdvanceSegmentMigrationRollback(context.Background(), run.ID, budget)
				if err != nil {
					t.Fatal(err)
				}
			}
			again, err := database.AdvanceSegmentMigrationRollback(context.Background(), run.ID, budget)
			if err != nil || again.Revision != run.Revision {
				t.Fatalf("idempotent rollback = %+v, %v", again, err)
			}
			_, cfg, err := database.loadMigrationConfig(context.Background(), run.ID)
			if err != nil {
				t.Fatal(err)
			}
			if _, err = os.Stat(migrationRunDir(cfg.candidateRoot, run.ID)); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("owned candidate remains: %v", err)
			}
			proofDB, err := database.openReadOnly(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			var intents int
			err = proofDB.QueryRow(`SELECT count(*) FROM archive_segment_migration_install_intents WHERE run_id=?`, run.ID).Scan(&intents)
			database.release(proofDB)
			if err != nil || intents != 0 {
				t.Fatalf("install intents = %d, %v", intents, err)
			}
		})
	}
}

func TestSegmentMigrationResumesAfterDurabilityFaults(t *testing.T) {
	t.Run("candidate durable before journal", func(t *testing.T) {
		database, run, budget, _ := newMigrationTestRun(t, 1)
		run, _ = database.AdvanceSegmentMigration(context.Background(), run.ID, budget)
		failed := false
		database.segmentMigrationHook = func(point string) error {
			if point == "candidate_page_durable" && !failed {
				failed = true
				return errors.New("injected candidate checkpoint failure")
			}
			return nil
		}
		if _, err := database.AdvanceSegmentMigration(context.Background(), run.ID, budget); err == nil {
			t.Fatal("fault was not observed")
		}
		database.segmentMigrationHook = nil
		got := run
		var err error
		for got.Phase != domain.SegmentMigrationVerifiedShadow {
			got, err = database.AdvanceSegmentMigration(context.Background(), run.ID, budget)
			if err != nil {
				break
			}
		}
		if err != nil || got.Phase != domain.SegmentMigrationVerifiedShadow {
			t.Fatalf("resume = %s, %v", got.Phase, err)
		}
	})

	t.Run("rename before directory fsync", func(t *testing.T) {
		database, run, budget, _ := newMigrationTestRun(t, 1)
		for run.Phase != domain.SegmentMigrationInstallIntent {
			var err error
			run, err = database.AdvanceSegmentMigration(context.Background(), run.ID, budget)
			if err != nil {
				t.Fatal(err)
			}
		}
		failed := false
		database.segmentMigrationHook = func(point string) error {
			if point == "after_install_rename" && !failed {
				failed = true
				return errors.New("injected directory fsync failure")
			}
			return nil
		}
		if _, err := database.AdvanceSegmentMigration(context.Background(), run.ID, budget); err == nil {
			t.Fatal("fault was not observed")
		}
		database.segmentMigrationHook = nil
		got, err := database.AdvanceSegmentMigration(context.Background(), run.ID, budget)
		if err != nil || got.Phase != domain.SegmentMigrationInstalled {
			t.Fatalf("resume = %s, %v", got.Phase, err)
		}
	})
}

func TestSegmentMigrationReconcilesOwnedInstallingWithoutFollowingSymlink(t *testing.T) {
	t.Run("regular partial is replaced", func(t *testing.T) {
		database, run, budget, archive := newMigrationTestRun(t, 1)
		for run.Phase != domain.SegmentMigrationInstallIntent {
			var err error
			run, err = database.AdvanceSegmentMigration(context.Background(), run.ID, budget)
			if err != nil {
				t.Fatal(err)
			}
		}
		partial := filepath.Join(archive, "."+run.SegmentID+".installing")
		if err := os.WriteFile(partial, []byte("partial"), 0o600); err != nil {
			t.Fatal(err)
		}
		got, err := database.AdvanceSegmentMigration(context.Background(), run.ID, budget)
		if err != nil || got.Phase != domain.SegmentMigrationInstalled {
			t.Fatalf("resume = %s, %v", got.Phase, err)
		}
	})

	t.Run("foreign symlink fails closed", func(t *testing.T) {
		database, run, budget, archive := newMigrationTestRun(t, 1)
		for run.Phase != domain.SegmentMigrationInstallIntent {
			var err error
			run, err = database.AdvanceSegmentMigration(context.Background(), run.ID, budget)
			if err != nil {
				t.Fatal(err)
			}
		}
		foreign := filepath.Join(t.TempDir(), "foreign")
		if err := os.WriteFile(foreign, []byte("keep"), 0o600); err != nil {
			t.Fatal(err)
		}
		partial := filepath.Join(archive, "."+run.SegmentID+".installing")
		if err := os.Symlink(foreign, partial); err != nil {
			t.Fatal(err)
		}
		if _, err := database.AdvanceSegmentMigration(context.Background(), run.ID, budget); !errors.Is(err, apptypes.ErrSegmentMigrationOrientation) {
			t.Fatalf("error = %v", err)
		}
		content, err := os.ReadFile(foreign)
		if err != nil || string(content) != "keep" {
			t.Fatalf("foreign file changed: %q, %v", content, err)
		}
	})
}

func TestSegmentMigrationResumesCandidateFinalizationFromDurablePages(t *testing.T) {
	database, run, budget, _ := newMigrationTestRun(t, 2)
	for run.Phase != domain.SegmentMigrationCopying || run.NextSequence <= run.Range.End {
		var err error
		run, err = database.AdvanceSegmentMigration(context.Background(), run.ID, budget)
		if err != nil {
			t.Fatal(err)
		}
	}
	failed := false
	database.segmentMigrationHook = func(point string) error {
		if point == "candidate_final_reread" && !failed {
			failed = true
			return context.DeadlineExceeded
		}
		return nil
	}
	if _, err := database.AdvanceSegmentMigration(context.Background(), run.ID, budget); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("error = %v", err)
	}
	database.segmentMigrationHook = nil
	got, err := database.AdvanceSegmentMigration(context.Background(), run.ID, budget)
	if err != nil || got.Phase != domain.SegmentMigrationCandidateBuilt {
		t.Fatalf("resume = %s, %v", got.Phase, err)
	}
}

func TestSegmentMigrationRejectsInstalledInodeExchangeBeforeCatalogCommit(t *testing.T) {
	database, run, budget, archive := newMigrationTestRun(t, 1)
	for run.Phase != domain.SegmentMigrationSealIntent {
		var err error
		run, err = database.AdvanceSegmentMigration(context.Background(), run.ID, budget)
		if err != nil {
			t.Fatal(err)
		}
	}
	path := filepath.Join(archive, run.SegmentID)
	database.segmentMigrationHook = func(point string) error {
		if point != "before_seal_catalog_commit" {
			return nil
		}
		content, err := os.ReadFile(path)
		if err != nil {
			return fmt.Errorf("read installed segment: %w", err)
		}
		if err = os.Rename(path, path+".old"); err != nil {
			return fmt.Errorf("exchange installed segment: %w", err)
		}
		return os.WriteFile(path, content, 0o400)
	}
	_, err := database.AdvanceSegmentMigration(context.Background(), run.ID, budget)
	if !errors.Is(err, apptypes.ErrSegmentMigrationOrientation) {
		t.Fatalf("error = %v", err)
	}
	snapshot, err := database.CurrentCatalogRanges(context.Background(), catalogTestBudget())
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Ranges[0].Placement != domain.CatalogPlacementReserved {
		t.Fatalf("placement = %s", snapshot.Ranges[0].Placement)
	}
}

func TestSegmentMigrationRejectsStaleCandidateBeforeInstall(t *testing.T) {
	database, run, budget, _ := newMigrationTestRun(t, 1)
	for run.Phase != domain.SegmentMigrationCandidateBuilt {
		var err error
		run, err = database.AdvanceSegmentMigration(context.Background(), run.ID, budget)
		if err != nil {
			t.Fatal(err)
		}
	}
	_, cfg, err := database.loadMigrationConfig(context.Background(), run.ID)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(migrationRunDir(cfg.candidateRoot, run.ID), run.CandidateBasename)
	if err = os.Chmod(path, 0o600); err != nil {
		t.Fatal(err)
	}
	file, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = file.Write([]byte("stale"))
	_ = file.Close()
	run, err = database.AdvanceSegmentMigration(context.Background(), run.ID, budget)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = database.AdvanceSegmentMigration(context.Background(), run.ID, budget); !errors.Is(err, apptypes.ErrSegmentMigrationOrientation) {
		t.Fatalf("error = %v", err)
	}
}

func TestSegmentMigrationRejectsSymlinkedCandidateRunDirectory(t *testing.T) {
	database, run, budget, _ := newMigrationTestRun(t, 1)
	run, err := database.AdvanceSegmentMigration(context.Background(), run.ID, budget)
	if err != nil {
		t.Fatal(err)
	}
	_, cfg, err := database.loadMigrationConfig(context.Background(), run.ID)
	if err != nil {
		t.Fatal(err)
	}
	foreign := t.TempDir()
	marker := filepath.Join(foreign, "marker")
	if err = os.WriteFile(marker, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err = os.Symlink(foreign, migrationRunDir(cfg.candidateRoot, run.ID)); err != nil {
		t.Fatal(err)
	}
	if _, err = database.AdvanceSegmentMigration(context.Background(), run.ID, budget); !errors.Is(err, apptypes.ErrSegmentMigrationOrientation) {
		t.Fatalf("error = %v", err)
	}
	content, err := os.ReadFile(marker)
	if err != nil || string(content) != "keep" {
		t.Fatalf("foreign content = %q, %v", content, err)
	}
}

func TestSegmentMigrationRollbackRejectsArchiveRootExchange(t *testing.T) {
	database, run, budget, archive := newMigrationTestRun(t, 1)
	for run.Phase != domain.SegmentMigrationInstalled {
		var err error
		run, err = database.AdvanceSegmentMigration(context.Background(), run.ID, budget)
		if err != nil {
			t.Fatal(err)
		}
	}
	content, err := os.ReadFile(filepath.Join(archive, run.SegmentID))
	if err != nil {
		t.Fatal(err)
	}
	old := archive + "-old"
	if err = os.Rename(archive, old); err != nil {
		t.Fatal(err)
	}
	if err = os.Mkdir(archive, 0o700); err != nil {
		t.Fatal(err)
	}
	foreign := filepath.Join(archive, run.SegmentID)
	if err = os.WriteFile(foreign, content, 0o400); err != nil {
		t.Fatal(err)
	}
	run, err = database.AdvanceSegmentMigrationRollback(context.Background(), run.ID, budget)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = database.AdvanceSegmentMigrationRollback(context.Background(), run.ID, budget); !errors.Is(err, apptypes.ErrSegmentMigrationOrientation) {
		t.Fatalf("error = %v", err)
	}
	if _, err = os.Stat(foreign); err != nil {
		t.Fatalf("foreign segment removed: %v", err)
	}
}

func TestSegmentMigrationRejectsWritableInstalledSegmentBeforeCatalogCommit(t *testing.T) {
	database, run, budget, archive := newMigrationTestRun(t, 1)
	for run.Phase != domain.SegmentMigrationSealIntent {
		var err error
		run, err = database.AdvanceSegmentMigration(context.Background(), run.ID, budget)
		if err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Chmod(filepath.Join(archive, run.SegmentID), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := database.AdvanceSegmentMigration(context.Background(), run.ID, budget)
	if !errors.Is(err, apptypes.ErrSegmentMigrationOrientation) || got.Revision != run.Revision {
		t.Fatalf("got=%+v err=%v", got, err)
	}
	catalog, err := database.CurrentCatalogRanges(context.Background(), catalogTestBudget())
	if err != nil {
		t.Fatal(err)
	}
	if len(catalog.Ranges) != 1 || catalog.Ranges[0].Placement != domain.CatalogPlacementReserved {
		t.Fatalf("catalog ranges = %+v", catalog.Ranges)
	}
}

func TestSegmentMigrationRollbackDeleteFailureRollsBackAndResumes(t *testing.T) {
	database, run, budget, _ := newMigrationTestRun(t, 1)
	for run.Phase != domain.SegmentMigrationInstalled {
		var err error
		run, err = database.AdvanceSegmentMigration(context.Background(), run.ID, budget)
		if err != nil {
			t.Fatal(err)
		}
	}
	run, err := database.AdvanceSegmentMigrationRollback(context.Background(), run.ID, budget)
	if err != nil {
		t.Fatal(err)
	}
	failed := false
	database.segmentMigrationHook = func(point string) error {
		if point == "before_rollback_deletes" && !failed {
			failed = true
			return errors.New("injected delete failure")
		}
		return nil
	}
	if _, err = database.AdvanceSegmentMigrationRollback(context.Background(), run.ID, budget); err == nil {
		t.Fatal("delete failure was ignored")
	}
	database.segmentMigrationHook = nil
	loaded, err := database.LoadSegmentMigration(context.Background(), run.ID)
	if err != nil || loaded.Phase != domain.SegmentMigrationRollbackIntent {
		t.Fatalf("journal advanced: %s, %v", loaded.Phase, err)
	}
	proofDB, err := database.openReadOnly(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	var active int
	err = proofDB.QueryRow(`SELECT count(*) FROM archive_segment_migration_active WHERE run_id=?`, run.ID).Scan(&active)
	database.release(proofDB)
	if err != nil || active != 1 {
		t.Fatalf("active=%d, %v", active, err)
	}
	got, err := database.AdvanceSegmentMigrationRollback(context.Background(), run.ID, budget)
	if err != nil || got.Phase != domain.SegmentMigrationRolledBack {
		t.Fatalf("resume=%s, %v", got.Phase, err)
	}
}

func TestSegmentMigrationProofUsesHeldFileAcrossPathSwap(t *testing.T) {
	database, run, budget, archive := newMigrationTestRun(t, 1)
	for run.Phase != domain.SegmentMigrationSealIntent {
		var err error
		run, err = database.AdvanceSegmentMigration(context.Background(), run.ID, budget)
		if err != nil {
			t.Fatal(err)
		}
	}
	path := filepath.Join(archive, run.SegmentID)
	good := path + ".good"
	if err := os.Rename(path, good); err != nil {
		t.Fatal(err)
	}
	content, err := os.ReadFile(good)
	if err != nil {
		t.Fatal(err)
	}
	content[len(content)/2] ^= 0xff
	if err = os.WriteFile(path, content, 0o400); err != nil {
		t.Fatal(err)
	}
	database.segmentMigrationHook = func(point string) error {
		if point != "after_seal_installed_pin" {
			return nil
		}
		if err := os.Rename(path, path+".bad"); err != nil {
			return fmt.Errorf("hide bad segment: %w", err)
		}
		if err := os.Rename(good, path); err != nil {
			return fmt.Errorf("show good segment: %w", err)
		}
		return nil
	}
	if _, err = database.AdvanceSegmentMigration(context.Background(), run.ID, budget); err == nil {
		t.Fatal("path-swapped good file authenticated a different held inode")
	}
}

func TestSegmentMigrationProofRejectsArchiveRootExchangeAfterPin(t *testing.T) {
	database, run, budget, archive := newMigrationTestRun(t, 1)
	for run.Phase != domain.SegmentMigrationSealIntent {
		var err error
		run, err = database.AdvanceSegmentMigration(context.Background(), run.ID, budget)
		if err != nil {
			t.Fatal(err)
		}
	}
	database.segmentMigrationHook = func(point string) error {
		if point != "after_seal_installed_pin" {
			return nil
		}
		old := archive + ".old"
		if err := os.Rename(archive, old); err != nil {
			return fmt.Errorf("exchange archive root: %w", err)
		}
		if err := os.Mkdir(archive, 0o700); err != nil {
			return fmt.Errorf("replace archive root: %w", err)
		}
		return nil
	}
	if _, err := database.AdvanceSegmentMigration(context.Background(), run.ID, budget); !errors.Is(err, apptypes.ErrSegmentMigrationOrientation) {
		t.Fatalf("error = %v", err)
	}
}

func TestSegmentMigrationCandidateDescriptorExchangeGuards(t *testing.T) {
	t.Run("symlink exchange before candidate SQLite IO", func(t *testing.T) {
		database, run, budget, _ := newMigrationTestRun(t, 1)
		run, _ = database.AdvanceSegmentMigration(context.Background(), run.ID, budget)
		_, cfg, _ := database.loadMigrationConfig(context.Background(), run.ID)
		path := migrationCandidateWorkPath(migrationRunDir(cfg.candidateRoot, run.ID))
		if err := os.Mkdir(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, nil, 0o600); err != nil {
			t.Fatal(err)
		}
		foreign := filepath.Join(t.TempDir(), "foreign.sqlite")
		if err := os.WriteFile(foreign, []byte("keep"), 0o600); err != nil {
			t.Fatal(err)
		}
		database.segmentMigrationHook = func(point string) error {
			if point != "before_candidate_sqlite_io" {
				return nil
			}
			if err := os.Remove(path); err != nil {
				return fmt.Errorf("unlink candidate before exchange: %w", err)
			}
			if err := os.Symlink(foreign, path); err != nil {
				return fmt.Errorf("link foreign candidate: %w", err)
			}
			return nil
		}
		if _, err := database.AdvanceSegmentMigration(context.Background(), run.ID, budget); err != nil {
			t.Fatal(err)
		}
		content, err := os.ReadFile(foreign)
		if err != nil || string(content) != "keep" {
			t.Fatalf("foreign content=%q err=%v", content, err)
		}
		if info, err := os.Lstat(path); err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
			t.Fatalf("published candidate info=%v err=%v", info, err)
		}
	})

	t.Run("final builder exchange before SQLite IO", func(t *testing.T) {
		database, run, budget, _ := newMigrationTestRun(t, 1)
		for run.Phase != domain.SegmentMigrationCopying || run.NextSequence <= run.Range.End {
			var err error
			run, err = database.AdvanceSegmentMigration(context.Background(), run.ID, budget)
			if err != nil {
				t.Fatal(err)
			}
		}
		_, cfg, _ := database.loadMigrationConfig(context.Background(), run.ID)
		dir := migrationRunDir(cfg.candidateRoot, run.ID)
		foreign := filepath.Join(t.TempDir(), "foreign.sqlite")
		if err := os.WriteFile(foreign, []byte("keep"), 0o600); err != nil {
			t.Fatal(err)
		}
		database.segmentMigrationHook = func(point string) error {
			if point != "before_segment_candidate_sqlite_io" {
				return nil
			}
			entries, err := os.ReadDir(dir)
			if err != nil {
				return fmt.Errorf("read candidate build directory: %w", err)
			}
			for _, entry := range entries {
				if strings.HasSuffix(entry.Name(), ".candidate") {
					path := filepath.Join(dir, entry.Name())
					if err = os.Rename(path, path+".held"); err != nil {
						return fmt.Errorf("exchange final candidate: %w", err)
					}
					if err = os.Symlink(foreign, path); err != nil {
						return fmt.Errorf("link foreign final candidate: %w", err)
					}
					return nil
				}
			}
			return errors.New("candidate temp not found")
		}
		got, err := database.AdvanceSegmentMigration(context.Background(), run.ID, budget)
		if err == nil || got.Revision != run.Revision {
			t.Fatalf("got=%+v err=%v", got, err)
		}
		content, readErr := os.ReadFile(foreign)
		if readErr != nil || string(content) != "keep" {
			t.Fatalf("foreign content=%q err=%v", content, readErr)
		}
	})

	t.Run("candidate root before pin", func(t *testing.T) {
		database, run, budget, _ := newMigrationTestRun(t, 1)
		run, _ = database.AdvanceSegmentMigration(context.Background(), run.ID, budget)
		_, cfg, _ := database.loadMigrationConfig(context.Background(), run.ID)
		database.segmentMigrationHook = func(point string) error {
			if point != "before_candidate_root_pin" {
				return nil
			}
			if err := os.Rename(cfg.candidateRoot, cfg.candidateRoot+".old"); err != nil {
				return fmt.Errorf("exchange candidate root before pin: %w", err)
			}
			return os.Mkdir(cfg.candidateRoot, 0o700)
		}
		got, err := database.AdvanceSegmentMigration(context.Background(), run.ID, budget)
		if !errors.Is(err, apptypes.ErrSegmentMigrationOrientation) || got.Revision != run.Revision {
			t.Fatalf("got=%+v err=%v", got, err)
		}
	})

	t.Run("work file", func(t *testing.T) {
		database, run, budget, _ := newMigrationTestRun(t, 1)
		run, _ = database.AdvanceSegmentMigration(context.Background(), run.ID, budget)
		_, cfg, _ := database.loadMigrationConfig(context.Background(), run.ID)
		dir := migrationRunDir(cfg.candidateRoot, run.ID)
		database.segmentMigrationHook = func(point string) error {
			if point != "before_candidate_fd_verify" {
				return nil
			}
			path := migrationCandidateWorkPath(dir)
			if err := os.Rename(path, path+".old"); err != nil {
				return fmt.Errorf("exchange work file: %w", err)
			}
			return os.WriteFile(path, nil, 0o600)
		}
		got, err := database.AdvanceSegmentMigration(context.Background(), run.ID, budget)
		if !errors.Is(err, apptypes.ErrSegmentMigrationOrientation) || got.Revision != run.Revision {
			t.Fatalf("got=%+v err=%v", got, err)
		}
	})

	t.Run("stale serialized temp recovery", func(t *testing.T) {
		database, run, budget, _ := newMigrationTestRun(t, 1)
		run, _ = database.AdvanceSegmentMigration(context.Background(), run.ID, budget)
		_, cfg, _ := database.loadMigrationConfig(context.Background(), run.ID)
		path := migrationCandidateWorkPath(migrationRunDir(cfg.candidateRoot, run.ID))
		if err := os.Mkdir(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path+".serializing", []byte("crash-partial"), 0o600); err != nil {
			t.Fatal(err)
		}
		got, err := database.AdvanceSegmentMigration(context.Background(), run.ID, budget)
		if err != nil || got.Revision <= run.Revision {
			t.Fatalf("got=%+v err=%v", got, err)
		}
		if _, err = os.Lstat(path + ".serializing"); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("stale temp remains: %v", err)
		}
	})

	t.Run("serialized temp symlink exchange", func(t *testing.T) {
		database, run, budget, _ := newMigrationTestRun(t, 1)
		run, _ = database.AdvanceSegmentMigration(context.Background(), run.ID, budget)
		_, cfg, _ := database.loadMigrationConfig(context.Background(), run.ID)
		path := migrationCandidateWorkPath(migrationRunDir(cfg.candidateRoot, run.ID))
		if err := os.Mkdir(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, nil, 0o600); err != nil {
			t.Fatal(err)
		}
		before, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		foreign := filepath.Join(t.TempDir(), "foreign.sqlite")
		if err := os.WriteFile(foreign, []byte("keep"), 0o600); err != nil {
			t.Fatal(err)
		}
		database.segmentMigrationHook = func(point string) error {
			if point != "after_candidate_serialize_fsync" {
				return nil
			}
			temp := path + ".serializing"
			if err := os.Rename(temp, temp+".held"); err != nil {
				return fmt.Errorf("exchange serialized temp: %w", err)
			}
			if err := os.Symlink(foreign, temp); err != nil {
				return fmt.Errorf("link foreign serialized temp: %w", err)
			}
			return nil
		}
		got, err := database.AdvanceSegmentMigration(context.Background(), run.ID, budget)
		if !errors.Is(err, apptypes.ErrSegmentMigrationOrientation) || got.Revision != run.Revision {
			t.Fatalf("got=%+v err=%v", got, err)
		}
		content, readErr := os.ReadFile(foreign)
		if readErr != nil || string(content) != "keep" {
			t.Fatalf("foreign content=%q err=%v", content, readErr)
		}
		after, statErr := os.Stat(path)
		if statErr != nil || !os.SameFile(before, after) || after.Size() != 0 {
			t.Fatalf("work candidate changed: before=%v after=%v err=%v", before, after, statErr)
		}
	})

	t.Run("canonical bytes with retained digest", func(t *testing.T) {
		database, run, budget, _ := newMigrationTestRun(t, 1)
		run, _ = database.AdvanceSegmentMigration(context.Background(), run.ID, budget)
		_, cfg, _ := database.loadMigrationConfig(context.Background(), run.ID)
		path := migrationCandidateWorkPath(migrationRunDir(cfg.candidateRoot, run.ID))
		database.segmentMigrationHook = func(point string) error {
			if point != "before_candidate_fd_verify" {
				return nil
			}
			corrupt, err := sql.Open("sqlite", segmentSQLiteDSN(path, "rw", false))
			if err != nil {
				return fmt.Errorf("open candidate corruption connection: %w", err)
			}
			defer func() { _ = corrupt.Close() }()
			_, err = corrupt.Exec(`UPDATE candidate_units SET canonical_bytes=zeroblob(length(canonical_bytes))`)
			if err != nil {
				return fmt.Errorf("corrupt candidate bytes: %w", err)
			}
			return nil
		}
		got, err := database.AdvanceSegmentMigration(context.Background(), run.ID, budget)
		if !errors.Is(err, apptypes.ErrSegmentMigrationOrientation) || got.Revision != run.Revision {
			t.Fatalf("got=%+v err=%v", got, err)
		}
	})

	t.Run("oversized canonical bytes", func(t *testing.T) {
		database, run, budget, _ := newMigrationTestRun(t, 1)
		run, _ = database.AdvanceSegmentMigration(context.Background(), run.ID, budget)
		_, cfg, _ := database.loadMigrationConfig(context.Background(), run.ID)
		path := migrationCandidateWorkPath(migrationRunDir(cfg.candidateRoot, run.ID))
		database.segmentMigrationHook = func(point string) error {
			if point != "before_candidate_fd_verify" {
				return nil
			}
			corrupt, err := sql.Open("sqlite", segmentSQLiteDSN(path, "rw", false))
			if err != nil {
				return fmt.Errorf("open oversized candidate connection: %w", err)
			}
			defer func() { _ = corrupt.Close() }()
			_, err = corrupt.Exec(`UPDATE candidate_units SET canonical_bytes=zeroblob(?)`, budget.MaxValuePlainBytes+1)
			if err != nil {
				return fmt.Errorf("expand candidate bytes: %w", err)
			}
			return nil
		}
		got, err := database.AdvanceSegmentMigration(context.Background(), run.ID, budget)
		if !errors.Is(err, apptypes.ErrSegmentMigrationLimit) || got.Revision != run.Revision {
			t.Fatalf("got=%+v err=%v", got, err)
		}
	})

	t.Run("candidate root", func(t *testing.T) {
		database, run, budget, _ := newMigrationTestRun(t, 1)
		run, _ = database.AdvanceSegmentMigration(context.Background(), run.ID, budget)
		_, cfg, _ := database.loadMigrationConfig(context.Background(), run.ID)
		database.segmentMigrationHook = func(point string) error {
			if point != "candidate_page_durable" {
				return nil
			}
			if err := os.Rename(cfg.candidateRoot, cfg.candidateRoot+".old"); err != nil {
				return fmt.Errorf("exchange candidate root: %w", err)
			}
			return os.Mkdir(cfg.candidateRoot, 0o700)
		}
		got, err := database.AdvanceSegmentMigration(context.Background(), run.ID, budget)
		if !errors.Is(err, apptypes.ErrSegmentMigrationOrientation) || got.Revision != run.Revision {
			t.Fatalf("got=%+v err=%v", got, err)
		}
	})

	t.Run("build run directory", func(t *testing.T) {
		database, run, budget, _ := newMigrationTestRun(t, 1)
		for run.Phase != domain.SegmentMigrationCopying || run.NextSequence <= run.Range.End {
			var err error
			run, err = database.AdvanceSegmentMigration(context.Background(), run.ID, budget)
			if err != nil {
				t.Fatal(err)
			}
		}
		_, cfg, _ := database.loadMigrationConfig(context.Background(), run.ID)
		dir := migrationRunDir(cfg.candidateRoot, run.ID)
		database.segmentMigrationHook = func(point string) error {
			if point != "after_candidate_build" {
				return nil
			}
			if err := os.Rename(dir, dir+".old"); err != nil {
				return fmt.Errorf("exchange run directory: %w", err)
			}
			return os.Mkdir(dir, 0o700)
		}
		got, err := database.AdvanceSegmentMigration(context.Background(), run.ID, budget)
		if !errors.Is(err, apptypes.ErrSegmentMigrationOrientation) || got.Revision != run.Revision {
			t.Fatalf("got=%+v err=%v", got, err)
		}
	})
}

func TestSegmentMigrationRejectsCorruptRecoveredSealedCandidate(t *testing.T) {
	database, run, budget, _ := newMigrationTestRun(t, 1)
	for run.Phase != domain.SegmentMigrationCopying || run.NextSequence <= run.Range.End {
		var err error
		run, err = database.AdvanceSegmentMigration(context.Background(), run.ID, budget)
		if err != nil {
			t.Fatal(err)
		}
	}
	crash := errors.New("crash after candidate build")
	database.segmentMigrationHook = func(point string) error {
		if point == "after_candidate_build" {
			return crash
		}
		return nil
	}
	if got, err := database.AdvanceSegmentMigration(context.Background(), run.ID, budget); !errors.Is(err, crash) || got.Revision != run.Revision {
		t.Fatalf("got=%+v err=%v", got, err)
	}
	database.segmentMigrationHook = nil
	_, cfg, err := database.loadMigrationConfig(context.Background(), run.ID)
	if err != nil {
		t.Fatal(err)
	}
	dir := migrationRunDir(cfg.candidateRoot, run.ID)
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var sealed string
	for _, entry := range entries {
		if strings.HasSuffix(entry.Name(), ".sqlite") && entry.Name() != filepath.Base(migrationCandidateWorkPath(dir)) {
			sealed = filepath.Join(dir, entry.Name())
			break
		}
	}
	if sealed == "" {
		t.Fatal("sealed candidate not found")
	}
	if err = os.Chmod(sealed, 0o600); err != nil {
		t.Fatal(err)
	}
	corrupt, err := sql.Open("sqlite", segmentSQLiteDSN(sealed, "rw", false))
	if err != nil {
		t.Fatal(err)
	}
	_, err = corrupt.Exec(`UPDATE history_units SET payload=zeroblob(length(payload)) WHERE sequence=(SELECT min(sequence) FROM history_units)`)
	_ = corrupt.Close()
	if err != nil {
		t.Fatal(err)
	}
	if err = os.Chmod(sealed, 0o400); err != nil {
		t.Fatal(err)
	}
	got, err := database.AdvanceSegmentMigration(context.Background(), run.ID, budget)
	if err == nil || got.Revision != run.Revision {
		t.Fatalf("got=%+v err=%v", got, err)
	}
}
