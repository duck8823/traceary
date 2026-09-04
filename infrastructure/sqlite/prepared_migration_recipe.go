package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"github.com/duck8823/traceary/application"
	"github.com/duck8823/traceary/domain"
)

// PreparedMigrationCandidateRecipe builds the fixed migration suffix on a
// run-owned inode. It never writes the bound source.
type PreparedMigrationCandidateRecipe struct {
	Migrations fs.FS
	Verifier   PreparedMigrationVerifier
	mu         sync.Mutex
	metrics    map[string]preparedMigrationMetrics
	// beforeApply runs after clone + WAL setup, before the pending-suffix
	// apply loop. Nil unless the plan's pending set includes a migration
	// that needs candidate-time data work (v81 restore is the first).
	beforeApply func(ctx context.Context, db *sql.DB) error
	// afterApply runs after the pending suffix is applied and before
	// wal_checkpoint(TRUNCATE). The upgrade recipe uses it for VACUUM.
	afterApply func(ctx context.Context, db *sql.DB) error
}

type preparedMigrationMetrics struct {
	peakOwned, peakWAL uint64
	elapsed            time.Duration
}

// Plan fixes the exact pending migration suffix digest.
func (r *PreparedMigrationCandidateRecipe) Plan(ctx context.Context, request application.PreparedCandidateRequest) (string, error) {
	db, err := openDirectReadOnly(ctx, request.Run.SourcePath)
	if err != nil {
		return "", fmt.Errorf("open prepared migration source: %w", err)
	}
	defer func() { _ = db.Close() }()
	plan, err := BuildPreparedMigrationPlan(ctx, db, r.Migrations)
	if err != nil {
		return "", err
	}
	if !plan.Offline || len(plan.Pending) == 0 {
		return "", errors.New("prepared migration requires an offline pending suffix")
	}
	return plan.Digest, nil
}

// Build clones and migrates only the run-owned offline candidate.
//
//nolint:wrapcheck // low-level I/O failures are classified by the upgrade use case.
func (r *PreparedMigrationCandidateRecipe) Build(ctx context.Context, request application.PreparedCandidateRequest) error {
	run := request.Run
	if run.PlanDigest == "" || run.SourceDigest == "" || run.Budget.WallTimeLimit <= 0 || run.Budget.OwnedDiskByteLimit == 0 || run.Budget.WALByteLimit == 0 || run.Budget.TemporaryByteLimit == 0 {
		return errors.New("invalid prepared migration build budget")
	}
	buildCtx, cancel := context.WithTimeout(ctx, run.Budget.WallTimeLimit)
	defer cancel()
	started := time.Now()
	if err := invokePreparedUpgradeFailure("copy"); err != nil {
		return err
	}
	recordPreparedUpgradeStep("clone")
	if err := exactFileClone(run.SourcePath, run.CandidatePath); err != nil {
		return fmt.Errorf("clone prepared migration source: %w", err)
	}
	db, err := sql.Open("sqlite", writableCandidateDSN(run.CandidatePath, time.Second))
	if err != nil {
		return err
	}
	db.SetMaxOpenConns(1)
	defer func() { _ = db.Close() }()
	plan, err := BuildPreparedMigrationPlan(buildCtx, db, r.Migrations)
	if err != nil {
		return err
	}
	if plan.Digest != run.PlanDigest || !plan.Offline {
		return errors.New("prepared migration plan changed after clone")
	}
	if _, err = db.ExecContext(buildCtx, `PRAGMA wal_autocheckpoint=0; PRAGMA synchronous=FULL; PRAGMA temp_store=MEMORY; PRAGMA cache_size=-2048`); err != nil {
		return err
	}
	var mode string
	if err = db.QueryRowContext(buildCtx, `PRAGMA journal_mode=WAL`).Scan(&mode); err != nil || mode != "wal" {
		return errors.New("cannot enable candidate WAL")
	}
	monitorCtx, stopMonitor := context.WithCancel(buildCtx)
	defer stopMonitor()
	monitorErr := make(chan error, 1)
	var peakOwned, peakWAL atomic.Uint64
	recordPeak := func(value uint64, peak *atomic.Uint64) {
		for {
			old := peak.Load()
			if value <= old || peak.CompareAndSwap(old, value) {
				return
			}
		}
	}
	measure := func() error {
		owned, wal, e := preparedOwnedBytes(run.CandidatePath)
		if e != nil {
			return e
		}
		recordPeak(owned, &peakOwned)
		recordPeak(wal, &peakWAL)
		if owned > run.Budget.OwnedDiskByteLimit || wal > run.Budget.WALByteLimit {
			return errors.New("prepared migration resource cap exceeded")
		}
		return nil
	}
	go func() {
		ticker := time.NewTicker(25 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-monitorCtx.Done():
				monitorErr <- nil
				return
			case <-ticker.C:
				if e := measure(); e != nil {
					cancel()
					monitorErr <- e
					return
				}
			}
		}
	}()
	if r.beforeApply != nil {
		if err = r.beforeApply(buildCtx, db); err != nil {
			stopMonitor()
			<-monitorErr
			return err
		}
	}
	for _, migration := range plan.Pending {
		if err = invokePreparedUpgradeFailure("migration"); err != nil {
			stopMonitor()
			<-monitorErr
			return err
		}
		recordPreparedUpgradeStep("apply")
		if err = executeExactMigration(buildCtx, db, exactMigrationFromPrepared(migration)); err != nil {
			stopMonitor()
			<-monitorErr
			return fmt.Errorf("apply prepared migration %d: %w", migration.Version, err)
		}
		if err = measure(); err != nil {
			stopMonitor()
			<-monitorErr
			return err
		}
	}
	if r.afterApply != nil {
		if err = r.afterApply(buildCtx, db); err != nil {
			stopMonitor()
			<-monitorErr
			return err
		}
	}
	recordPreparedUpgradeStep("checkpoint")
	if _, err = db.ExecContext(buildCtx, `PRAGMA wal_checkpoint(TRUNCATE)`); err != nil {
		stopMonitor()
		<-monitorErr
		return fmt.Errorf("checkpoint prepared migration: %w", err)
	}
	if err = db.Close(); err != nil {
		stopMonitor()
		<-monitorErr
		return err
	}
	stopMonitor()
	if monitorFailure := <-monitorErr; monitorFailure != nil {
		return monitorFailure
	}
	owned, wal, err := preparedOwnedBytes(run.CandidatePath)
	if err != nil {
		return err
	}
	recordPeak(owned, &peakOwned)
	recordPeak(wal, &peakWAL)
	if owned > run.Budget.OwnedDiskByteLimit || wal > run.Budget.WALByteLimit {
		return errors.New("prepared migration resource cap exceeded at close")
	}
	r.mu.Lock()
	if r.metrics == nil {
		r.metrics = map[string]preparedMigrationMetrics{}
	}
	r.metrics[run.ID] = preparedMigrationMetrics{peakOwned: peakOwned.Load(), peakWAL: peakWAL.Load(), elapsed: time.Since(started)}
	r.mu.Unlock()
	return nil
}

