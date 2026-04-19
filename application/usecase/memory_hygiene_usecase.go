package usecase

import (
	"context"

	apptypes "github.com/duck8823/traceary/application/types"
)

// MemoryHygieneUsecase surfaces suggestions for accepted durable memories
// that need attention — redaction patterns now mask their content, the
// memory has gone stale, or another accepted memory duplicates it. The
// usecase is read-only; actually applying a transition still goes through
// the durable-memory lifecycle methods on MemoryUsecase.
type MemoryHygieneUsecase interface {
	Scan(ctx context.Context, criteria apptypes.MemoryHygieneScanCriteria) (apptypes.MemoryHygieneScanResult, error)
}
