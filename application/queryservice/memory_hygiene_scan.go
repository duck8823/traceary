package queryservice

import (
	"context"
	"errors"

	apptypes "github.com/duck8823/traceary/application/types"
)

// ErrMemoryHygieneRevisionChanged identifies a continuation whose store
// revision no longer matches. Callers must restart without the old cursor.
var ErrMemoryHygieneRevisionChanged = errors.New("memory hygiene revision changed")

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
