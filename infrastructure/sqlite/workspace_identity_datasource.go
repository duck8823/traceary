package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"sort"
	"time"

	"golang.org/x/xerrors"

	"github.com/duck8823/traceary/application/queryservice"
	apptypes "github.com/duck8823/traceary/application/types"
	"github.com/duck8823/traceary/domain/model"
	"github.com/duck8823/traceary/domain/types"
)

// WorkspaceIdentityDatasource serves body-free diagnostics and reviewed aliases.
type WorkspaceIdentityDatasource struct {
	db *Database
}

// NewWorkspaceIdentityDatasource constructs the workspace diagnostics adapter.
func NewWorkspaceIdentityDatasource(db *Database) *WorkspaceIdentityDatasource {
	return &WorkspaceIdentityDatasource{db: db}
}

var (
	_ queryservice.WorkspaceIdentityQueryService = (*WorkspaceIdentityDatasource)(nil)
	_ model.WorkspaceAliasRepository             = (*WorkspaceIdentityDatasource)(nil)
)

// WorkspaceIdentityReport returns body-free attribution and delivery metrics.
func (d *WorkspaceIdentityDatasource) WorkspaceIdentityReport(ctx context.Context, conflictSampleLimit int) (apptypes.WorkspaceIdentityReport, error) {
	db, err := d.db.open(ctx)
	if err != nil {
		return apptypes.WorkspaceIdentityReport{}, xerrors.Errorf("failed to open DB for workspace identity report: %w", err)
	}
	defer func() {
		if err := db.Close(); err != nil {
			slog.Debug("failed to close workspace identity report DB", "error", err)
		}
	}()

	tx, err := db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return apptypes.WorkspaceIdentityReport{}, xerrors.Errorf("failed to begin workspace identity report: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	report := apptypes.WorkspaceIdentityReport{}
	if err := readWorkspaceIdentityCoverage(ctx, tx, &report.Coverage); err != nil {
		return apptypes.WorkspaceIdentityReport{}, err
	}
	sources, err := readWorkspaceIdentitySources(ctx, tx)
	if err != nil {
		return apptypes.WorkspaceIdentityReport{}, err
	}
	report.Sources = sources
	report.ConflictPairCount, err = readWorkspaceConflictPairCount(ctx, tx)
	if err != nil {
		return apptypes.WorkspaceIdentityReport{}, err
	}
	report.ConflictSamples, err = readWorkspaceConflictSamples(ctx, tx, conflictSampleLimit)
	if err != nil {
		return apptypes.WorkspaceIdentityReport{}, err
	}
	report.Aliases, err = readWorkspaceAliases(ctx, tx)
	if err != nil {
		return apptypes.WorkspaceIdentityReport{}, err
	}
	if err := tx.Commit(); err != nil {
		return apptypes.WorkspaceIdentityReport{}, xerrors.Errorf("failed to commit workspace identity report: %w", err)
	}
	return report, nil
}

func readWorkspaceIdentityCoverage(ctx context.Context, tx *sql.Tx, coverage *apptypes.WorkspaceIdentityCoverage) error {
	aggregated, err := columnExistsInTransaction(ctx, tx, "session_workspace_observations", "observation_count")
	if err != nil {
		return err
	}
	coverage.PreCollapse = !aggregated
	if !aggregated {
		if err := tx.QueryRowContext(ctx, `
			SELECT
				(SELECT COUNT(*) FROM events),
				(SELECT COUNT(*)
				   FROM session_workspace_observations o
				   JOIN events e ON e.id = o.observed_event_id
				  WHERE o.observation_kind = 'primary'
				    AND o.observed_event_id IS NOT NULL
				    AND o.observed_event_id <> ''),
				(SELECT COUNT(*) FROM session_workspace_observations),
				(SELECT COUNT(*) FROM (
					SELECT 1 FROM session_workspace_observations
					 GROUP BY session_id, workspace, observed_relationship, source_client, source_hook, observation_kind
				))
		`).Scan(&coverage.EventCount, &coverage.CoveredEvents, &coverage.ObservationCount, &coverage.ObservationKeys); err != nil {
			return xerrors.Errorf("failed to read workspace identity coverage: %w", err)
		}
		coverage.ObservationRows = coverage.ObservationCount
		coverage.MissingEvents = coverage.EventCount - coverage.CoveredEvents
		coverage.CoverageRate = ratio(coverage.CoveredEvents, coverage.EventCount)
		return readWorkspaceIdentityOrphans(ctx, tx, coverage)
	}

	if err := tx.QueryRowContext(ctx, `
		SELECT
			(SELECT COUNT(*) FROM events),
			(SELECT COUNT(*) FROM session_workspace_observations),
			(SELECT COALESCE(SUM(observation_count), 0) FROM session_workspace_observations)
	`).Scan(&coverage.EventCount, &coverage.ObservationRows, &coverage.ObservationCount); err != nil {
		return xerrors.Errorf("failed to read workspace identity coverage: %w", err)
	}
	coverage.ObservationKeys = coverage.ObservationRows
	if err := readWorkspaceIdentityCoveredEvents(ctx, tx, coverage); err != nil {
		return err
	}
	coverage.MissingEvents = coverage.EventCount - coverage.CoveredEvents
	coverage.CoverageRate = ratio(coverage.CoveredEvents, coverage.EventCount)
	return readWorkspaceIdentityOrphans(ctx, tx, coverage)
}

func readWorkspaceIdentityCoveredEvents(ctx context.Context, tx *sql.Tx, coverage *apptypes.WorkspaceIdentityCoverage) error {
	var exhausted int
	frontierNorm, frontierID := "", ""
	var stateExists int
	if err := tx.QueryRowContext(
		ctx,
		`SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = 'workspace_observation_catchup_state'`,
	).Scan(&stateExists); err != nil {
		return xerrors.Errorf("failed to inspect workspace observation catch-up state: %w", err)
	}
	if stateExists == 0 {
		coverage.CoveredEvents = 0
		return nil
	}
	hasFrontier, err := columnExistsInTransaction(ctx, tx, "workspace_observation_catchup_state", "frontier_created_at_norm")
	if err != nil {
		return err
	}
	if hasFrontier {
		err = tx.QueryRowContext(
			ctx,
			`SELECT exhausted, frontier_created_at_norm, frontier_event_id
			   FROM workspace_observation_catchup_state WHERE singleton = 1`,
		).Scan(&exhausted, &frontierNorm, &frontierID)
	} else {
		err = tx.QueryRowContext(ctx, `SELECT exhausted FROM workspace_observation_catchup_state WHERE singleton = 1`).Scan(&exhausted)
	}
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			coverage.CoveredEvents = 0
			return nil
		}
		return xerrors.Errorf("failed to read workspace observation catch-up state: %w", err)
	}
	if exhausted == 1 {
		coverage.CoveredEvents = coverage.EventCount
		return nil
	}
	if frontierNorm == "" {
		coverage.CoveredEvents = 0
		return nil
	}
	if err := tx.QueryRowContext(
		ctx,
		`SELECT COUNT(*) FROM events WHERE (created_at_norm, id) >= (?, ?)`,
		frontierNorm,
		frontierID,
	).Scan(&coverage.CoveredEvents); err != nil {
		return xerrors.Errorf("failed to count frontier-covered events: %w", err)
	}
	return nil
}

