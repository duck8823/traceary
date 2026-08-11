package sqlite

import (
	"context"
	"database/sql"

	"golang.org/x/xerrors"

	apptypes "github.com/duck8823/traceary/application/types"
)

const searchProjectionExclusionReportLimit = 20

func measureSearchProjectionExclusions(ctx context.Context, db *sql.DB, status *apptypes.SearchProjectionStatus) error {
	var generation string
	if err := db.QueryRowContext(ctx, `SELECT COALESCE(active_generation_id,'') FROM search_projection_state WHERE singleton=1`).Scan(&generation); err != nil {
		return xerrors.Errorf("read active projection generation for exclusions: %w", err)
	}
	if generation == "" {
		return nil
	}
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM search_projection_exclusions WHERE generation_id=?`, generation).Scan(&status.ExclusionCount); err != nil {
		return xerrors.Errorf("count search projection exclusions: %w", err)
	}
	rows, err := db.QueryContext(ctx, `SELECT source_sequence,event_id,class,source_bytes,byte_limit FROM search_projection_exclusions WHERE generation_id=? ORDER BY source_sequence LIMIT ?`, generation, searchProjectionExclusionReportLimit)
	if err != nil {
		return xerrors.Errorf("query search projection exclusions: %w", err)
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var exclusion apptypes.SearchProjectionExclusion
		if err := rows.Scan(&exclusion.Sequence, &exclusion.EventID, &exclusion.Class, &exclusion.SourceBytes, &exclusion.ByteLimit); err != nil {
			return xerrors.Errorf("scan search projection exclusion: %w", err)
		}
		status.Exclusions = append(status.Exclusions, exclusion)
	}
	if err := rows.Err(); err != nil {
		return xerrors.Errorf("iterate search projection exclusions: %w", err)
	}
	return nil
}
