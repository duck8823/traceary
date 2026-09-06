//nolint:wrapcheck,revive // Guard diagnostics retain the exact SQLite operation that failed.
package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
)

// rollbackGuardLookupChunk bounds one membership probe against the retained
// rollback inode, so GiB-scale stores never load a full ID set into memory.
const rollbackGuardLookupChunk = 500

// SQLiteCompactionRollbackGuard implements
// application.CompactionRollbackGuard by comparing event IDs across the two
// inodes. Compaction copies then vacuums rows, so surviving events keep
// their IDs and copy filters only drop tables or clear bodies; an event
// present in the published store but absent from the rollback inode arrived
// after the pre-compact snapshot (#2329).
type SQLiteCompactionRollbackGuard struct{}

// CountRecordsAtRisk implements application.CompactionRollbackGuard.
func (SQLiteCompactionRollbackGuard) CountRecordsAtRisk(ctx context.Context, publishedPath, rollbackPath string) (int, error) {
	published, err := openDirectReadOnly(ctx, publishedPath)
	if err != nil {
		return 0, fmt.Errorf("open published store for rollback guard: %w", err)
	}
	defer func() { _ = published.Close() }()
	rollback, err := openDirectReadOnly(ctx, rollbackPath)
	if err != nil {
		return 0, fmt.Errorf("open rollback inode for rollback guard: %w", err)
	}
	defer func() { _ = rollback.Close() }()
	// A side without an events table holds no events, so there is nothing a
	// rollback could discard. The events table ships in the initial schema,
	// so this only triggers for synthetic test stores.
	for _, db := range []*sql.DB{published, rollback} {
		ok, err := tableExists(ctx, db, "events")
		if err != nil {
			return 0, fmt.Errorf("inspect events table for rollback guard: %w", err)
		}
		if !ok {
			return 0, nil
		}
	}
	atRisk := 0
	var offset int64
	for {
		if err := ctx.Err(); err != nil {
			return 0, err
		}
		ids, err := publishedEventIDs(ctx, published, offset)
		if err != nil {
			return 0, err
		}
		if len(ids) == 0 {
			return atRisk, nil
		}
		missing, err := countAbsentFromRollback(ctx, rollback, ids)
		if err != nil {
			return 0, err
		}
		atRisk += missing
		offset += int64(len(ids))
	}
}

func publishedEventIDs(ctx context.Context, db *sql.DB, offset int64) ([]string, error) {
	rows, err := db.QueryContext(ctx, `SELECT id FROM events ORDER BY rowid LIMIT ? OFFSET ?`, rollbackGuardLookupChunk, offset)
	if err != nil {
		return nil, fmt.Errorf("list published event IDs: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("scan published event ID: %w", err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate published event IDs: %w", err)
	}
	return ids, nil
}

func countAbsentFromRollback(ctx context.Context, db *sql.DB, ids []string) (int, error) {
	placeholders := make([]string, len(ids))
	args := make([]any, len(ids))
	for i, id := range ids {
		placeholders[i] = "?"
		args[i] = id
	}
	rows, err := db.QueryContext(ctx, `SELECT id FROM events WHERE id IN (`+strings.Join(placeholders, ",")+`)`, args...)
	if err != nil {
		return 0, fmt.Errorf("probe rollback event IDs: %w", err)
	}
	defer func() { _ = rows.Close() }()
	present := make(map[string]struct{}, len(ids))
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return 0, fmt.Errorf("scan rollback event ID: %w", err)
		}
		present[id] = struct{}{}
	}
	if err := rows.Err(); err != nil {
		return 0, fmt.Errorf("iterate rollback event IDs: %w", err)
	}
	missing := 0
	for _, id := range ids {
		if _, ok := present[id]; !ok {
			missing++
		}
	}
	return missing, nil
}
