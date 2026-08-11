package sqlite

import (
	"context"
	"database/sql"
	_ "embed"

	"golang.org/x/xerrors"

	apptypes "github.com/duck8823/traceary/application/types"
)

//go:embed sql/select_search_projection_exclusions.sql
var selectSearchProjectionExclusionsSQL string

const searchProjectionExclusionReportLimit = 20

func measureSearchProjectionExclusions(ctx context.Context, db *sql.DB, status *apptypes.SearchProjectionStatus) error {
	var generation string
	if err := db.QueryRowContext(ctx, `SELECT COALESCE(generation_id,'') FROM search_projection_state WHERE singleton=1`).Scan(&generation); err != nil {
		return xerrors.Errorf("read projection generation for exclusions: %w", err)
	}
	if generation == "" {
		return nil
	}
	status.ExclusionGenerationID = generation
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM search_projection_exclusions WHERE generation_id=?`, generation).Scan(&status.ExclusionCount); err != nil {
		return xerrors.Errorf("count search projection exclusions: %w", err)
	}
	rows, err := db.QueryContext(ctx, selectSearchProjectionExclusionsSQL, generation, searchProjectionExclusionReportLimit)
	if err != nil {
		return xerrors.Errorf("query search projection exclusions: %w", err)
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var exclusion apptypes.SearchProjectionExclusion
		if err := rows.Scan(&exclusion.Sequence, &exclusion.EventID, &exclusion.Class, &exclusion.MeasuredBytes, &exclusion.ByteLimit); err != nil {
			return xerrors.Errorf("scan search projection exclusion: %w", err)
		}
		status.Exclusions = append(status.Exclusions, exclusion)
	}
	if err := rows.Err(); err != nil {
		return xerrors.Errorf("iterate search projection exclusions: %w", err)
	}
	return nil
}