//nolint:wrapcheck // callers classify filesystem observation failures uniformly.
func preparedOwnedBytes(path string) (uint64, uint64, error) {
	var total, wal uint64
	for _, suffix := range []string{"", "-wal", "-shm"} {
		info, err := os.Lstat(path + suffix)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return 0, 0, err
		}
		if !info.Mode().IsRegular() {
			return 0, 0, errors.New("prepared candidate component is not regular")
		}
		size := uint64(info.Size())
		total += size
		if suffix == "-wal" {
			wal = size
		}
	}
	return total, wal, nil
}

// ClassifyOwnedCandidate determines whether recovery may reuse an owned candidate.
//
//nolint:wrapcheck // filesystem failures must remain distinguishable during recovery.
func (r *PreparedMigrationCandidateRecipe) ClassifyOwnedCandidate(ctx context.Context, inspection application.PreparedCandidateInspection) (domain.CandidateCondition, error) {
	db, err := openDirectReadOnly(ctx, inspection.Run.CandidatePath)
	if err != nil {
		return domain.CandidateConditionOwnedIncomplete, nil
	}
	defer func() { _ = db.Close() }()
	plan, err := BuildPreparedMigrationPlan(ctx, db, r.Migrations)
	if err != nil {
		return domain.CandidateConditionOwnedIncomplete, nil
	}
	if plan.Current == plan.Latest && len(plan.Pending) == 0 {
		return domain.CandidateConditionComplete, nil
	}
	return domain.CandidateConditionOwnedIncomplete, nil
}

// Sync checkpoints and fsyncs the prepared candidate before publication.
//
//nolint:wrapcheck // context and SQLite failures retain their original identity.
func (r *PreparedMigrationCandidateRecipe) Sync(ctx context.Context, request application.PreparedCandidateRequest) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := rejectSQLiteSidecars(request.Run.CandidatePath); err != nil {
		return err
	}
	recordPreparedUpgradeStep("sync")
	if err := syncFile(request.Run.CandidatePath); err != nil {
		return err
	}
	return syncDirectory(filepathDir(request.Run.CandidatePath))
}

// Verify produces body-free logical and resource evidence for publication.
func (r *PreparedMigrationCandidateRecipe) Verify(ctx context.Context, request application.PreparedCandidateRequest) (domain.PreparedCandidateEvidence, error) {
	evidence, err := r.Verifier.VerifyPair(ctx, request.Run.SourcePath, request.Run.CandidatePath, request.Run.PlanDigest)
	if err != nil {
		return evidence, err
	}
	if evidence.SourceDigest != request.Run.SourceDigest {
		return domain.PreparedCandidateEvidence{}, errors.New("prepared migration source digest changed")
	}
	r.mu.Lock()
	metrics, ok := r.metrics[request.Run.ID]
	r.mu.Unlock()
	if ok {
		evidence.PeakOwnedBytes = metrics.peakOwned
		evidence.PeakWALBytes = metrics.peakWAL
		evidence.BuildMilliseconds = metrics.elapsed.Milliseconds()
	}
	return evidence, nil
}
