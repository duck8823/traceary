package sqlite

import (
	"context"
	"database/sql"
	"time"

	apptypes "github.com/duck8823/traceary/application/types"
	"golang.org/x/xerrors"
)

const selectProjectionEvictionQuery = `SELECT document_id,length(CAST(body_text AS BLOB)),created_at_norm,created_at_norm<?,length(CAST(generation_id AS BLOB))+length(CAST(event_id AS BLOB))+length(CAST(created_at_norm AS BLOB))+2*length(CAST(body_text AS BLOB))+32 FROM search_projection_recent_documents WHERE generation_id=? ORDER BY created_at_norm,event_rowid LIMIT ?`

func recentProjectionHasExpired(ctx context.Context, db *sql.DB, generation, cutoff string) (bool, error) {
	var exists int
	if err := db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM search_projection_recent_documents WHERE generation_id=? AND created_at_norm<?)`, generation, cutoff).Scan(&exists); err != nil {
		return false, xerrors.Errorf("check expired recent projection rows: %w", err)
	}
	return exists != 0, nil
}

// selectProjectionEviction reads only the index-aligned oldest candidates. The
// application planner decides how many are needed for the byte and age rules.
func selectProjectionEviction(ctx context.Context, db *sql.DB, out apptypes.ProjectionSnapshot, b apptypes.SearchProjectionBudget, now time.Time) (apptypes.ProjectionSnapshot, error) {
	cutoff := projectionCutoff(now.Add(-b.RecentAge))
	rows, err := db.QueryContext(ctx, selectProjectionEvictionQuery, cutoff, out.Generation.GenerationID, b.Rows+1)
	if err != nil {
		return out, xerrors.Errorf("select recent projection eviction candidates: %w", err)
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var c apptypes.ProjectionCleanupCandidate
		if err := rows.Scan(&c.RowID, &c.ReleasedSourceBytes, &c.CreatedAtNorm, &c.Expired, &c.LogicalBytes); err != nil {
			return out, xerrors.Errorf("scan recent projection eviction candidate: %w", err)
		}
		c.Class = "eviction"
		out.Cleanup = append(out.Cleanup, c)
	}
	if err := rows.Err(); err != nil {
		return out, xerrors.Errorf("iterate recent projection eviction candidates: %w", err)
	}
	out.CleanupDone = len(out.Cleanup) <= b.Rows
	if !out.CleanupDone {
		out.Cleanup = out.Cleanup[:b.Rows]
	}
	return out, nil
}
