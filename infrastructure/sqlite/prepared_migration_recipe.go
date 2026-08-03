package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"sync"
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
}

type preparedMigrationMetrics struct {
	peakOwned, peakWAL uint64
	elapsed            time.Duration
}

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

func (r *PreparedMigrationCandidateRecipe) Build(ctx context.Context, request application.PreparedCandidateRequest) error {
	run := request.Run
	if run.PlanDigest == "" || run.Budget.WallTimeLimit <= 0 || run.Budget.OwnedDiskByteLimit == 0 || run.Budget.WALByteLimit == 0 {
		return errors.New("invalid prepared migration build budget")
	}
	buildCtx, cancel := context.WithTimeout(ctx, run.Budget.WallTimeLimit)
	defer cancel()
	started := time.Now()
	if err := exactFileClone(run.SourcePath, run.CandidatePath); err != nil {
		return fmt.Errorf("clone prepared migration source: %w", err)
	}
	db, err := sql.Open("sqlite", writableRehearsalDSN(run.CandidatePath, time.Second))
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
	if _, err = db.ExecContext(buildCtx, `PRAGMA wal_autocheckpoint=0`); err != nil {
		return err
	}
	var mode string
	if err = db.QueryRowContext(buildCtx, `PRAGMA journal_mode=WAL`).Scan(&mode); err != nil || mode != "wal" {
		return errors.New("cannot enable candidate WAL")
	}
	monitorCtx, stopMonitor := context.WithCancel(buildCtx)
	defer stopMonitor()
	monitorErr := make(chan error, 1)
	var peakOwned, peakWAL uint64
	go func() {
		ticker := time.NewTicker(25 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-monitorCtx.Done():
				monitorErr <- nil
				return
			case <-ticker.C:
				owned, wal, e := preparedOwnedBytes(run.CandidatePath)
				if e != nil {
					cancel()
					monitorErr <- e
					return
				}
				if owned > peakOwned {
					peakOwned = owned
				}
				if wal > peakWAL {
					peakWAL = wal
				}
				if owned > run.Budget.OwnedDiskByteLimit || wal > run.Budget.WALByteLimit {
					cancel()
					monitorErr <- errors.New("prepared migration resource cap exceeded")
					return
				}
			}
		}
	}()
	for _, migration := range plan.Pending {
		if err = executeExactMigration(buildCtx, db, exactMigrationFromPrepared(migration)); err != nil {
			stopMonitor()
			<-monitorErr
			return fmt.Errorf("apply prepared migration %d: %w", migration.Version, err)
		}
	}
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
	if owned > peakOwned {
		peakOwned = owned
	}
	if wal > peakWAL {
		peakWAL = wal
	}
	if owned > run.Budget.OwnedDiskByteLimit || wal > run.Budget.WALByteLimit {
		return errors.New("prepared migration resource cap exceeded at close")
	}
	r.mu.Lock()
	if r.metrics == nil {
		r.metrics = map[string]preparedMigrationMetrics{}
	}
	r.metrics[run.ID] = preparedMigrationMetrics{peakOwned: peakOwned, peakWAL: peakWAL, elapsed: time.Since(started)}
	r.mu.Unlock()
	return nil
}

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

func (r *PreparedMigrationCandidateRecipe) Sync(ctx context.Context, request application.PreparedCandidateRequest) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := rejectSQLiteSidecars(request.Run.CandidatePath); err != nil {
		return err
	}
	if err := syncFile(request.Run.CandidatePath); err != nil {
		return err
	}
	return syncDirectory(filepathDir(request.Run.CandidatePath))
}

func (r *PreparedMigrationCandidateRecipe) Verify(ctx context.Context, request application.PreparedCandidateRequest) (domain.PreparedCandidateEvidence, error) {
	evidence, err := r.Verifier.VerifyPair(ctx, request.Run.SourcePath, request.Run.CandidatePath, request.Run.PlanDigest)
	if err != nil {
		return evidence, err
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
