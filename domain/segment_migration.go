package domain

import (
	"errors"
	"strings"
)

var (
	// ErrSegmentMigrationInvalid rejects a malformed durable run revision.
	ErrSegmentMigrationInvalid = errors.New("segment migration run is invalid")
	// ErrSegmentMigrationTransition rejects an edge outside the #1651 protocol.
	ErrSegmentMigrationTransition = errors.New("segment migration transition is illegal")
)

// SegmentMigrationPhase is a durable, forward-only #1651 protocol boundary.
type SegmentMigrationPhase string

const (
	// SegmentMigrationPlanned is the initial durable phase.
	SegmentMigrationPlanned SegmentMigrationPhase = "planned"
	// SegmentMigrationCopying checkpoints frozen source pages.
	SegmentMigrationCopying SegmentMigrationPhase = "copying"
	// SegmentMigrationCandidateBuilt records a sealed owned candidate.
	SegmentMigrationCandidateBuilt SegmentMigrationPhase = "candidate_built"
	// SegmentMigrationInstallIntent durably precedes archive publication.
	SegmentMigrationInstallIntent SegmentMigrationPhase = "install_intent"
	// SegmentMigrationInstalled records the pinned-root publication result.
	SegmentMigrationInstalled SegmentMigrationPhase = "installed"
	// SegmentMigrationSealIntent durably precedes Catalog binding.
	SegmentMigrationSealIntent SegmentMigrationPhase = "seal_intent"
	// SegmentMigrationSealed records atomic binding and Reserved-to-Sealed proof.
	SegmentMigrationSealed SegmentMigrationPhase = "sealed"
	// SegmentMigrationVerifyIntent durably precedes parity verification.
	SegmentMigrationVerifyIntent SegmentMigrationPhase = "verify_intent"
	// SegmentMigrationVerifiedShadow is the #1651 success state without authority cutover.
	SegmentMigrationVerifiedShadow SegmentMigrationPhase = "verified_shadow"
	// SegmentMigrationRollbackIntent durably precedes a forward rollback.
	SegmentMigrationRollbackIntent SegmentMigrationPhase = "rollback_intent"
	// SegmentMigrationRolledBack restores Reserved while retaining bound files.
	SegmentMigrationRolledBack SegmentMigrationPhase = "rolled_back"
)

// SegmentMigrationRun authenticates one frozen target and its durable progress.
type SegmentMigrationRun struct {
	ID, StoreID, ReservationID, PlanDigest string
	Range                                  CatalogRange
	Phase                                  SegmentMigrationPhase
	Revision                               int64
	NextSequence, CopiedRows               int64
	CopiedPlainBytes                       int64
	SourceDigest                           string
	CandidateBasename, SegmentID           string
	ManifestDigest, FileDigest             string
	CatalogEpoch                           int64
}

// Validate enforces immutable identity and monotonic checkpoint invariants.
func (r SegmentMigrationRun) Validate() error {
	if strings.TrimSpace(r.ID) == "" || len(r.StoreID) != 32 || strings.TrimSpace(r.ReservationID) == "" ||
		!ValidCatalogDigest(r.PlanDigest) || r.Range.Start <= 0 || r.Range.End < r.Range.Start || r.Revision < 1 ||
		r.NextSequence < r.Range.Start || r.NextSequence > r.Range.End+1 || r.CopiedRows != r.NextSequence-r.Range.Start || r.CopiedPlainBytes < 0 {
		return ErrSegmentMigrationInvalid
	}
	switch r.Phase {
	case SegmentMigrationPlanned, SegmentMigrationCopying, SegmentMigrationCandidateBuilt, SegmentMigrationInstallIntent, SegmentMigrationInstalled, SegmentMigrationSealIntent, SegmentMigrationSealed, SegmentMigrationVerifyIntent, SegmentMigrationVerifiedShadow, SegmentMigrationRollbackIntent, SegmentMigrationRolledBack:
	default:
		return ErrSegmentMigrationInvalid
	}
	if r.Phase == SegmentMigrationCandidateBuilt || r.Phase == SegmentMigrationInstallIntent || r.Phase == SegmentMigrationInstalled || r.Phase == SegmentMigrationSealIntent || r.Phase == SegmentMigrationSealed || r.Phase == SegmentMigrationVerifyIntent || r.Phase == SegmentMigrationVerifiedShadow {
		if r.NextSequence != r.Range.End+1 || r.CandidateBasename == "" || r.SegmentID != r.CandidateBasename || !ValidCatalogDigest(r.SourceDigest) || !ValidCatalogDigest(r.ManifestDigest) || !ValidCatalogDigest(r.FileDigest) {
			return ErrSegmentMigrationInvalid
		}
	}
	if (r.Phase == SegmentMigrationSealed || r.Phase == SegmentMigrationVerifyIntent || r.Phase == SegmentMigrationVerifiedShadow) && r.CatalogEpoch <= 0 {
		return ErrSegmentMigrationInvalid
	}
	return nil
}

