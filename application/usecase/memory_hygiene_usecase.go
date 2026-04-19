package usecase

import (
	"context"

	apptypes "github.com/duck8823/traceary/application/types"
)

// MemoryHygieneUsecase surfaces suggestions for accepted durable memories
// that need attention — redaction patterns now mask their content, the
// memory has gone stale, or another accepted memory duplicates it — and
// applies the matching lifecycle transitions when the operator commits
// a subset of those suggestions.
type MemoryHygieneUsecase interface {
	Scan(ctx context.Context, criteria apptypes.MemoryHygieneScanCriteria) (apptypes.MemoryHygieneScanResult, error)
	// Apply commits the lifecycle transition implied by each matching
	// suggestion: redaction_hit → supersede with the sanitized fact,
	// expiry_candidate → expire at the current time, duplicate → reject
	// the duplicate copy. Ids without a current suggestion are reported
	// under Failures so the caller can retry only the tail that actually
	// still needed work.
	Apply(ctx context.Context, criteria apptypes.MemoryHygieneApplyCriteria) (apptypes.MemoryHygieneApplyResult, error)
}
