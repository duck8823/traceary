package types

import (
	"errors"
	"time"

	"github.com/duck8823/traceary/domain"
)

var (
	// ErrSegmentMigrationConflict rejects a second active or contradictory run.
	ErrSegmentMigrationConflict = errors.New("segment migration already active")
	// ErrSegmentMigrationNotFound identifies an unknown run.
	ErrSegmentMigrationNotFound = errors.New("segment migration not found")
	// ErrSegmentMigrationStaleSource rejects source evidence that differs from the frozen plan.
	ErrSegmentMigrationStaleSource = errors.New("segment migration source proof mismatch")
	// ErrSegmentMigrationIncomplete requires a bounded resume.
	ErrSegmentMigrationIncomplete = errors.New("segment migration operation incomplete")
	// ErrSegmentMigrationOrientation rejects contradictory candidate/archive files.
	ErrSegmentMigrationOrientation = errors.New("segment migration file orientation is invalid")
	// ErrSegmentMigrationLimit reports a fixed migration resource cap.
	ErrSegmentMigrationLimit = errors.New("segment migration resource limit exceeded")
	// ErrSegmentMigrationEnvelopeExpansion rejects a resume or recovery budget
	// that expands any resource authorized at start.
	ErrSegmentMigrationEnvelopeExpansion = errors.New("segment migration resource envelope expansion")
)

const (
	// SegmentMigrationMaxPageRows is the hard per-checkpoint hydration cap.
	SegmentMigrationMaxPageRows = 1000
	// SegmentMigrationMaxSteps is the hard application resume cap.
	SegmentMigrationMaxSteps = 64
)

// SegmentMigrationBudget fixes every resource bound used by one resume.
type SegmentMigrationBudget struct {
	PageRows, MaxSteps                      int
	WallTime, LockTime                      time.Duration
	MaxPlainBytes, MaxStoredBytes           int64
	MaxValuePlainBytes, MaxValueStoredBytes int64
	MaxFileBytes, MaxSummaryBytes           int64
	MaxWALBytes, MinFreeDiskBytes           int64
	MaxSummaryRows                          uint64
}

// Valid reports whether every independent operation cap is positive and bounded.
func (b SegmentMigrationBudget) Valid() bool {
	return b.PageRows > 0 && b.PageRows <= SegmentMigrationMaxPageRows && b.MaxSteps > 0 && b.MaxSteps <= SegmentMigrationMaxSteps &&
		b.WallTime > 0 && b.WallTime <= 2*time.Minute && b.LockTime > 0 && b.LockTime <= 30*time.Second &&
		b.MaxPlainBytes > 0 && b.MaxStoredBytes > 0 && b.MaxValuePlainBytes > 0 && b.MaxValueStoredBytes > 0 && b.MaxFileBytes > 0 && b.MaxSummaryBytes > 0 && b.MaxWALBytes > 0 && b.MinFreeDiskBytes > 0 && b.MaxSummaryRows > 0
}

// Tightens reports whether b stays within the immutable start envelope.
// MinFreeDiskBytes is a safety floor, so increasing it is a tightening.
func (b SegmentMigrationBudget) Tightens(envelope SegmentMigrationBudget) bool {
	return b.Valid() && envelope.Valid() &&
		b.PageRows <= envelope.PageRows && b.MaxSteps <= envelope.MaxSteps &&
		b.WallTime <= envelope.WallTime && b.LockTime <= envelope.LockTime &&
		b.MaxPlainBytes <= envelope.MaxPlainBytes && b.MaxStoredBytes <= envelope.MaxStoredBytes &&
		b.MaxValuePlainBytes <= envelope.MaxValuePlainBytes && b.MaxValueStoredBytes <= envelope.MaxValueStoredBytes &&
		b.MaxFileBytes <= envelope.MaxFileBytes && b.MaxSummaryBytes <= envelope.MaxSummaryBytes &&
		b.MaxWALBytes <= envelope.MaxWALBytes && b.MinFreeDiskBytes >= envelope.MinFreeDiskBytes &&
		b.MaxSummaryRows <= envelope.MaxSummaryRows
}

// SegmentMigrationStart binds only a pre-existing immutable target plan.
type SegmentMigrationStart struct {
	RunID, StoreID, ReservationID, PlanDigest string
	SoftwareCommit                            string
	Range                                     domain.CatalogRange
	CandidateRoot, ArchiveRoot                string
	CompressionFloor                          int
	Budget                                    SegmentMigrationBudget
}
