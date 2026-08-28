package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"golang.org/x/xerrors"

	"github.com/duck8823/traceary/application"
	apptypes "github.com/duck8823/traceary/application/types"
)

type terminalProjectionTable struct {
	name            string
	logicalBytesSQL string
}

// terminalProjectionTables is the ordered delete set. Keep it equal to the
// tables projectionCleanupDiscoveryBranches addresses.
var terminalProjectionTables = []terminalProjectionTable{
	{name: "search_projection_recent_documents", logicalBytesSQL: recentCleanupLogicalBytesSQL},
	{name: "search_projection_session_summaries", logicalBytesSQL: summaryCleanupLogicalBytesSQL},
	{name: "search_projection_command_aggregates", logicalBytesSQL: aggregateCleanupLogicalBytesSQL},
	{name: "search_projection_session_keywords", logicalBytesSQL: keywordCleanupLogicalBytesSQL},
	{name: "literal_search_fingerprints", logicalBytesSQL: fingerprintCleanupLogicalBytesSQL},
	{name: "search_projection_exclusions", logicalBytesSQL: exclusionCleanupLogicalBytesSQL},
}

const listEligibleTerminalGenerationsSQL = `
SELECT l.generation_id, l.state, l.failure_class, l.terminal_at, l.reclaimed_at, l.reclaimed_rows
  FROM search_projection_generation_lifecycle l
  JOIN search_projection_state s ON s.singleton = 1
 WHERE l.state IN ('failed','abandoned')
   AND l.generation_id <> COALESCE(s.active_generation_id, '')
   AND NOT (l.generation_id = COALESCE(s.generation_id, '')
            AND s.state IN ('rebuilding','drifted'))
   AND l.reclaimed_at = ''
 ORDER BY l.terminal_at, l.generation_id`

const listTerminalGenerationEvidenceSQL = `
SELECT l.generation_id, l.state, l.failure_class, l.terminal_at, l.reclaimed_at, l.reclaimed_rows
  FROM search_projection_generation_lifecycle l
  JOIN search_projection_state s ON s.singleton = 1
 WHERE l.state IN ('failed','abandoned')
   AND l.generation_id <> COALESCE(s.active_generation_id, '')
 ORDER BY l.terminal_at, l.generation_id`

const reclaimEligibilityGuardSQL = `
SELECT COUNT(*)
  FROM search_projection_generation_lifecycle l
  JOIN search_projection_state s ON s.singleton = 1
 WHERE l.generation_id = ?
   AND l.state IN ('failed','abandoned')
   AND l.generation_id <> COALESCE(s.active_generation_id, '')
   AND NOT (l.generation_id = COALESCE(s.generation_id, '')
            AND s.state IN ('rebuilding','drifted'))
   AND NOT EXISTS (SELECT 1 FROM search_projection_generation_lifecycle
                    WHERE generation_id = ? AND state = 'complete')`

const terminalKeywordInventorySQL = `
SELECT COUNT(*), COALESCE(SUM(length(CAST(k.keyword AS BLOB))), 0)
  FROM search_projection_session_keywords k
  JOIN search_projection_generation_lifecycle l ON l.generation_id = k.generation_id
  JOIN search_projection_state s ON s.singleton = 1
 WHERE l.state IN ('failed','abandoned')
   AND l.generation_id <> COALESCE(s.active_generation_id, '')`

const terminalFingerprintInventorySQL = `
SELECT COUNT(*), COALESCE(SUM(length(CAST(f.generation_id AS BLOB))
                            + length(CAST(f.event_id AS BLOB))
                            + length(f.fingerprint) + 16), 0)
  FROM literal_search_fingerprints f
  JOIN search_projection_generation_lifecycle l ON l.generation_id = f.generation_id
  JOIN search_projection_state s ON s.singleton = 1
 WHERE l.state IN ('failed','abandoned')
   AND l.generation_id <> COALESCE(s.active_generation_id, '')`

func listTerminalGenerationsOn(ctx context.Context, db *sql.DB) ([]apptypes.SearchProjectionTerminalGeneration, error) {
	return scanTerminalGenerations(ctx, db, listEligibleTerminalGenerationsSQL)
}

