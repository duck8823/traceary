package sqlite

import (
	"context"
	"database/sql"
	"time"

	"golang.org/x/xerrors"

	apptypes "github.com/duck8823/traceary/application/types"
)

// cleanupDiscoveryMinRowTime is a conservative per-row hold budget used to
// shrink a cleanup page so discovery plus delete can finish inside the
// remaining wall. It is not a measured SLA; it only keeps a 50ms test open
// from selecting more leftovers than the write transaction can commit.
const cleanupDiscoveryMinRowTime = 2 * time.Millisecond

type projectionCleanupBranch struct {
	query string
	args  []any
}

// selectProjectionCleanupPaged addresses each leftover table by its primary
// key and applies LIMIT inside that table. A UNION ALL of every leftover row
// before LIMIT can spend the whole wall budget discovering and then commit
// nothing (#2010). After a page deletes, the next open's LIMIT naturally
// returns the next leftovers.
func selectProjectionCleanupPaged(ctx context.Context, db *sql.DB, out apptypes.ProjectionSnapshot, b apptypes.SearchProjectionBudget) (apptypes.ProjectionSnapshot, error) {
	page := cleanupDiscoveryPageSize(ctx, b)
	want := page + 1
	scannedAll := true
	for _, branch := range projectionCleanupDiscoveryBranches(out, want) {
		if err := ctx.Err(); err != nil {
			if len(out.Cleanup) > 0 {
				out.CleanupDone = false
				if len(out.Cleanup) > page {
					out.Cleanup = out.Cleanup[:page]
				}
				return out, nil
			}
			return out, xerrors.Errorf("cleanup leftover discovery cancelled: %w", err)
		}
		remaining := want - len(out.Cleanup)
		if remaining <= 0 {
			scannedAll = false
			break
		}
		branch.args[len(branch.args)-1] = remaining
		rows, err := db.QueryContext(ctx, branch.query, branch.args...)
		if err != nil {
			if ctx.Err() != nil && len(out.Cleanup) > 0 {
				out.CleanupDone = false
				if len(out.Cleanup) > page {
					out.Cleanup = out.Cleanup[:page]
				}
				return out, nil
			}
			return out, xerrors.Errorf("query cleanup leftover page: %w", err)
		}
		fetched, scanErr := appendProjectionCleanupRows(rows, &out)
		_ = rows.Close()
		if scanErr != nil {
			if ctx.Err() != nil && len(out.Cleanup) > 0 {
				out.CleanupDone = false
				if len(out.Cleanup) > page {
					out.Cleanup = out.Cleanup[:page]
				}
				return out, nil
			}
			return out, xerrors.Errorf("read cleanup leftover page: %w", scanErr)
		}
		if fetched == remaining {
			scannedAll = false
		}
	}
	if scannedAll && len(out.Cleanup) <= page {
		out.CleanupDone = true
	} else {
		out.CleanupDone = false
		if len(out.Cleanup) > page {
			out.Cleanup = out.Cleanup[:page]
		}
	}
	return out, nil
}

func cleanupDiscoveryPageSize(ctx context.Context, b apptypes.SearchProjectionBudget) int {
	page := b.Rows
	if page < 1 {
		page = 1
	}
	deadline, ok := ctx.Deadline()
	if !ok {
		return page
	}
	remaining := time.Until(deadline)
	if remaining <= 0 {
		return 1
	}
	maxByTime := int(remaining / cleanupDiscoveryMinRowTime)
	if maxByTime < 1 {
		maxByTime = 1
	}
	if maxByTime < page {
		return maxByTime
	}
	return page
}

