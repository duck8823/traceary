package usecase

import (
	"context"

	apptypes "github.com/duck8823/traceary/application/types"
)

// MemoryHygieneUsecase is a legacy adapter interface for hygiene behavior now
// exposed by MemoryUsecase.
//
// Deprecated: use MemoryUsecase.Scan and MemoryUsecase.Apply instead. This
// shim remains until DI is collapsed in the follow-up consolidation PR.
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
