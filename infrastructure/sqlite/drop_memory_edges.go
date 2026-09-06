package sqlite

import (
	"context"
	"database/sql"
	"fmt"

	apptypes "github.com/duck8823/traceary/application/types"
)

// SemanticVerifierDropMemoryEdges is the Layer-2 verifier for offline
// migration 85 (owner #2327). It asserts table absence only.
const SemanticVerifierDropMemoryEdges SemanticVerifierID = "drop_memory_edges"

const droppedMemoryEdgesReaderVersion = 41

const memoryEdgesTable = "memory_edges"

func pendingDropsMemoryEdges(plan PreparedMigrationPlan) bool {
	for _, migration := range plan.Pending {
		if migration.Version != 85 {
			continue
		}
		entry, ok := preparedMigrationManifest[85]
		if ok && entry.SemanticVerifierID == SemanticVerifierDropMemoryEdges {
			return true
		}
	}
	return false
}

func refuseMemoryEdgesIfNonEmpty(ctx context.Context, db *sql.DB) error {
	exists, err := tableExists(ctx, db, memoryEdgesTable)
	if err != nil {
		return err
	}
	if !exists {
		return nil
	}
	var rowCount int
	if err = db.QueryRowContext(ctx, `SELECT COUNT(*) FROM memory_edges`).Scan(&rowCount); err != nil {
		return fmt.Errorf("count memory_edges rows: %w", err)
	}
	if rowCount == 0 {
		return nil
	}
	return &apptypes.MemoryEdgesNonEmptyError{RowCount: rowCount}
}

func verifyDropMemoryEdges(ctx context.Context, candidateDB *sql.DB, laterReaderRaisePending bool) error {
	exists, err := tableExists(ctx, candidateDB, memoryEdgesTable)
	if err != nil {
		return err
	}
	if exists {
		return fmt.Errorf("candidate still has table %s", memoryEdgesTable)
	}
	var minimumReader int
	if err := candidateDB.QueryRowContext(ctx, `SELECT minimum_reader_version FROM store_format_state WHERE singleton = 1`).Scan(&minimumReader); err != nil {
		return fmt.Errorf("read candidate minimum_reader_version: %w", err)
	}
	if laterReaderRaisePending {
		if minimumReader < droppedMemoryEdgesReaderVersion {
			return fmt.Errorf("candidate minimum_reader_version = %d, want at least %d", minimumReader, droppedMemoryEdgesReaderVersion)
		}
	} else if minimumReader != droppedMemoryEdgesReaderVersion {
		return fmt.Errorf("candidate minimum_reader_version = %d, want %d", minimumReader, droppedMemoryEdgesReaderVersion)
	}
	return nil
}