func projectionCleanupDiscoveryBranches(out apptypes.ProjectionSnapshot, limit int) []projectionCleanupBranch {
	if out.CleanupAll {
		return []projectionCleanupBranch{
			{`SELECT 'recent',document_id,` + recentCleanupLogicalBytesSQL + `, '','','',X'' FROM search_projection_recent_documents ORDER BY document_id LIMIT ?`, []any{limit}},
			{`SELECT 'summary',0,` + summaryCleanupLogicalBytesSQL + `, generation_id,session_id,'',X'' FROM search_projection_session_summaries ORDER BY generation_id,session_id LIMIT ?`, []any{limit}},
			{`SELECT 'aggregate',0,` + aggregateCleanupLogicalBytesSQL + `, generation_id,session_id,'',X'' FROM search_projection_command_aggregates ORDER BY generation_id,session_id LIMIT ?`, []any{limit}},
			{`SELECT 'keyword',0,` + keywordCleanupLogicalBytesSQL + `, generation_id,session_id,keyword,X'' FROM search_projection_session_keywords ORDER BY generation_id,session_id,keyword LIMIT ?`, []any{limit}},
			{`SELECT 'fingerprint',0,` + fingerprintCleanupLogicalBytesSQL + `, generation_id,event_id,'',fingerprint FROM literal_search_fingerprints ORDER BY generation_id,event_id,fingerprint LIMIT ?`, []any{limit}},
			{`SELECT 'exclusion',source_sequence,` + exclusionCleanupLogicalBytesSQL + `, generation_id,'','',X'' FROM search_projection_exclusions ORDER BY generation_id,source_sequence LIMIT ?`, []any{limit}},
		}
	}
	generation := out.Generation.GenerationID
	// Two range scans per table so generation_id<>current is a PK seek, not a
	// full leftover materialization. After deletes, the next LIMIT page is
	// the next keyset.
	return []projectionCleanupBranch{
		{`SELECT 'recent',document_id,` + recentCleanupLogicalBytesSQL + `, '','','',X'' FROM search_projection_recent_documents WHERE generation_id<? ORDER BY document_id LIMIT ?`, []any{generation, limit}},
		{`SELECT 'recent',document_id,` + recentCleanupLogicalBytesSQL + `, '','','',X'' FROM search_projection_recent_documents WHERE generation_id>? ORDER BY document_id LIMIT ?`, []any{generation, limit}},
		{`SELECT 'summary',0,` + summaryCleanupLogicalBytesSQL + `, generation_id,session_id,'',X'' FROM search_projection_session_summaries WHERE generation_id<? ORDER BY generation_id,session_id LIMIT ?`, []any{generation, limit}},
		{`SELECT 'summary',0,` + summaryCleanupLogicalBytesSQL + `, generation_id,session_id,'',X'' FROM search_projection_session_summaries WHERE generation_id>? ORDER BY generation_id,session_id LIMIT ?`, []any{generation, limit}},
		{`SELECT 'aggregate',0,` + aggregateCleanupLogicalBytesSQL + `, generation_id,session_id,'',X'' FROM search_projection_command_aggregates WHERE generation_id<? ORDER BY generation_id,session_id LIMIT ?`, []any{generation, limit}},
		{`SELECT 'aggregate',0,` + aggregateCleanupLogicalBytesSQL + `, generation_id,session_id,'',X'' FROM search_projection_command_aggregates WHERE generation_id>? ORDER BY generation_id,session_id LIMIT ?`, []any{generation, limit}},
		{`SELECT 'keyword',0,` + keywordCleanupLogicalBytesSQL + `, generation_id,session_id,keyword,X'' FROM search_projection_session_keywords WHERE generation_id<? ORDER BY generation_id,session_id,keyword LIMIT ?`, []any{generation, limit}},
		{`SELECT 'keyword',0,` + keywordCleanupLogicalBytesSQL + `, generation_id,session_id,keyword,X'' FROM search_projection_session_keywords WHERE generation_id>? ORDER BY generation_id,session_id,keyword LIMIT ?`, []any{generation, limit}},
		{`SELECT 'fingerprint',0,` + fingerprintCleanupLogicalBytesSQL + `, generation_id,event_id,'',fingerprint FROM literal_search_fingerprints WHERE generation_id<? ORDER BY generation_id,event_id,fingerprint LIMIT ?`, []any{generation, limit}},
		{`SELECT 'fingerprint',0,` + fingerprintCleanupLogicalBytesSQL + `, generation_id,event_id,'',fingerprint FROM literal_search_fingerprints WHERE generation_id>? ORDER BY generation_id,event_id,fingerprint LIMIT ?`, []any{generation, limit}},
		{`SELECT 'exclusion',source_sequence,` + exclusionCleanupLogicalBytesSQL + `, generation_id,'','',X'' FROM search_projection_exclusions WHERE generation_id<? ORDER BY generation_id,source_sequence LIMIT ?`, []any{generation, limit}},
		{`SELECT 'exclusion',source_sequence,` + exclusionCleanupLogicalBytesSQL + `, generation_id,'','',X'' FROM search_projection_exclusions WHERE generation_id>? ORDER BY generation_id,source_sequence LIMIT ?`, []any{generation, limit}},
	}
}

