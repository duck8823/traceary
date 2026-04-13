package usecase

import (
	"context"

	apptypes "github.com/duck8823/traceary/application/types"
)

// MemoryExtractionUsecase extracts candidate durable memories from existing
// session/history signals.
type MemoryExtractionUsecase interface {
	// Extract proposes candidate memories from the target session and returns the
	// created candidate details.
	Extract(ctx context.Context, criteria apptypes.MemoryExtractionCriteria) ([]apptypes.MemoryDetails, error)
}
