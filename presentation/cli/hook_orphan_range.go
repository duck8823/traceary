package cli

import (
	"context"
	"log/slog"

	"github.com/duck8823/traceary/domain/types"
)

// recordOrphanRangeAtCompact front-loads orphan discovery after the post-compact
// digest attempt. Best-effort for the same reasons as applyPostCompactDigest:
// a compact can legitimately arrive for a session that has no row, and the
// orphan range is opportunistic bookkeeping with gc as the fallback that
// discovers the same gap from covers_to on ended/stale sessions. Returning the
// error would re-spool the whole compact delivery for replay and risk a
// duplicate event, which is a worse trade than losing one front-loaded row.
// The warn line is the observability. Call only from the post-compact branch
// after applyPostCompactDigest — pre-compact must not record.
func (c *RootCLI) recordOrphanRangeAtCompact(
	ctx context.Context,
	sessionID types.SessionID,
	compactEventID types.EventID,
) {
	if c.sessionOrphanRange == nil {
		return
	}
	if err := c.sessionOrphanRange.RecordAtCompact(ctx, sessionID, compactEventID); err != nil {
		slog.Warn(
			"post-compact orphan range record failed",
			"session_id", sessionID,
			"compact_event_id", compactEventID,
			"error", err,
		)
	}
}