func readWorkspaceIdentityOrphans(ctx context.Context, tx *sql.Tx, coverage *apptypes.WorkspaceIdentityCoverage) error {
	if err := tx.QueryRowContext(ctx, countUnreachableWorkspaceObservationsQuery).Scan(&coverage.OrphanObservationRows); err != nil {
		return xerrors.Errorf("failed to count unreachable workspace observations: %w", err)
	}
	return nil
}

type workspaceIdentitySourceKey struct {
	client string
	hook   string
}

func readWorkspaceIdentitySources(ctx context.Context, tx *sql.Tx) ([]apptypes.WorkspaceIdentitySourceReport, error) {
	bySource := map[workspaceIdentitySourceKey]*apptypes.WorkspaceIdentitySourceReport{}
	volumeExpr := "1"
	aggregated, err := columnExistsInTransaction(ctx, tx, "session_workspace_observations", "observation_count")
	if err != nil {
		return nil, err
	}
	if aggregated {
		volumeExpr = "observation_count"
	}
	rows, err := tx.QueryContext(ctx, `
		SELECT source_client, source_hook, SUM(observation_count),
		       SUM(CASE WHEN current_relationship = 'exact' THEN observation_count ELSE 0 END),
		       SUM(CASE WHEN current_relationship = 'descendant' THEN observation_count ELSE 0 END),
		       SUM(CASE WHEN current_relationship = 'ancestor' THEN observation_count ELSE 0 END),
		       SUM(CASE WHEN current_relationship = 'explicit_alias' THEN observation_count ELSE 0 END),
		       SUM(CASE WHEN current_relationship = 'conflict' THEN observation_count ELSE 0 END),
		       SUM(CASE WHEN current_relationship = 'unknown' THEN observation_count ELSE 0 END),
		       SUM(CASE WHEN ingested_relationship = 'conflict' THEN observation_count ELSE 0 END)
		  FROM (
			SELECT o.source_client, o.source_hook, o.session_id, o.workspace,
			       o.observed_relationship AS ingested_relationship,
			       `+volumeExpr+` AS observation_count,
			       CASE
			         WHEN o.observed_relationship = 'conflict' AND EXISTS (
			           SELECT 1 FROM session_workspace_aliases a
			            WHERE a.session_id = o.session_id AND a.alias_workspace = o.workspace
			         ) THEN 'explicit_alias'
			         ELSE o.observed_relationship
			       END AS current_relationship
			  FROM session_workspace_observations o
		  )
		 GROUP BY source_client, source_hook`)
	if err != nil {
		return nil, xerrors.Errorf("failed to query workspace relationship sources: %w", err)
	}
	for rows.Next() {
		var item apptypes.WorkspaceIdentitySourceReport
		if err := rows.Scan(
			&item.Client, &item.SourceHook, &item.ObservationCount,
			&item.Relationships.Exact, &item.Relationships.Descendant,
			&item.Relationships.Ancestor, &item.Relationships.ExplicitAlias,
			&item.Relationships.Conflict, &item.Relationships.Unknown,
			&item.IngestedConflictCount,
		); err != nil {
			_ = rows.Close()
			return nil, xerrors.Errorf("failed to scan workspace relationship source: %w", err)
		}
		item.KnownRelationshipCount = item.ObservationCount - item.Relationships.Unknown
		item.ConflictRate = ratio(item.Relationships.Conflict, item.KnownRelationshipCount)
		key := workspaceIdentitySourceKey{client: item.Client, hook: item.SourceHook}
		itemCopy := item
		bySource[key] = &itemCopy
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return nil, xerrors.Errorf("failed to iterate workspace relationship sources: %w", err)
	}
	if err := rows.Close(); err != nil {
		return nil, xerrors.Errorf("failed to close workspace relationship sources: %w", err)
	}

	if err := readWorkspaceIdentitySourceConflictPairs(ctx, tx, bySource); err != nil {
		return nil, err
	}

	rows, err = tx.QueryContext(ctx, `
		SELECT d.source_client, d.source_hook, COUNT(*),
		       SUM(a.attempt_origin = 'runtime'),
		       SUM(a.attempt_origin = 'backfill'),
		       SUM(a.outcome = 'accepted'),
		       SUM(a.outcome = 'conflict'),
		       SUM(a.attempt_origin = 'runtime' AND a.outcome = 'exact_redelivery')
		  FROM hook_delivery_attempts a
		  JOIN hook_deliveries d ON d.delivery_record_id = a.delivery_record_id
		 GROUP BY d.source_client, d.source_hook`)
	if err != nil {
		return nil, xerrors.Errorf("failed to query hook delivery attempt sources: %w", err)
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var client, hook string
		var attempts, runtimeAttempts, backfilledAttempts, accepted, conflicts, exact int
		if err := rows.Scan(&client, &hook, &attempts, &runtimeAttempts, &backfilledAttempts, &accepted, &conflicts, &exact); err != nil {
			return nil, xerrors.Errorf("failed to scan hook delivery attempt source: %w", err)
		}
		key := workspaceIdentitySourceKey{client: client, hook: hook}
		item := bySource[key]
		if item == nil {
			item = &apptypes.WorkspaceIdentitySourceReport{Client: client, SourceHook: hook}
			bySource[key] = item
		}
		item.DeliveryAttemptCount = attempts
		item.RuntimeAttemptCount = runtimeAttempts
		item.BackfilledAttemptCount = backfilledAttempts
		item.AcceptedDeliveryCount = accepted
		item.IdentityConflictCount = conflicts
		item.ExactRedeliveryCount = exact
		item.ExactRedeliveryRate = ratio(exact, runtimeAttempts)
	}
	if err := rows.Err(); err != nil {
		return nil, xerrors.Errorf("failed to iterate hook delivery attempt sources: %w", err)
	}

	result := make([]apptypes.WorkspaceIdentitySourceReport, 0, len(bySource))
	for _, item := range bySource {
		result = append(result, *item)
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].Client != result[j].Client {
			return result[i].Client < result[j].Client
		}
		return result[i].SourceHook < result[j].SourceHook
	})
	return result, nil
}

