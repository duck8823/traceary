package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/duck8823/traceary/application"
	"github.com/duck8823/traceary/domain"
)

// vacuumCandidateOutputPath is the run-owned VACUUM INTO sibling. The run ID
// makes it unique per run, so a same-run rebuild may drop and rewrite it.
func vacuumCandidateOutputPath(candidatePath, runID string) string {
	return candidatePath + ".vacuum-" + runID
}

// writeVacuumCandidateOutput compacts the candidate with VACUUM INTO a sibling
// file, instead of an in-place VACUUM.
//
// An in-place VACUUM rebuilds inside the candidate file, so its transient
// footprint (WAL/journal against the live candidate) is unbounded until it
// finishes; on a real-sized store that ends in a late SQLITE_FULL abort after
// the expensive copy + migration work (#2347). VACUUM INTO writes the
// compacted database to one bounded sibling file: peak stays at source +
// candidate + compacted output, and a failure leaves the pre-VACUUM candidate
// intact for retry instead of a half-rebuilt file.
//
// The sibling is copied back over the candidate with O_TRUNC instead of
// renamed: the prepared candidate inode must survive through the publication
// fences, and renaming under the open build handle would leave it pointed at
// the replaced inode (SQLITE_READONLY_DBMOVED) while its final checkpoint
// recreates the replaced file's -wal/-shm for Sync's sidecar refusal.
//
//nolint:wrapcheck // context and SQLite failures retain their original identity.
func writeVacuumCandidateOutput(ctx context.Context, db *sql.DB, candidatePath, runID string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if candidatePath == "" || runID == "" {
		return errors.New("vacuum upgrade candidate requires candidate path and run id")
	}
	// Fold cadence leftovers so VACUUM INTO reads settled pages.
	if _, err := db.ExecContext(ctx, `PRAGMA wal_checkpoint(TRUNCATE)`); err != nil {
		return fmt.Errorf("checkpoint candidate before vacuum: %w", err)
	}
	intoPath := vacuumCandidateOutputPath(candidatePath, runID)
	if err := os.Remove(intoPath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("clear stale vacuum upgrade output: %w", err)
	}
	//nolint:gosec // the INTO path is run-owned (same dir as the fenced
	// candidate); single-quote escaping is the SQLite string-literal rule.
	literal := "'" + strings.ReplaceAll(intoPath, "'", "''") + "'"
	if _, err := db.ExecContext(ctx, `VACUUM INTO `+literal); err != nil {
		_ = os.Remove(intoPath)
		return fmt.Errorf("vacuum upgrade candidate: %w", err)
	}
	if err := installVacuumCandidateOutput(intoPath, candidatePath); err != nil {
		_ = os.Remove(intoPath)
		return err
	}
	if err := os.Remove(intoPath); err != nil {
		return fmt.Errorf("remove installed vacuum upgrade output: %w", err)
	}
	if err := syncDirectory(filepathDir(candidatePath)); err != nil {
		return fmt.Errorf("sync directory after vacuum install: %w", err)
	}
	return nil
}

// installVacuumCandidateOutput copies the compacted sibling back over the
// candidate with O_TRUNC, so the prepared candidate inode survives. A torn
// copy fails closed: the candidate no longer verifies, recovery classifies it
// owned-incomplete, and the retry rebuilds from the untouched source.
func installVacuumCandidateOutput(intoPath, candidatePath string) error {
	in, err := os.Open(intoPath)
	if err != nil {
		return fmt.Errorf("open vacuum upgrade output: %w", err)
	}
	defer func() { _ = in.Close() }()
	out, err := os.OpenFile(candidatePath, os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return fmt.Errorf("open vacuum candidate destination: %w", err)
	}
	if _, err = io.Copy(out, in); err != nil {
		_ = out.Close()
		return fmt.Errorf("copy vacuum upgrade output: %w", err)
	}
	if err = out.Sync(); err != nil {
		_ = out.Close()
		return fmt.Errorf("sync vacuum candidate: %w", err)
	}
	if err = out.Close(); err != nil {
		return fmt.Errorf("close vacuum candidate: %w", err)
	}
	return nil
}

// discardStaleVacuumCandidateOutput drops a VACUUM INTO sibling that a
// crashed build left behind. The sibling is never load-bearing after Build:
// the candidate already holds the compacted bytes, so Sync only sweeps it.
//
//nolint:wrapcheck // context and filesystem failures retain their original identity.
func discardStaleVacuumCandidateOutput(ctx context.Context, candidatePath, runID string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if candidatePath == "" || runID == "" {
		return errors.New("discard vacuum upgrade output requires candidate path and run id")
	}
	if err := os.Remove(vacuumCandidateOutputPath(candidatePath, runID)); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove stale vacuum upgrade output: %w", err)
	}
	return nil
}

