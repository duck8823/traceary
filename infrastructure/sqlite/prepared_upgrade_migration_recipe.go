package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/duck8823/traceary/application"
	"github.com/duck8823/traceary/domain"
)

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
	r.afterApply = func(buildCtx context.Context, db *sql.DB) error {
		recordPreparedUpgradeStep("vacuum")
		_, err := db.ExecContext(buildCtx, `VACUUM`)
		if err != nil {
			return fmt.Errorf("vacuum upgrade candidate: %w", err)
		}
		return nil
	}
	return r.PreparedMigrationCandidateRecipe.Build(ctx, request)
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
