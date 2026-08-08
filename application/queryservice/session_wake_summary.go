package queryservice

import (
	"context"

	"github.com/duck8823/traceary/domain/types"
)

// SessionWakeSummary is one finished session summary eligible for wake injection.
// Order from ListEligible is newest-first (started_at DESC, session_id DESC).
type SessionWakeSummary struct {
	SessionID types.SessionID
	Summary   string
}

// SessionWakeSummaryQueryService loads finished (non-degraded) top-level
// session summaries for a workspace so the wake path can inject them.
// Budget arithmetic is not the query's job — callers trim in Go.
type SessionWakeSummaryQueryService interface {
	// ListEligible returns non-degraded top-level refinements for workspace,
	// excluding excludeSessionID, newest first, up to limit rows.
	// A store without the session_refinements table returns an empty slice.
	ListEligible(
		ctx context.Context,
		workspace types.Workspace,
		excludeSessionID types.SessionID,
		limit int,
	) ([]SessionWakeSummary, error)
}