// preparedUpgradeStepRecorder is an optional ordered-event sink for tests that
// pin apply → VACUUM → checkpoint → sync → reopen RO → verify → fence → exchange.
var preparedUpgradeStepRecorder func(string)

// preparedUpgradeFailureHook injects copy/migration/verification failures.
var preparedUpgradeFailureHook func(string) error

func recordPreparedUpgradeStep(step string) {
	if preparedUpgradeStepRecorder != nil {
		preparedUpgradeStepRecorder(step)
	}
}

func invokePreparedUpgradeFailure(step string) error {
	if preparedUpgradeFailureHook == nil {
		return nil
	}
	return preparedUpgradeFailureHook(step)
}

// preparedVerifyOpenIsReadOnly is set by Verify when the verify handle rejects writes.
var preparedVerifyOpenIsReadOnly bool

// PreparedUpgradeMigrationRecipe extends PreparedMigrationCandidateRecipe with
// VACUUM, a read-only reopen before verify, and the two-layer conservation
// verifier. It contains no migration SQL and no second clone/checkpoint.
type PreparedUpgradeMigrationRecipe struct {
	PreparedMigrationCandidateRecipe
}

// Build clones the source, applies the catalog suffix, then VACUUMs.
//
//nolint:wrapcheck // delegates to the base recipe; afterApply errors keep their identity.
func (r *PreparedUpgradeMigrationRecipe) Build(ctx context.Context, request application.PreparedCandidateRequest) error {
	r.afterApply = func(buildCtx context.Context, db *sql.DB) (*sql.DB, error) {
		recordPreparedUpgradeStep("vacuum")
		if err := writeVacuumCandidateOutput(buildCtx, db, request.Run.CandidatePath, request.Run.ID); err != nil {
			return db, err
		}
		// The copy-back replaced the file under the open handle. Close it
		// and reopen, so Build's final checkpoint and close run against the
		// installed content instead of a stale pager.
		if err := db.Close(); err != nil {
			return db, fmt.Errorf("close pre-vacuum candidate handle: %w", err)
		}
		fresh, err := sql.Open("sqlite", writableCandidateDSN(request.Run.CandidatePath, time.Second))
		if err != nil {
			return db, err
		}
		fresh.SetMaxOpenConns(1)
		return fresh, nil
	}
	r.beforeApply = nil
	if err := r.bindDedupeArchiveRestore(ctx, request); err != nil {
		return err
	}
	if err := r.bindDecodePayloads(ctx, request); err != nil {
		return err
	}
	if err := r.bindArchiveSegmentsRefuse(ctx, request); err != nil {
		return err
	}
	if err := r.bindMemoryEdgesRefuse(ctx, request); err != nil {
		return err
	}
	if err := r.bindCompatSurfaceRefuse(ctx, request); err != nil {
		return err
	}
	return r.PreparedMigrationCandidateRecipe.Build(ctx, request)
}

func (r *PreparedUpgradeMigrationRecipe) bindArchiveSegmentsRefuse(ctx context.Context, request application.PreparedCandidateRequest) error {
	db, err := openDirectReadOnly(ctx, request.Run.SourcePath)
	if err != nil {
		return fmt.Errorf("open source to gate archive_segments drop: %w", err)
	}
	defer func() { _ = db.Close() }()
	plan, err := BuildPreparedMigrationPlan(ctx, db, r.Migrations)
	if err != nil {
		return err
	}
	if pendingDropsArchiveSegments(plan) {
		prev := r.beforeApply
		r.beforeApply = func(buildCtx context.Context, db *sql.DB) error {
			if prev != nil {
				if err := prev(buildCtx, db); err != nil {
					return err
				}
			}
			return refuseArchiveSegmentsIfNonEmpty(buildCtx, db)
		}
	}
	return nil
}

func (r *PreparedUpgradeMigrationRecipe) bindMemoryEdgesRefuse(ctx context.Context, request application.PreparedCandidateRequest) error {
	db, err := openDirectReadOnly(ctx, request.Run.SourcePath)
	if err != nil {
		return fmt.Errorf("open source to gate memory_edges drop: %w", err)
	}
	defer func() { _ = db.Close() }()
	plan, err := BuildPreparedMigrationPlan(ctx, db, r.Migrations)
	if err != nil {
		return err
	}
	if pendingDropsMemoryEdges(plan) {
		prev := r.beforeApply
		r.beforeApply = func(buildCtx context.Context, db *sql.DB) error {
			if prev != nil {
				if err := prev(buildCtx, db); err != nil {
					return err
				}
			}
			return refuseMemoryEdgesIfNonEmpty(buildCtx, db)
		}
	}
	return nil
}

