package usecase

import (
	"context"

	"github.com/duck8823/traceary/domain/model"
	"github.com/duck8823/traceary/domain/types"
)

// SessionRefineInput is the write-port request.
//
// Agent callers set Summary (and Keywords) to the finished text. Traceary
// stores what it is handed and owns generation / coverage bookkeeping only.
type SessionRefineInput struct {
	SessionID  types.SessionID
	Summary    string
	Keywords   string
	ProducedBy string
	CoversTo   types.EventID
	Degraded   bool
	// HasAgentReasoning is the first-write value only when Degraded is true
	// (unused by current callers). A non-degraded first write always stores 1.
	// On supersede, a stored 1 is kept, and a non-degraded write upgrades a
	// mechanical-only row so a later agent fold becomes wake-eligible (#1877).
	HasAgentReasoning bool
	// CoverageOnly advances covers_to while keeping stored summary text,
	// degraded, has_agent_reasoning, and produced_by. With no existing row,
	// Refine returns errCoverageOnlyNoRow and inserts nothing.
	CoverageOnly bool
}

// SessionRefinementUsecase is the L2 refinement write port.
type SessionRefinementUsecase interface {
	// Refine stores or supersedes a session refinement. Replaying the same
	// covers_to range is a no-op: one row, unchanged generation and text.
	Refine(ctx context.Context, input SessionRefineInput) (model.SessionRefineResult, error)
}
