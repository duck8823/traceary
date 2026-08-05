package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	apptypes "github.com/duck8823/traceary/application/types"
	"github.com/duck8823/traceary/domain"
)

func migrationTestBudget() apptypes.SegmentMigrationBudget {
	return apptypes.SegmentMigrationBudget{PageRows: 1, MaxSteps: 32, WallTime: time.Minute, LockTime: time.Second, MaxPlainBytes: 1 << 20, MaxStoredBytes: 1 << 20, MaxValuePlainBytes: 1 << 20, MaxValueStoredBytes: 1 << 20, MaxFileBytes: 2 << 20, MaxSummaryBytes: 1 << 20, MaxSummaryRows: 1000, MaxWALBytes: 16 << 20, MinFreeDiskBytes: 1}
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
	run, err := database.StartSegmentMigration(ctx, apptypes.SegmentMigrationStart{RunID: "migration-run", StoreID: storeID, ReservationID: plan.ReservationID, PlanDigest: plan.PlanDigest, Range: plan.Range, CandidateRoot: candidate, ArchiveRoot: archive, CompressionFloor: 32, Budget: budget})
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
	_, err = database.StartSegmentMigration(ctx, apptypes.SegmentMigrationStart{RunID: "other", StoreID: run.StoreID, ReservationID: run.ReservationID, PlanDigest: run.PlanDigest, Range: run.Range, CandidateRoot: t.TempDir(), ArchiveRoot: t.TempDir(), CompressionFloor: 1, Budget: budget})
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
	if err = copyPinnedNoReplace(source, cfg.archiveRoot, run.CandidateBasename, budget.MaxFileBytes, cfg.archiveDevice, cfg.archiveInode); err != nil {
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
	err = copyPinnedNoReplaceWithHook(source, root, finalName, 1024, device, inode, func() error {
		return os.WriteFile(filepath.Join(root, finalName), []byte("competitor"), 0o400)
	})
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