func readWorkspaceIdentitySourceConflictPairs(
	ctx context.Context,
	tx *sql.Tx,
	bySource map[workspaceIdentitySourceKey]*apptypes.WorkspaceIdentitySourceReport,
) error {
	rows, err := tx.QueryContext(ctx, `
		SELECT source_client, source_hook, COUNT(*)
		  FROM (
			SELECT DISTINCT o.source_client, o.source_hook, o.session_id, o.workspace
			  FROM session_workspace_observations o
			 WHERE o.observed_relationship = 'conflict'
			   AND NOT EXISTS (
			       SELECT 1 FROM session_workspace_aliases a
			        WHERE a.session_id = o.session_id AND a.alias_workspace = o.workspace
			   )
		  )
		 GROUP BY source_client, source_hook`)
	if err != nil {
		return xerrors.Errorf("failed to query workspace conflict pairs by source: %w", err)
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var client, hook string
		var pairs int
		if err := rows.Scan(&client, &hook, &pairs); err != nil {
			return xerrors.Errorf("failed to scan workspace conflict pairs by source: %w", err)
		}
		key := workspaceIdentitySourceKey{client: client, hook: hook}
		item := bySource[key]
		if item == nil {
			item = &apptypes.WorkspaceIdentitySourceReport{Client: client, SourceHook: hook}
			bySource[key] = item
		}
		item.ConflictPairCount = pairs
	}
	if err := rows.Err(); err != nil {
		return xerrors.Errorf("failed to iterate workspace conflict pairs by source: %w", err)
	}
	return nil
}

