package usecase

import (
	"context"

	"github.com/duck8823/traceary/domain/types"
)

// SessionOrphanRangeUsecase front-loads orphan discovery at the compact
// boundary. Consolidation itself lives on OrphanConsolidationUsecase.
type SessionOrphanRangeUsecase interface {
	// RecordAtCompact records an orphan range when the session's current
	// refinement covers_to is strictly before compactEventID. When no
	// refinement exists the range starts at the session's first event. When
	// the refinement already covers the compact event, nothing is recorded.
	RecordAtCompact(ctx context.Context, sessionID types.SessionID, compactEventID types.EventID) error
}