func appendProjectionCleanupRows(rows *sql.Rows, out *apptypes.ProjectionSnapshot) (int, error) {
	fetched := 0
	for rows.Next() {
		var c apptypes.ProjectionCleanupCandidate
		if err := rows.Scan(&c.Class, &c.RowID, &c.LogicalBytes, &c.Address1, &c.Address2, &c.Address3, &c.AddressBlob); err != nil {
			return fetched, xerrors.Errorf("scan cleanup leftover row: %w", err)
		}
		out.Cleanup = append(out.Cleanup, c)
		fetched++
	}
	if err := rows.Err(); err != nil {
		return fetched, xerrors.Errorf("iterate cleanup leftover rows: %w", err)
	}
	return fetched, nil
}

// RecordCleanupNoProgressAttempt increments the durable cleanup stall
// counter. After SearchProjectionCleanupNoProgressLimit consecutive attempts
// that committed no row, the generation is parked as failed so the next open
// skips instead of retrying forever (#2010).
func (d *Database) RecordCleanupNoProgressAttempt(ctx context.Context, generation string, now time.Time) (int, bool, error) {
	if generation == "" {
		return 0, false, &apptypes.SearchProjectionNoProgressError{Reason: "cleanup no-progress recording requires a generation id"}
	}
	db, err := d.open(ctx)
	if err != nil {
		return 0, false, xerrors.Errorf("open store for cleanup no-progress: %w", err)
	}
	defer func() { _ = db.Close() }()
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return 0, false, xerrors.Errorf("begin cleanup no-progress: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	stamp := formatTimestamp(now.UTC())
	result, err := tx.ExecContext(ctx, `UPDATE search_projection_state SET cleanup_no_progress_attempts=cleanup_no_progress_attempts+1,updated_at=? WHERE generation_id=? AND phase='cleanup' AND state IN ('rebuilding','drifted')`, stamp, generation)
	if err != nil {
		return 0, false, xerrors.Errorf("increment cleanup no-progress attempts: %w", err)
	}
	if n, rowErr := result.RowsAffected(); rowErr != nil || n != 1 {
		return 0, false, &apptypes.SearchProjectionDriftError{}
	}
	var attempts int
	if err = tx.QueryRowContext(ctx, `SELECT cleanup_no_progress_attempts FROM search_projection_state WHERE generation_id=?`, generation).Scan(&attempts); err != nil {
		return 0, false, xerrors.Errorf("read cleanup no-progress attempts: %w", err)
	}
	if attempts < apptypes.SearchProjectionCleanupNoProgressLimit {
		if err = tx.Commit(); err != nil {
			return 0, false, xerrors.Errorf("commit cleanup no-progress increment: %w", err)
		}
		return attempts, false, nil
	}
	result, err = tx.ExecContext(ctx, `UPDATE search_projection_state SET state='failed',phase='complete',failure_class=?,updated_at=? WHERE generation_id=? AND state IN ('rebuilding','drifted')`, apptypes.SearchProjectionFailureCleanupNoProgress, stamp, generation)
	if err != nil {
		return 0, false, xerrors.Errorf("park cleanup no-progress generation: %w", err)
	}
	if n, rowErr := result.RowsAffected(); rowErr != nil || n != 1 {
		return 0, false, &apptypes.SearchProjectionDriftError{}
	}
	result, err = tx.ExecContext(ctx, `UPDATE search_projection_generation_lifecycle SET state='failed' WHERE generation_id=? AND state IN ('rebuilding','drifted')`, generation)
	if err != nil {
		return 0, false, xerrors.Errorf("park cleanup no-progress lifecycle: %w", err)
	}
	if n, rowErr := result.RowsAffected(); rowErr != nil || n != 1 {
		return 0, false, &apptypes.SearchProjectionDriftError{}
	}
	if err = tx.Commit(); err != nil {
		return 0, false, xerrors.Errorf("commit cleanup no-progress park: %w", err)
	}
	return attempts, true, nil
}