func readWorkspaceConflictPairCount(ctx context.Context, tx *sql.Tx) (int, error) {
	var count int
	if err := tx.QueryRowContext(ctx, `
		SELECT COUNT(*)
		  FROM (
			SELECT DISTINCT o.session_id, o.workspace
			  FROM session_workspace_observations o
			 WHERE o.observed_relationship = 'conflict'
			   AND NOT EXISTS (
			       SELECT 1 FROM session_workspace_aliases a
			        WHERE a.session_id = o.session_id AND a.alias_workspace = o.workspace
			   )
		  )`).Scan(&count); err != nil {
		return 0, xerrors.Errorf("failed to count workspace conflict pairs: %w", err)
	}
	return count, nil
}

func readWorkspaceConflictSamples(ctx context.Context, tx *sql.Tx, limit int) ([]apptypes.WorkspaceConflictSample, error) {
	result := make([]apptypes.WorkspaceConflictSample, 0)
	if limit == 0 {
		return result, nil
	}
	aggregated, err := columnExistsInTransaction(ctx, tx, "session_workspace_observations", "observation_count")
	if err != nil {
		return nil, err
	}
	sampleQuery := `
		SELECT observed_event_id, session_id, workspace, source_client, source_hook
		  FROM (
			SELECT o.observed_event_id, o.session_id, o.workspace, o.source_client, o.source_hook,
			       o.observed_at,
			       ROW_NUMBER() OVER (
			         PARTITION BY o.session_id, o.workspace
			         ORDER BY ts_norm(o.observed_at) DESC, o.observation_id DESC
			       ) AS rn
			  FROM session_workspace_observations o
			 WHERE o.observed_relationship = 'conflict'
			   AND o.observed_event_id IS NOT NULL AND o.observed_event_id <> ''
			   AND NOT EXISTS (
			       SELECT 1 FROM session_workspace_aliases a
			        WHERE a.session_id = o.session_id AND a.alias_workspace = o.workspace
			   )
		  )
		 WHERE rn = 1
		 ORDER BY ts_norm(observed_at) DESC, session_id, workspace
		 LIMIT ?`
	if aggregated {
		sampleQuery = `
		SELECT observed_event_id, session_id, workspace, source_client, source_hook
		  FROM (
			SELECT o.observed_event_id, o.session_id, o.workspace, o.source_client, o.source_hook,
			       o.last_observed_at,
			       ROW_NUMBER() OVER (
			         PARTITION BY o.session_id, o.workspace
			         ORDER BY ts_norm(o.last_observed_at) DESC, o.source_client DESC, o.source_hook DESC
			       ) AS rn
			  FROM session_workspace_observations o
			 WHERE o.observed_relationship = 'conflict'
			   AND o.observed_event_id IS NOT NULL AND o.observed_event_id <> ''
			   AND NOT EXISTS (
			       SELECT 1 FROM session_workspace_aliases a
			        WHERE a.session_id = o.session_id AND a.alias_workspace = o.workspace
			   )
		  )
		 WHERE rn = 1
		 ORDER BY ts_norm(last_observed_at) DESC, session_id, workspace
		 LIMIT ?`
	}
	rows, err := tx.QueryContext(ctx, sampleQuery, limit)
	if err != nil {
		return nil, xerrors.Errorf("failed to query workspace conflict samples: %w", err)
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var item apptypes.WorkspaceConflictSample
		if err := rows.Scan(&item.EventID, &item.SessionID, &item.Workspace, &item.Client, &item.SourceHook); err != nil {
			return nil, xerrors.Errorf("failed to scan workspace conflict sample: %w", err)
		}
		result = append(result, item)
	}
	if err := rows.Err(); err != nil {
		return nil, xerrors.Errorf("failed to iterate workspace conflict samples: %w", err)
	}
	return result, nil
}