// CanTransition reports the only durable edges owned by #1651.
func (r SegmentMigrationRun) CanTransition(to SegmentMigrationPhase) bool {
	linear := map[SegmentMigrationPhase]SegmentMigrationPhase{
		SegmentMigrationPlanned: SegmentMigrationCopying, SegmentMigrationCopying: SegmentMigrationCandidateBuilt,
		SegmentMigrationCandidateBuilt: SegmentMigrationInstallIntent, SegmentMigrationInstallIntent: SegmentMigrationInstalled,
		SegmentMigrationInstalled: SegmentMigrationSealIntent, SegmentMigrationSealIntent: SegmentMigrationSealed,
		SegmentMigrationSealed: SegmentMigrationVerifyIntent, SegmentMigrationVerifyIntent: SegmentMigrationVerifiedShadow,
	}
	if linear[r.Phase] == to {
		return true
	}
	if to == SegmentMigrationRollbackIntent {
		return r.Phase == SegmentMigrationCopying || r.Phase == SegmentMigrationCandidateBuilt || r.Phase == SegmentMigrationInstallIntent || r.Phase == SegmentMigrationInstalled || r.Phase == SegmentMigrationSealIntent || r.Phase == SegmentMigrationSealed || r.Phase == SegmentMigrationVerifyIntent || r.Phase == SegmentMigrationVerifiedShadow
	}
	return r.Phase == SegmentMigrationRollbackIntent && to == SegmentMigrationRolledBack
}

// Advance returns a new append-only revision.
func (r SegmentMigrationRun) Advance(to SegmentMigrationPhase) (SegmentMigrationRun, error) {
	if err := r.Validate(); err != nil || !r.CanTransition(to) {
		return SegmentMigrationRun{}, ErrSegmentMigrationTransition
	}
	r.Phase, r.Revision = to, r.Revision+1
	return r, nil
}

// ValidateRevision authenticates one append-only journal edge, including the
// copying self-edge used by bounded page checkpoints.
func (r SegmentMigrationRun) ValidateRevision(next SegmentMigrationRun) error {
	if err := r.Validate(); err != nil {
		return err
	}
	if err := next.Validate(); err != nil {
		return err
	}
	if r.ID != next.ID || r.StoreID != next.StoreID || r.ReservationID != next.ReservationID || r.PlanDigest != next.PlanDigest || r.Range != next.Range || next.Revision != r.Revision+1 || next.NextSequence < r.NextSequence || next.CopiedRows < r.CopiedRows || next.CopiedPlainBytes < r.CopiedPlainBytes {
		return ErrSegmentMigrationTransition
	}
	if r.SourceDigest != "" && r.SourceDigest != next.SourceDigest || r.CandidateBasename != "" && r.CandidateBasename != next.CandidateBasename || r.SegmentID != "" && r.SegmentID != next.SegmentID || r.ManifestDigest != "" && r.ManifestDigest != next.ManifestDigest || r.FileDigest != "" && r.FileDigest != next.FileDigest || next.CatalogEpoch < r.CatalogEpoch {
		return ErrSegmentMigrationTransition
	}
	if r.Phase == SegmentMigrationCopying && next.Phase == SegmentMigrationCopying {
		if next.NextSequence <= r.NextSequence {
			return ErrSegmentMigrationTransition
		}
		return nil
	}
	if !r.CanTransition(next.Phase) {
		return ErrSegmentMigrationTransition
	}
	return nil
}
