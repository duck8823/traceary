package queryservice

import (
	"context"
	"errors"
	"fmt"

	apptypes "github.com/duck8823/traceary/application/types"
)

// ErrMemoryHygieneRevisionChanged identifies a continuation whose store
// revision no longer matches. Read-only scans may preserve their keyset in an
// explicit best-effort continuation; apply callers must fail closed.
var ErrMemoryHygieneRevisionChanged = errors.New("memory hygiene revision changed")

// MemoryHygieneRevisionChangedError carries the revision a best-effort scan
// continuation must bind before it resumes at the retained keyset.
type MemoryHygieneRevisionChangedError struct {
	CurrentRevision int64
}

// Error implements error without exposing any memory content.
func (e *MemoryHygieneRevisionChangedError) Error() string {
	return fmt.Sprintf("%s: current revision %d", ErrMemoryHygieneRevisionChanged, e.CurrentRevision)
}

// Unwrap preserves errors.Is compatibility for strict apply callers.
func (e *MemoryHygieneRevisionChangedError) Unwrap() error {
	return ErrMemoryHygieneRevisionChanged
}

// NewMemoryHygieneRevisionChangedError builds the typed scan-source mismatch.
func NewMemoryHygieneRevisionChangedError(currentRevision int64) error {
	return &MemoryHygieneRevisionChangedError{CurrentRevision: currentRevision}
}

// ErrMemoryHygieneRescanRequired identifies a cursor that cannot be
// authenticated by this process. Retrying it cannot succeed; callers must
// start a new scan without the cursor.
var ErrMemoryHygieneRescanRequired = errors.New("memory hygiene rescan required")

// ErrMemoryHygieneContinuationCannotProgress identifies a finite scan budget
// that stopped before its cursor position advanced. Retrying that cursor with
// the same budget would repeat the same work indefinitely, so callers must
// increase the relevant budget or narrow the scan instead.
var ErrMemoryHygieneContinuationCannotProgress = errors.New("memory hygiene continuation cannot progress")

// MemoryHygieneScanSource is the narrow read boundary consumed by the bounded
// hygiene use case. Generic List cannot express a revision-stable keyset or
// exhaustive pair continuation, so production scanners must implement this
// source explicitly.
type MemoryHygieneScanSource interface {
	// ScanMemoryHygienePage returns one revision-consistent bounded page.
	ScanMemoryHygienePage(ctx context.Context, criteria apptypes.MemoryHygieneScanPageCriteria) (apptypes.MemoryHygieneScanSourcePage, error)
	// RevalidateMemoryHygiene loads only a requested target and its relevant
	// peers for apply. Complete=false means the apply path must fail closed.
	RevalidateMemoryHygiene(ctx context.Context, criteria apptypes.MemoryHygieneRevalidationCriteria) (apptypes.MemoryHygieneRevalidationSourceResult, error)
}
