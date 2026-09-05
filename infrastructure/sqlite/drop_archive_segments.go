package sqlite

import (
	"context"
	"database/sql"
	"fmt"

	apptypes "github.com/duck8823/traceary/application/types"
)

// SemanticVerifierDropArchiveSegments is the Layer-2 verifier for offline
// migration 84 (owner #2326). It asserts table absence only.
const SemanticVerifierDropArchiveSegments SemanticVerifierID = "drop_archive_segments"

const droppedArchiveSegmentsReaderVersion = 40

const archiveSegmentsTable = "archive_segments"

func pendingDropsArchiveSegments(plan PreparedMigrationPlan) bool {
	for _, migration := range plan.Pending {
		if migration.Version != 84 {
			continue
		}
		entry, ok := preparedMigrationManifest[84]
		if ok && entry.SemanticVerifierID == SemanticVerifierDropArchiveSegments {
			return true
		}
	}
	return false
}

func refuseArchiveSegmentsIfNonEmpty(ctx context.Context, db *sql.DB) error {
	exists, err := tableExists(ctx, db, archiveSegmentsTable)
	if err != nil {
		return err
	}
	if !exists {
		return nil
	}
	var rowCount int
	if err = db.QueryRowContext(ctx, `SELECT COUNT(*) FROM archive_segments`).Scan(&rowCount); err != nil {
		return fmt.Errorf("count archive_segments rows: %w", err)
	}
	if rowCount == 0 {
		return nil
	}
	return &apptypes.ArchiveSegmentsNonEmptyError{RowCount: rowCount}
}

func verifyDropArchiveSegments(ctx context.Context, candidateDB *sql.DB, laterReaderRaisePending bool) error {
	exists, err := tableExists(ctx, candidateDB, archiveSegmentsTable)
	if err != nil {
		return err
	}
	if exists {
		return fmt.Errorf("candidate still has table %s", archiveSegmentsTable)
	}
	var minimumReader int
	if err := candidateDB.QueryRowContext(ctx, `SELECT minimum_reader_version FROM store_format_state WHERE singleton = 1`).Scan(&minimumReader); err != nil {
		return fmt.Errorf("read candidate minimum_reader_version: %w", err)
	}
	if laterReaderRaisePending {
		if minimumReader < droppedArchiveSegmentsReaderVersion {
			return fmt.Errorf("candidate minimum_reader_version = %d, want at least %d", minimumReader, droppedArchiveSegmentsReaderVersion)
		}
	} else if minimumReader != droppedArchiveSegmentsReaderVersion {
		return fmt.Errorf("candidate minimum_reader_version = %d, want %d", minimumReader, droppedArchiveSegmentsReaderVersion)
	}
	return nil
}
