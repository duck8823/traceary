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

// SegmentMigrationAction names one bounded application-selected workflow step.
type SegmentMigrationAction string

// SegmentMigrationRollbackAction names one bounded rollback workflow step.
type SegmentMigrationRollbackAction string

// Bounded forward actions selected from the run aggregate.
const (
	SegmentMigrationActionBeginCopy           SegmentMigrationAction = "begin_copy"
	SegmentMigrationActionCopyPage            SegmentMigrationAction = "copy_page"
	SegmentMigrationActionBuildCandidate      SegmentMigrationAction = "build_candidate"
	SegmentMigrationActionRecordInstallIntent SegmentMigrationAction = "record_install_intent"
	SegmentMigrationActionInstall             SegmentMigrationAction = "install"
	SegmentMigrationActionRecordSealIntent    SegmentMigrationAction = "record_seal_intent"
	SegmentMigrationActionSeal                SegmentMigrationAction = "seal"
	SegmentMigrationActionRecordVerifyIntent  SegmentMigrationAction = "record_verify_intent"
	SegmentMigrationActionVerify              SegmentMigrationAction = "verify"
)

const (
	// SegmentMigrationRollbackActionRecordIntent appends the forward rollback intent.
	SegmentMigrationRollbackActionRecordIntent SegmentMigrationRollbackAction = "record_rollback_intent"
	// SegmentMigrationRollbackActionComplete reconciles external state and commits terminal rollback.
	SegmentMigrationRollbackActionComplete SegmentMigrationRollbackAction = "complete_rollback"
)

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

// SegmentMigrationPageProof authenticates one durable candidate-page append.
type SegmentMigrationPageProof struct {
	NextSequence, Rows, PlainBytes int64
}

// SegmentMigrationCandidateProof binds the sealed candidate evidence.
type SegmentMigrationCandidateProof struct {
	SourceDigest, Basename, ManifestDigest, FileDigest string
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
		return r.Phase == SegmentMigrationPlanned || r.Phase == SegmentMigrationCopying || r.Phase == SegmentMigrationCandidateBuilt || r.Phase == SegmentMigrationInstallIntent || r.Phase == SegmentMigrationInstalled || r.Phase == SegmentMigrationSealIntent || r.Phase == SegmentMigrationSealed || r.Phase == SegmentMigrationVerifyIntent || r.Phase == SegmentMigrationVerifiedShadow
	}
	return r.Phase == SegmentMigrationRollbackIntent && to == SegmentMigrationRolledBack
}

// NextAction is the application-visible decision for one bounded forward step.
func (r SegmentMigrationRun) NextAction() (SegmentMigrationAction, bool) {
	switch r.Phase {
	case SegmentMigrationPlanned:
		return SegmentMigrationActionBeginCopy, true
	case SegmentMigrationCopying:
		if r.NextSequence <= r.Range.End {
			return SegmentMigrationActionCopyPage, true
		}
		return SegmentMigrationActionBuildCandidate, true
	case SegmentMigrationCandidateBuilt:
		return SegmentMigrationActionRecordInstallIntent, true
	case SegmentMigrationInstallIntent:
		return SegmentMigrationActionInstall, true
	case SegmentMigrationInstalled:
		return SegmentMigrationActionRecordSealIntent, true
	case SegmentMigrationSealIntent:
		return SegmentMigrationActionSeal, true
	case SegmentMigrationSealed:
		return SegmentMigrationActionRecordVerifyIntent, true
	case SegmentMigrationVerifyIntent:
		return SegmentMigrationActionVerify, true
	default:
		return "", false
	}
}

// NextRollbackAction is the application-visible rollback decision.
func (r SegmentMigrationRun) NextRollbackAction() (SegmentMigrationRollbackAction, bool) {
	if r.Phase == SegmentMigrationRolledBack {
		return "", false
	}
	if r.Phase == SegmentMigrationRollbackIntent {
		return SegmentMigrationRollbackActionComplete, true
	}
	if r.CanTransition(SegmentMigrationRollbackIntent) {
		return SegmentMigrationRollbackActionRecordIntent, true
	}
	return "", false
}

// Advance returns a new append-only revision.
func (r SegmentMigrationRun) Advance(to SegmentMigrationPhase) (SegmentMigrationRun, error) {
	if to == SegmentMigrationCandidateBuilt || to == SegmentMigrationSealed || to == SegmentMigrationVerifiedShadow {
		return SegmentMigrationRun{}, ErrSegmentMigrationTransition
	}
	if err := r.Validate(); err != nil || !r.CanTransition(to) {
		return SegmentMigrationRun{}, ErrSegmentMigrationTransition
	}
	r.Phase, r.Revision = to, r.Revision+1
	return r, nil
}

// CheckpointPage returns a valid copying revision after candidate durability.
func (r SegmentMigrationRun) CheckpointPage(proof SegmentMigrationPageProof) (SegmentMigrationRun, error) {
	if r.Phase != SegmentMigrationCopying || proof.NextSequence <= r.NextSequence || proof.NextSequence > r.Range.End+1 || proof.Rows != proof.NextSequence-r.Range.Start || proof.PlainBytes < r.CopiedPlainBytes {
		return SegmentMigrationRun{}, ErrSegmentMigrationTransition
	}
	r.Revision++
	r.NextSequence, r.CopiedRows, r.CopiedPlainBytes = proof.NextSequence, proof.Rows, proof.PlainBytes
	if err := r.Validate(); err != nil {
		return SegmentMigrationRun{}, ErrSegmentMigrationTransition
	}
	return r, nil
}

// RecordCandidateBuilt atomically attaches every candidate proof field.
func (r SegmentMigrationRun) RecordCandidateBuilt(proof SegmentMigrationCandidateProof) (SegmentMigrationRun, error) {
	if r.Phase != SegmentMigrationCopying || r.NextSequence != r.Range.End+1 {
		return SegmentMigrationRun{}, ErrSegmentMigrationTransition
	}
	r.Phase, r.Revision = SegmentMigrationCandidateBuilt, r.Revision+1
	r.SourceDigest, r.CandidateBasename, r.SegmentID = proof.SourceDigest, proof.Basename, proof.Basename
	r.ManifestDigest, r.FileDigest = proof.ManifestDigest, proof.FileDigest
	if err := r.Validate(); err != nil {
		return SegmentMigrationRun{}, ErrSegmentMigrationTransition
	}
	return r, nil
}

// RecordCatalogPhase attaches the committed Catalog epoch to an evidence edge.
func (r SegmentMigrationRun) RecordCatalogPhase(to SegmentMigrationPhase, epoch int64) (SegmentMigrationRun, error) {
	if epoch <= 0 || (to != SegmentMigrationSealed && to != SegmentMigrationVerifiedShadow) || !r.CanTransition(to) {
		return SegmentMigrationRun{}, ErrSegmentMigrationTransition
	}
	r.Phase, r.Revision, r.CatalogEpoch = to, r.Revision+1, epoch
	if err := r.Validate(); err != nil {
		return SegmentMigrationRun{}, ErrSegmentMigrationTransition
	}
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
