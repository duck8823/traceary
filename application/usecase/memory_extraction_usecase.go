package usecase

import (
	"context"

	apptypes "github.com/duck8823/traceary/application/types"
)

// MemoryExtractionUsecase is a legacy adapter interface for the capture
// methods now exposed by MemoryUsecase.
//
// Deprecated: use MemoryUsecase.Extract instead. This shim remains until DI is
// collapsed in the follow-up consolidation PR.
type MemoryExtractionUsecase interface {
	// Extract proposes candidate memories from the target session and returns the
	// created candidate details.
	//
	// Stored facts are expected to pass through the existing memory
	// sanitization/redaction path before persistence.
	Extract(ctx context.Context, criteria apptypes.MemoryExtractionCriteria) ([]apptypes.MemoryDetails, error)
}
