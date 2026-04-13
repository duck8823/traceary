package usecase

import (
	"context"

	apptypes "github.com/duck8823/traceary/application/types"
)

// MemoryExtractionUsecase extracts candidate durable memories from existing
// session/history signals.
//
// Extraction is intentionally candidate-only. The resulting facts still need
// review before they become accepted durable memories.
type MemoryExtractionUsecase interface {
	// Extract proposes candidate memories from the target session and returns the
	// created candidate details.
	//
	// Stored facts are expected to pass through the existing memory
	// sanitization/redaction path before persistence.
	Extract(ctx context.Context, criteria apptypes.MemoryExtractionCriteria) ([]apptypes.MemoryDetails, error)
}
