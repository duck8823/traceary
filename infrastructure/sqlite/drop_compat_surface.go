package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	apptypes "github.com/duck8823/traceary/application/types"
)

// SemanticVerifierDropCompatSurface is the Layer-2 verifier for offline
// migration 86 (owner #2322). The identifier names this migration's
// verifier, not a generic compatibility-surface registry: it asserts
// absence of event_metadata_projection.legacy_source_hook, absence of
// that name from both recreated trigger bodies, and reader version 42.
const SemanticVerifierDropCompatSurface SemanticVerifierID = "drop_compat_surface"

const droppedCompatSurfaceReaderVersion = 42

const droppedLegacySourceHookColumn = "legacy_source_hook"

func pendingDropsCompatSurface(plan PreparedMigrationPlan) bool {
	for _, migration := range plan.Pending {
		if migration.Version != 86 {
			continue
		}
		entry, ok := preparedMigrationManifest[86]
		if ok && entry.SemanticVerifierID == SemanticVerifierDropCompatSurface {
			return true
		}
	}
	return false
}

func refuseLegacyHookIfNonNull(ctx context.Context, db *sql.DB) error {
	hasColumn, err := tableHasColumn(ctx, db, "event_metadata_projection", droppedLegacySourceHookColumn)
	if err != nil {
		return err
	}
	if !hasColumn {
		return nil
	}
	var rowCount int
	if err = db.QueryRowContext(ctx, `SELECT COUNT(*) FROM event_metadata_projection WHERE legacy_source_hook IS NOT NULL`).Scan(&rowCount); err != nil {
		return fmt.Errorf("count non-null legacy_source_hook rows: %w", err)
	}
	if rowCount == 0 {
		return nil
	}
	return &apptypes.LegacyHookNonEmptyError{RowCount: rowCount}
}

func verifyDropCompatSurface(ctx context.Context, candidateDB *sql.DB) error {
	hasColumn, err := tableHasColumn(ctx, candidateDB, "event_metadata_projection", droppedLegacySourceHookColumn)
	if err != nil {
		return err
	}
	if hasColumn {
		return fmt.Errorf("candidate event_metadata_projection still has column %s", droppedLegacySourceHookColumn)
	}
	for _, name := range []string{
		"event_metadata_projection_events_after_insert",
		"event_metadata_projection_events_after_update",
	} {
		var sqlText sql.NullString
		if err := candidateDB.QueryRowContext(ctx, `SELECT sql FROM sqlite_schema WHERE type = 'trigger' AND name = ?`, name).Scan(&sqlText); err != nil {
			return fmt.Errorf("read candidate trigger %s: %w", name, err)
		}
		if !sqlText.Valid || sqlText.String == "" {
			return fmt.Errorf("candidate is missing trigger %s", name)
		}
		if strings.Contains(sqlText.String, droppedLegacySourceHookColumn) {
			return fmt.Errorf("candidate trigger %s still references %s", name, droppedLegacySourceHookColumn)
		}
	}
	var minimumReader int
	if err := candidateDB.QueryRowContext(ctx, `SELECT minimum_reader_version FROM store_format_state WHERE singleton = 1`).Scan(&minimumReader); err != nil {
		return fmt.Errorf("read candidate minimum_reader_version: %w", err)
	}
	if minimumReader != droppedCompatSurfaceReaderVersion {
		return fmt.Errorf("candidate minimum_reader_version = %d, want %d", minimumReader, droppedCompatSurfaceReaderVersion)
	}
	return nil
}