func readWorkspaceAliases(ctx context.Context, tx *sql.Tx) ([]apptypes.WorkspaceAliasSummary, error) {
	rows, err := tx.QueryContext(ctx, `
		SELECT session_id, alias_workspace, reviewed_at, reviewed_by, note
		  FROM session_workspace_aliases
		 ORDER BY ts_norm(reviewed_at) DESC, session_id, alias_workspace`)
	if err != nil {
		return nil, xerrors.Errorf("failed to query workspace aliases: %w", err)
	}
	defer func() { _ = rows.Close() }()
	result := make([]apptypes.WorkspaceAliasSummary, 0)
	for rows.Next() {
		var item apptypes.WorkspaceAliasSummary
		var reviewedAt string
		if err := rows.Scan(&item.SessionID, &item.Workspace, &reviewedAt, &item.ReviewedBy, &item.Note); err != nil {
			return nil, xerrors.Errorf("failed to scan workspace alias: %w", err)
		}
		parsed, err := time.Parse(time.RFC3339Nano, reviewedAt)
		if err != nil {
			return nil, xerrors.Errorf("failed to parse workspace alias review time: %w", err)
		}
		item.ReviewedAt = parsed
		result = append(result, item)
	}
	if err := rows.Err(); err != nil {
		return nil, xerrors.Errorf("failed to iterate workspace aliases: %w", err)
	}
	return result, nil
}

// SaveWorkspaceAlias inserts or updates an explicit operator-reviewed alias.
func (d *WorkspaceIdentityDatasource) SaveWorkspaceAlias(ctx context.Context, alias *model.WorkspaceAlias) error {
	if alias == nil {
		return xerrors.Errorf("workspace alias must not be nil")
	}
	db, err := d.db.open(ctx)
	if err != nil {
		return xerrors.Errorf("failed to open DB for workspace alias save: %w", err)
	}
	defer func() { _ = db.Close() }()
	if _, err := db.ExecContext(ctx, `
		INSERT INTO session_workspace_aliases (
			session_id, alias_workspace, reviewed_at, reviewed_by, note
		) VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(session_id, alias_workspace) DO UPDATE SET
			reviewed_at = excluded.reviewed_at,
			reviewed_by = excluded.reviewed_by,
			note = excluded.note`,
		alias.SessionID().String(), alias.Workspace().String(), formatTimestamp(alias.ReviewedAt()), alias.ReviewedBy(), alias.Note(),
	); err != nil {
		return xerrors.Errorf("failed to persist workspace alias: %w", err)
	}
	return nil
}

// DeleteWorkspaceAlias removes one explicit reviewed alias decision.
func (d *WorkspaceIdentityDatasource) DeleteWorkspaceAlias(ctx context.Context, sessionID types.SessionID, workspace types.Workspace) error {
	db, err := d.db.open(ctx)
	if err != nil {
		return xerrors.Errorf("failed to open DB for workspace alias delete: %w", err)
	}
	defer func() { _ = db.Close() }()
	if _, err := db.ExecContext(ctx, `
		DELETE FROM session_workspace_aliases
		 WHERE session_id = ? AND alias_workspace = ?`, sessionID.String(), workspace.String()); err != nil {
		return xerrors.Errorf("failed to delete workspace alias: %w", err)
	}
	return nil
}

func ratio(numerator, denominator int) float64 {
	if denominator <= 0 {
		return 0
	}
	return float64(numerator) / float64(denominator)
}