func (r *PreparedUpgradeMigrationRecipe) bindCompatSurfaceRefuse(ctx context.Context, request application.PreparedCandidateRequest) error {
	db, err := openDirectReadOnly(ctx, request.Run.SourcePath)
	if err != nil {
		return fmt.Errorf("open source to gate legacy_source_hook drop: %w", err)
	}
	defer func() { _ = db.Close() }()
	plan, err := BuildPreparedMigrationPlan(ctx, db, r.Migrations)
	if err != nil {
		return err
	}
	if pendingDropsCompatSurface(plan) {
		prev := r.beforeApply
		r.beforeApply = func(buildCtx context.Context, db *sql.DB) error {
			if prev != nil {
				if err := prev(buildCtx, db); err != nil {
					return err
				}
			}
			return refuseLegacyHookIfNonNull(buildCtx, db)
		}
	}
	return nil
}

func (r *PreparedUpgradeMigrationRecipe) bindDecodePayloads(ctx context.Context, request application.PreparedCandidateRequest) error {
	db, err := openDirectReadOnly(ctx, request.Run.SourcePath)
	if err != nil {
		return fmt.Errorf("open source to gate payload decode: %w", err)
	}
	defer func() { _ = db.Close() }()
	plan, err := BuildPreparedMigrationPlan(ctx, db, r.Migrations)
	if err != nil {
		return err
	}
	if pendingHasDecodePayloads(plan) {
		prev := r.beforeApply
		r.beforeApply = func(buildCtx context.Context, db *sql.DB) error {
			if prev != nil {
				if err := prev(buildCtx, db); err != nil {
					return err
				}
			}
			return decodePayloadsForMigrationOrRefuse(buildCtx, db)
		}
	}
	return nil
}

func (r *PreparedUpgradeMigrationRecipe) bindDedupeArchiveRestore(ctx context.Context, request application.PreparedCandidateRequest) error {
	db, err := openDirectReadOnly(ctx, request.Run.SourcePath)
	if err != nil {
		return fmt.Errorf("open source to gate archive restore: %w", err)
	}
	defer func() { _ = db.Close() }()
	plan, err := BuildPreparedMigrationPlan(ctx, db, r.Migrations)
	if err != nil {
		return err
	}
	if pendingHasRestoreDedupeArchive(plan) {
		r.beforeApply = restoreDedupeArchiveOrRefuse
	}
	return nil
}

// Sync sweeps a VACUUM INTO sibling that a crashed build may have left
// behind, then syncs. The sibling is never load-bearing after Build.
//
//nolint:wrapcheck // sweep and sync failures keep their original identity.
func (r *PreparedUpgradeMigrationRecipe) Sync(ctx context.Context, request application.PreparedCandidateRequest) error {
	if err := discardStaleVacuumCandidateOutput(ctx, request.Run.CandidatePath, request.Run.ID); err != nil {
		return err
	}
	return r.PreparedMigrationCandidateRecipe.Sync(ctx, request)
}

// Verify reopens the candidate read-only and runs the two-layer verifier.
//
//nolint:wrapcheck // test hooks and verifier failures keep their original identity.
func (r *PreparedUpgradeMigrationRecipe) Verify(ctx context.Context, request application.PreparedCandidateRequest) (domain.PreparedCandidateEvidence, error) {
	if err := invokePreparedUpgradeFailure("verification"); err != nil {
		return domain.PreparedCandidateEvidence{}, err
	}
	recordPreparedUpgradeStep("reopen_ro")
	candidateDB, err := openDirectReadOnly(ctx, request.Run.CandidatePath)
	if err != nil {
		return domain.PreparedCandidateEvidence{}, fmt.Errorf("reopen upgrade candidate read-only: %w", err)
	}
	_, writeErr := candidateDB.ExecContext(ctx, `PRAGMA user_version=1`)
	_ = candidateDB.Close()
	if writeErr == nil {
		return domain.PreparedCandidateEvidence{}, errors.New("upgrade verify handle is writable")
	}
	preparedVerifyOpenIsReadOnly = true
	recordPreparedUpgradeStep("verify")
	evidence, err := r.Verifier.VerifyUpgradePair(ctx, request.Run.SourcePath, request.Run.CandidatePath, request.Run.PlanDigest)
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
