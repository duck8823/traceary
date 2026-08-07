package usecase

import (
	"context"

	"github.com/duck8823/traceary/domain/model"
	"github.com/duck8823/traceary/domain/types"
)

// SessionRefineInput is the write-port request. The caller (agent or gc)
// composes summary text; this port never merges or transforms it.
type SessionRefineInput struct {
	SessionID  types.SessionID
	Summary    string
	Keywords   string
	ProducedBy string
	CoversTo   types.EventID
	Degraded   bool
}

// SessionRefinementUsecase is the L2 refinement write port.
type SessionRefinementUsecase interface {
	// Refine stores or supersedes a session refinement. Replaying the same
	// covers_to range is a no-op: one row, unchanged generation and text.
	Refine(ctx context.Context, input SessionRefineInput) (model.SessionRefineResult, error)
}