func scanTerminalGenerations(ctx context.Context, q interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}, query string) ([]apptypes.SearchProjectionTerminalGeneration, error) {
	rows, err := q.QueryContext(ctx, query)
	if err != nil {
		return nil, xerrors.Errorf("list terminal search-projection generations: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []apptypes.SearchProjectionTerminalGeneration
	for rows.Next() {
		var g apptypes.SearchProjectionTerminalGeneration
		if err := rows.Scan(&g.GenerationID, &g.LifecycleState, &g.FailureClass, &g.TerminalAt, &g.ReclaimedAt, &g.ReclaimedRows); err != nil {
			return nil, xerrors.Errorf("scan terminal search-projection generation: %w", err)
		}
		out = append(out, g)
	}
	if err := rows.Err(); err != nil {
		return nil, xerrors.Errorf("iterate terminal search-projection generations: %w", err)
	}
	return out, nil
}

func reclaimTerminalGenerationPageOn(ctx context.Context, db *sql.DB, generation string, pageRows int, now time.Time) (apptypes.SearchProjectionReclaimProgress, error) {
	var out apptypes.SearchProjectionReclaimProgress
	if generation == "" {
		return out, &apptypes.SearchProjectionDriftError{}
	}
	if pageRows < 1 {
		pageRows = 1
	}
	tx, err := beginImmediate(ctx, db)
	if err != nil {
		if isSearchProjectionDeadline(err) || isSearchProjectionSQLiteBusy(err) {
			return out, &apptypes.SearchProjectionNoProgressError{Code: apptypes.SearchProjectionNoProgressLockDurationCap, Reason: "projection lock acquisition exceeded"}
		}
		return out, err
	}
	defer func() { _ = tx.Rollback() }()

	var eligible int
	if err := tx.QueryRowContext(ctx, reclaimEligibilityGuardSQL, generation, generation).Scan(&eligible); err != nil {
		return out, xerrors.Errorf("re-check terminal generation eligibility: %w", err)
	}
	if eligible != 1 {
		return out, &apptypes.SearchProjectionDriftError{}
	}

	emptyTables := 0
	for _, table := range terminalProjectionTables {
		measureSQL := fmt.Sprintf(
			`SELECT COUNT(*), COALESCE(SUM(%s), 0) FROM %s WHERE rowid IN (SELECT rowid FROM %s WHERE generation_id = ? LIMIT ?)`,
			table.logicalBytesSQL, table.name, table.name,
		)
		var count, logical int64
		if err := tx.QueryRowContext(ctx, measureSQL, generation, pageRows).Scan(&count, &logical); err != nil {
			return out, xerrors.Errorf("measure terminal reclaim page for %s: %w", table.name, err)
		}
		if count == 0 {
			emptyTables++
			continue
		}
		deleteSQL := fmt.Sprintf(
			`DELETE FROM %s WHERE rowid IN (SELECT rowid FROM %s WHERE generation_id = ? LIMIT ?)`,
			table.name, table.name,
		)
		result, err := tx.ExecContext(ctx, deleteSQL, generation, pageRows)
		if err != nil {
			return out, xerrors.Errorf("delete terminal reclaim page for %s: %w", table.name, err)
		}
		affected, affErr := result.RowsAffected()
		if affErr != nil || affected != count {
			return out, &apptypes.SearchProjectionDriftError{}
		}
		out.Deleted += count
		out.LogicalBytes += logical
	}

	done := emptyTables == len(terminalProjectionTables)
	out.Done = done
	doneFlag := 0
	if done {
		doneFlag = 1
	}
	stamp := formatTimestamp(now.UTC())
	result, err := tx.ExecContext(ctx, `
UPDATE search_projection_generation_lifecycle
   SET reclaimed_rows = reclaimed_rows + ?,
       reclaimed_logical_bytes = reclaimed_logical_bytes + ?,
       reclaimed_at = CASE WHEN ? = 1 THEN ? ELSE reclaimed_at END
 WHERE generation_id = ?
   AND state IN ('failed','abandoned')`,
		out.Deleted, out.LogicalBytes, doneFlag, stamp, generation)
	if err != nil {
		return out, xerrors.Errorf("advance terminal reclaim counters: %w", err)
	}
	n, nerr := result.RowsAffected()
	if nerr != nil || n != 1 {
		return out, &apptypes.SearchProjectionDriftError{}
	}
	if err := tx.Commit(); err != nil {
		return out, err
	}
	return out, nil
}

func reclaimTerminalProjectionGenerationsOn(ctx context.Context, db *sql.DB, pageRows int, now time.Time) (application.CompactStep, error) {
	step := application.CompactStep{Name: application.CompactStepProjectionReclaim, Detail: map[string]int64{}}
	gens, err := listTerminalGenerationsOn(ctx, db)
	if err != nil {
		return step, err
	}
	if len(gens) == 0 {
		step.Skipped = "no terminal generations"
		return step, nil
	}
	var keywordRows, fingerprintRows, keywordBytes, fingerprintBytes int64
	if err := db.QueryRowContext(ctx, terminalKeywordInventorySQL).Scan(&keywordRows, &keywordBytes); err != nil {
		return step, xerrors.Errorf("measure terminal keyword rows: %w", err)
	}
	if err := db.QueryRowContext(ctx, terminalFingerprintInventorySQL).Scan(&fingerprintRows, &fingerprintBytes); err != nil {
		return step, xerrors.Errorf("measure terminal fingerprint rows: %w", err)
	}
	var deleted, logical int64
	for _, g := range gens {
		for {
			progress, pageErr := reclaimTerminalGenerationPageOn(ctx, db, g.GenerationID, pageRows, now)
			if pageErr != nil {
				return step, pageErr
			}
			deleted += progress.Deleted
			logical += progress.LogicalBytes
			if progress.Done {
				break
			}
		}
	}
	step.Rows = deleted
	step.BytesBefore = logical
	step.BytesAfter = 0
	step.BytesReclaimed = logical
	step.Detail["generations"] = int64(len(gens))
	step.Detail["keyword_rows"] = keywordRows
	step.Detail["fingerprint_rows"] = fingerprintRows
	return step, nil
}

func reclaimTerminalProjectionGenerationsStep(ctx context.Context, db *sql.DB, filter application.CompactFilter) error {
	hasEvents, err := tableExists(ctx, db, "events")
	if err != nil {
		return err
	}
	if !hasEvents {
		filter.Report(application.CompactStep{
			Name:    application.CompactStepProjectionReclaim,
			Skipped: "no events table",
			Detail:  map[string]int64{},
		})
		return nil
	}
	hasLifecycle, err := tableExists(ctx, db, "search_projection_generation_lifecycle")
	if err != nil {
		return err
	}
	if !hasLifecycle {
		filter.Report(application.CompactStep{
			Name:    application.CompactStepProjectionReclaim,
			Skipped: "no search projection lifecycle",
			Detail:  map[string]int64{},
		})
		return nil
	}
	step, err := reclaimTerminalProjectionGenerationsOn(ctx, db, apptypes.SearchProjectionTerminalReclaimPageRows, time.Now().UTC())
	if err != nil {
		return err
	}
	filter.Report(step)
	return nil
}

// ListTerminalGenerations returns failed/abandoned generations eligible for reclaim.
func (d *Database) ListTerminalGenerations(ctx context.Context) ([]apptypes.SearchProjectionTerminalGeneration, error) {
	db, err := d.openReadOnly(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = db.Close() }()
	return listTerminalGenerationsOn(ctx, db)
}

// ReclaimTerminalGenerationPage deletes one lock-bounded page of a terminal generation.
func (d *Database) ReclaimTerminalGenerationPage(ctx context.Context, generation string, b apptypes.SearchProjectionBudget, pageRows int, now time.Time) (apptypes.SearchProjectionReclaimProgress, error) {
	db, err := d.open(ctx)
	if err != nil {
		return apptypes.SearchProjectionReclaimProgress{}, err
	}
	defer func() { _ = db.Close() }()
	hold := b.LockTime
	if hold <= 0 {
		hold = apptypes.DefaultSearchProjectionLockTime
	}
	holdCtx, cancel := context.WithTimeout(ctx, hold)
	defer cancel()
	if pageRows < 1 {
		pageRows = apptypes.SearchProjectionTerminalReclaimPageRows
	}
	progress, err := reclaimTerminalGenerationPageOn(holdCtx, db, generation, pageRows, now)
	if err != nil {
		if isSearchProjectionDeadline(err) {
			return apptypes.SearchProjectionReclaimProgress{}, &apptypes.SearchProjectionNoProgressError{Code: apptypes.SearchProjectionNoProgressLockDurationCap, Reason: "projection lock acquisition exceeded"}
		}
		return apptypes.SearchProjectionReclaimProgress{}, err
	}
	return progress, nil
}

func measureTerminalProjectionInventory(ctx context.Context, queryer statusQueryer, s *apptypes.SearchProjectionStatus) error {
	var name string
	err := queryer.QueryRowContext(ctx, `SELECT name FROM sqlite_master WHERE type = 'table' AND name = 'search_projection_generation_lifecycle'`).Scan(&name)
	if err == sql.ErrNoRows {
		return nil
	}
	if err != nil {
		return xerrors.Errorf("inspect search projection lifecycle table: %w", err)
	}
	evidence, err := scanTerminalGenerations(ctx, queryer, listTerminalGenerationEvidenceSQL)
	if err != nil {
		return err
	}
	s.TerminalGenerationEvidence = evidence
	unreclaimed := 0
	for _, g := range evidence {
		if g.ReclaimedAt == "" {
			unreclaimed++
		}
	}
	s.TerminalGenerations = unreclaimed
	if err := queryer.QueryRowContext(ctx, terminalKeywordInventorySQL).Scan(&s.TerminalKeywordRows, &s.TerminalKeywordLogicalBytes); err != nil {
		return xerrors.Errorf("count terminal session keywords: %w", err)
	}
	if err := queryer.QueryRowContext(ctx, terminalFingerprintInventorySQL).Scan(&s.TerminalFingerprintRows, &s.TerminalFingerprintLogicalBytes); err != nil {
		return xerrors.Errorf("count terminal fingerprints: %w", err)
	}
	return nil
}
