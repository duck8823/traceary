package sqlite

import (
	"context"
	"database/sql"
	_ "embed"

	"golang.org/x/xerrors"

	apptypes "github.com/duck8823/traceary/application/types"
)

//go:embed sql/insert_search_projection_exclusion.sql
var insertSearchProjectionExclusionSQL string

// insertSearchProjectionExclusion keeps the exclusion write beside the
// checkpoint mutation performed by ApplyBatch; the caller owns the transaction.
func insertSearchProjectionExclusion(ctx context.Context, tx *sql.Tx, generation string, exclusion apptypes.ProjectionExclusion) error {
	if _, err := tx.ExecContext(ctx, insertSearchProjectionExclusionSQL, generation, exclusion.Sequence, exclusion.EventID, exclusion.Class, exclusion.MeasuredBytes, exclusion.ByteLimit); err != nil {
		return xerrors.Errorf("insert search projection exclusion: %w", err)
	}
	return nil
}
