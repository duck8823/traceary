package sqlite

import (
	"context"
	"database/sql"

	"golang.org/x/xerrors"

	apptypes "github.com/duck8823/traceary/application/types"
)

// insertSearchProjectionExclusion keeps the exclusion write beside the
// checkpoint mutation performed by ApplyBatch; the caller owns the transaction.
func insertSearchProjectionExclusion(ctx context.Context, tx *sql.Tx, generation string, exclusion apptypes.ProjectionExclusion) error {
	if _, err := tx.ExecContext(ctx, `INSERT INTO search_projection_exclusions(generation_id,source_sequence,event_id,class,source_bytes,byte_limit) VALUES(?,?,?,?,?,?)`, generation, exclusion.Sequence, exclusion.EventID, exclusion.Class, exclusion.SourceBytes, exclusion.ByteLimit); err != nil {
		return xerrors.Errorf("insert search projection exclusion: %w", err)
	}
	return nil
}
