package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
)

// SemanticVerifierDropDedupeArchive is the Layer-2 verifier for offline
// migration 81 (owner #2324).
const SemanticVerifierDropDedupeArchive SemanticVerifierID = "drop_dedupe_archive"

const droppedDedupeArchiveReaderVersion = 37

func verifyDropDedupeArchive(ctx context.Context, sourceDB, candidateDB *sql.DB, decodePending bool) error {
	exists, err := tableExists(ctx, candidateDB, restoreDedupeArchiveTable)
	if err != nil {
		return err
	}
	if exists {
		return fmt.Errorf("candidate still has table %s", restoreDedupeArchiveTable)
	}
	var minimumReader int
	if err := candidateDB.QueryRowContext(ctx, `SELECT minimum_reader_version FROM store_format_state WHERE singleton = 1`).Scan(&minimumReader); err != nil {
		return fmt.Errorf("read candidate minimum_reader_version: %w", err)
	}
	if minimumReader < droppedDedupeArchiveReaderVersion {
		return fmt.Errorf("candidate minimum_reader_version = %d, want at least %d", minimumReader, droppedDedupeArchiveReaderVersion)
	}
	return verifyRestoreDedupeArchiveConservation(ctx, sourceDB, candidateDB, decodePending)
}

func verifyRestoreDedupeArchiveConservation(ctx context.Context, sourceDB, candidateDB *sql.DB, decodePending bool) error {
	sourceCount, err := countTableRows(ctx, sourceDB, "events")
	if err != nil {
		return err
	}
	candidateCount, err := countTableRows(ctx, candidateDB, "events")
	if err != nil {
		return err
	}
	archiveCount, archiveIDs, err := sourceArchiveInventory(ctx, sourceDB)
	if err != nil {
		return err
	}
	if candidateCount != sourceCount+archiveCount {
		return fmt.Errorf("candidate events count=%d, want source %d + archive %d", candidateCount, sourceCount, archiveCount)
	}
	if decodePending {
		for _, id := range archiveIDs {
			var exists int
			if err := candidateDB.QueryRowContext(ctx, `SELECT 1 FROM events WHERE id = ?`, id).Scan(&exists); err != nil {
				return fmt.Errorf("archived event %s missing from candidate", id)
			}
		}
		return nil
	}
	columns, err := tableColumns(ctx, sourceDB, "events")
	if err != nil {
		return err
	}
	baseQuery := `SELECT ` + joinQuotedIdentifiers(columns) + ` FROM ` + quoteIdentifier("events")
	srcCount, srcDigest, err := digestRows(ctx, sourceDB, columns, baseQuery)
	if err != nil {
		return fmt.Errorf("digest source events: %w", err)
	}
	candQuery := baseQuery
	var args []any
	if len(archiveIDs) > 0 {
		placeholders := make([]string, len(archiveIDs))
		args = make([]any, len(archiveIDs))
		for i, id := range archiveIDs {
			placeholders[i] = "?"
			args[i] = id
		}
		candQuery = baseQuery + ` WHERE id NOT IN (` + strings.Join(placeholders, ",") + `)`
	}
	candCount, candDigest, err := digestRows(ctx, candidateDB, columns, candQuery, args...)
	if err != nil {
		return fmt.Errorf("digest candidate source-id events: %w", err)
	}
	if srcCount != candCount || srcDigest != candDigest {
		return fmt.Errorf("candidate rewrote source event rows during dedupe-archive restore")
	}
	for _, id := range archiveIDs {
		var exists int
		if err := candidateDB.QueryRowContext(ctx, `SELECT 1 FROM events WHERE id = ?`, id).Scan(&exists); err != nil {
			return fmt.Errorf("archived event %s missing from candidate", id)
		}
	}
	return nil
}

func sourceArchiveInventory(ctx context.Context, sourceDB *sql.DB) (int64, []string, error) {
	exists, err := tableExists(ctx, sourceDB, restoreDedupeArchiveTable)
	if err != nil {
		return 0, nil, err
	}
	if !exists {
		return 0, nil, nil
	}
	var count int64
	if err := sourceDB.QueryRowContext(ctx, `SELECT COUNT(*) FROM event_content_dedupe_archive`).Scan(&count); err != nil {
		return 0, nil, fmt.Errorf("count source event_content_dedupe_archive: %w", err)
	}
	rows, err := sourceDB.QueryContext(ctx, `SELECT DISTINCT id FROM event_content_dedupe_archive`)
	if err != nil {
		return 0, nil, fmt.Errorf("list source event_content_dedupe_archive ids: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return 0, nil, fmt.Errorf("scan source archive id: %w", err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return 0, nil, fmt.Errorf("iterate source archive ids: %w", err)
	}
	return count, ids, nil
}

func countTableRows(ctx context.Context, db *sql.DB, table string) (int64, error) {
	exists, err := tableExists(ctx, db, table)
	if err != nil {
		return 0, err
	}
	if !exists {
		return 0, nil
	}
	var count int64
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM `+quoteIdentifier(table)).Scan(&count); err != nil {
		return 0, fmt.Errorf("count %s: %w", table, err)
	}
	return count, nil
}
