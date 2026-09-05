package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/duck8823/traceary/domain"
)

var upgradeConservationTables = []string{
	"events",
	"sessions",
	"command_audits",
	"memories",
	"session_refinements",
}

var upgradeIndexLaws = map[int64][]indexConservation{
	35: {
		{Index: "idx_event_metadata_kind_created_at_norm_id_desc", BaseTable: "event_metadata_projection"},
		{Index: "idx_event_metadata_boundary_time_context", BaseTable: "event_metadata_projection"},
	},
	45: {
		{Index: "idx_raw_body_retention_entries_event_id", BaseTable: "raw_body_retention_entries"},
	},
}

type indexConservation struct {
	Index     string
	BaseTable string
}

// VerifyUpgradePair extends VerifyPair with five-table conservation, per-migration
// Layer-1 laws, and the Layer-2 verifier for historical version 76. It keeps
// PRAGMA integrity_check.
func (v PreparedMigrationVerifier) VerifyUpgradePair(ctx context.Context, source, candidate, planDigest string) (domain.PreparedCandidateEvidence, error) {
	evidence, err := v.VerifyPair(ctx, source, candidate, planDigest)
	if err != nil {
		return evidence, err
	}
	sourceDB, err := openDirectReadOnly(ctx, source)
	if err != nil {
		return domain.PreparedCandidateEvidence{}, errors.New("open source for upgrade verification")
	}
	defer func() { _ = sourceDB.Close() }()
	candidateDB, err := openDirectReadOnly(ctx, candidate)
	if err != nil {
		return domain.PreparedCandidateEvidence{}, errors.New("open candidate for upgrade verification")
	}
	defer func() { _ = candidateDB.Close() }()
	plan, err := BuildPreparedMigrationPlan(ctx, sourceDB, v.Migrations)
	if err != nil {
		return domain.PreparedCandidateEvidence{}, err
	}
	skipEvents := pendingHasRestoreDedupeArchive(plan)
	skipCodecRewrite := pendingHasDecodePayloads(plan)
	if err = verifyFiveTableConservation(ctx, sourceDB, candidateDB, skipEvents, skipCodecRewrite); err != nil {
		return domain.PreparedCandidateEvidence{}, err
	}
	for _, migration := range plan.Pending {
		if err = evaluateConservationLaw(ctx, sourceDB, candidateDB, migration.Version, skipCodecRewrite); err != nil {
			return domain.PreparedCandidateEvidence{}, err
		}
		switch semanticVerifierFor(migration.Version) {
		case SemanticVerifierCollapseSessionWorkspaceObservations:
			if err = verifyCollapseSessionWorkspaceObservations(ctx, sourceDB, candidateDB); err != nil {
				return domain.PreparedCandidateEvidence{}, err
			}
		case SemanticVerifierRepairEpochZeroHookUsage:
			if err = verifyRepairEpochZeroHookUsage(ctx, sourceDB, candidateDB); err != nil {
				return domain.PreparedCandidateEvidence{}, err
			}
		case SemanticVerifierDropRetiredTable:
			if err = verifyDropRetiredTable(ctx, sourceDB, candidateDB); err != nil {
				return domain.PreparedCandidateEvidence{}, err
			}
		case SemanticVerifierDropSearchProjectionFamily:
			if err = verifyDropSearchProjectionFamily(ctx, sourceDB, candidateDB, skipEvents, skipCodecRewrite); err != nil {
				return domain.PreparedCandidateEvidence{}, err
			}
		case SemanticVerifierDropDedupeArchive:
			if err = verifyDropDedupeArchive(ctx, sourceDB, candidateDB, skipCodecRewrite); err != nil {
				return domain.PreparedCandidateEvidence{}, err
			}
		case SemanticVerifierDropEncodedPayloads:
			if err = verifyDropEncodedPayloads(ctx, sourceDB, candidateDB, candidate); err != nil {
				return domain.PreparedCandidateEvidence{}, err
			}
		}
	}
	return evidence, nil
}

func verifyFiveTableConservation(ctx context.Context, sourceDB, candidateDB *sql.DB, skipEvents, skipCodecRewrite bool) error {
	for _, table := range upgradeConservationTables {
		if skipEvents && table == "events" {
			continue
		}
		if skipCodecRewrite && (table == "events" || table == "command_audits") {
			continue
		}
		sourceHas, err := tableExists(ctx, sourceDB, table)
		if err != nil {
			return err
		}
		candidateHas, err := tableExists(ctx, candidateDB, table)
		if err != nil {
			return err
		}
		if !sourceHas || !candidateHas {
			continue
		}
		if err = verifyTableCountAndDigest(ctx, sourceDB, candidateDB, table); err != nil {
			return err
		}
	}
	return nil
}

func verifyTableCountAndDigest(ctx context.Context, sourceDB, candidateDB *sql.DB, table string) error {
	columns, err := tableColumns(ctx, sourceDB, table)
	if err != nil {
		return err
	}
	query := `SELECT ` + joinQuotedIdentifiers(columns) + ` FROM ` + quoteIdentifier(table)
	srcCount, srcDigest, err := digestRows(ctx, sourceDB, columns, query)
	if err != nil {
		return fmt.Errorf("digest source %s: %w", table, err)
	}
	candCount, candDigest, err := digestRows(ctx, candidateDB, columns, query)
	if err != nil {
		return fmt.Errorf("digest candidate %s: %w", table, err)
	}
	if srcCount != candCount || srcDigest != candDigest {
		return fmt.Errorf("candidate changed conserved table %s", table)
	}
	return nil
}

func evaluateConservationLaw(ctx context.Context, sourceDB, candidateDB *sql.DB, version int64, decodePending bool) error {
	law := conservationLawFor(version)
	switch law {
	case ConservationLawBaseConserving:
		return nil
	case ConservationLawIndexPresentBasePreserved:
		for _, spec := range upgradeIndexLaws[version] {
			exists, err := sqliteIndexExists(ctx, candidateDB, spec.Index)
			if err != nil {
				return err
			}
			if !exists {
				return fmt.Errorf("candidate missing required index %s for migration %d", spec.Index, version)
			}
			sourceHas, err := tableExists(ctx, sourceDB, spec.BaseTable)
			if err != nil {
				return err
			}
			candidateHas, err := tableExists(ctx, candidateDB, spec.BaseTable)
			if err != nil {
				return err
			}
			if sourceHas && candidateHas {
				if err = verifyTableCountAndDigest(ctx, sourceDB, candidateDB, spec.BaseTable); err != nil {
					return err
				}
			}
		}
		return nil
	case ConservationLawRewriteCollapse:
		return nil
	case ConservationLawRestoreDedupeArchive:
		return verifyRestoreDedupeArchiveConservation(ctx, sourceDB, candidateDB, decodePending)
	case ConservationLawDecodePayloadsDropCodec:
		return nil
	case "":
		return fmt.Errorf("migration %d has no conservation law", version)
	default:
		return fmt.Errorf("unknown conservation law %q for migration %d", law, version)
	}
}

func verifyCollapseSessionWorkspaceObservations(ctx context.Context, sourceDB, candidateDB *sql.DB) error {
	sourceHas, err := tableExists(ctx, sourceDB, "session_workspace_observations")
	if err != nil {
		return err
	}
	candidateHas, err := tableExists(ctx, candidateDB, "session_workspace_observations")
	if err != nil {
		return err
	}
	if !sourceHas || !candidateHas {
		return errors.New("session_workspace_observations missing for collapse verifier")
	}
	var sourceRows, candidateRows int64
	if err = sourceDB.QueryRowContext(ctx, `SELECT COUNT(*) FROM session_workspace_observations`).Scan(&sourceRows); err != nil {
		return fmt.Errorf("count source observations: %w", err)
	}
	if err = candidateDB.QueryRowContext(ctx, `SELECT COUNT(*) FROM session_workspace_observations`).Scan(&candidateRows); err != nil {
		return fmt.Errorf("count candidate observations: %w", err)
	}
	var sourceKeys int64
	if err = sourceDB.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM (
			SELECT 1 FROM session_workspace_observations
			 GROUP BY session_id, workspace, observed_relationship, source_client, source_hook, observation_kind
		)`).Scan(&sourceKeys); err != nil {
		return fmt.Errorf("count source observation keys: %w", err)
	}
	if candidateRows != sourceKeys {
		return fmt.Errorf("collapsed observation keys = %d, want %d", candidateRows, sourceKeys)
	}
	rows, err := sourceDB.QueryContext(ctx, `
		SELECT session_id, workspace, observed_relationship, source_client, source_hook, observation_kind, COUNT(*)
		  FROM session_workspace_observations
		 GROUP BY session_id, workspace, observed_relationship, source_client, source_hook, observation_kind`)
	if err != nil {
		return fmt.Errorf("query source observation keys: %w", err)
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var sessionID, workspace, rel, client, hook, kind string
		var count int64
		if err = rows.Scan(&sessionID, &workspace, &rel, &client, &hook, &kind, &count); err != nil {
			return fmt.Errorf("scan source observation key: %w", err)
		}
		var got int64
		err = candidateDB.QueryRowContext(ctx, `
			SELECT observation_count FROM session_workspace_observations
			 WHERE session_id=? AND workspace=? AND observed_relationship=? AND source_client=? AND source_hook=? AND observation_kind=?`,
			sessionID, workspace, rel, client, hook, kind).Scan(&got)
		if err != nil {
			return fmt.Errorf("collapsed observation key missing: %w", err)
		}
		if got != count {
			return fmt.Errorf("observation_count=%d, want %d", got, count)
		}
	}
	if err = rows.Err(); err != nil {
		return fmt.Errorf("iterate source observation keys: %w", err)
	}
	exists, err := sqliteIndexExists(ctx, candidateDB, "idx_session_workspace_observations_relationship")
	if err != nil {
		return err
	}
	if !exists {
		return errors.New("collapsed observations missing relationship index")
	}
	for _, dropped := range []string{
		"idx_session_workspace_observations_delivery_attribution",
		"idx_session_workspace_observations_primary_event",
	} {
		still, err := sqliteIndexExists(ctx, candidateDB, dropped)
		if err != nil {
			return err
		}
		if still {
			return fmt.Errorf("collapsed observations retained dropped index %s", dropped)
		}
	}
	_ = sourceRows
	return nil
}

func sqliteIndexExists(ctx context.Context, db *sql.DB, name string) (bool, error) {
	var n int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM sqlite_master WHERE type='index' AND name=?`, name).Scan(&n); err != nil {
		return false, fmt.Errorf("inspect index %s: %w", name, err)
	}
	return n > 0, nil
}

func verifyRepairEpochZeroHookUsage(ctx context.Context, sourceDB, candidateDB *sql.DB) error {
	sourceHas, err := tableExists(ctx, sourceDB, "usage_observations")
	if err != nil {
		return err
	}
	candidateHas, err := tableExists(ctx, candidateDB, "usage_observations")
	if err != nil {
		return err
	}
	if !sourceHas || !candidateHas {
		return errors.New("usage_observations missing for epoch-zero hook usage verifier")
	}
	var sourceRows, candidateRows int64
	if err = sourceDB.QueryRowContext(ctx, `SELECT COUNT(*) FROM usage_observations`).Scan(&sourceRows); err != nil {
		return fmt.Errorf("count source usage_observations: %w", err)
	}
	if err = candidateDB.QueryRowContext(ctx, `SELECT COUNT(*) FROM usage_observations`).Scan(&candidateRows); err != nil {
		return fmt.Errorf("count candidate usage_observations: %w", err)
	}
	if sourceRows != candidateRows {
		return fmt.Errorf("usage_observations row count source=%d candidate=%d", sourceRows, candidateRows)
	}
	remainingRepairable := `
SELECT COUNT(*) FROM usage_observations AS observation
 WHERE ts_norm(observation.observed_at) = ?
   AND ` + epochZeroHookUsageSourceFilter + `
   AND ` + epochZeroHookUsageRepairableSQL
	var remaining int64
	if err = candidateDB.QueryRowContext(ctx, remainingRepairable, epochZeroHookUsageObservedAt).Scan(&remaining); err != nil {
		return fmt.Errorf("count remaining repairable epoch-zero hook usage rows: %w", err)
	}
	if remaining != 0 {
		return fmt.Errorf("candidate still has %d repairable epoch-zero hook usage rows", remaining)
	}
	columns, err := tableColumns(ctx, sourceDB, "usage_observations")
	if err != nil {
		return err
	}
	targetIDs, err := epochZeroHookUsageIDs(ctx, sourceDB)
	if err != nil {
		return err
	}
	nonTargetQuery, args := usageObservationsExcludingIDsQuery(columns, targetIDs)
	srcCount, srcDigest, err := digestRows(ctx, sourceDB, columns, nonTargetQuery, args...)
	if err != nil {
		return fmt.Errorf("digest source non-target usage_observations: %w", err)
	}
	candCount, candDigest, err := digestRows(ctx, candidateDB, columns, nonTargetQuery, args...)
	if err != nil {
		return fmt.Errorf("digest candidate non-target usage_observations: %w", err)
	}
	if srcCount != candCount || srcDigest != candDigest {
		return errors.New("candidate changed non-target usage_observations rows")
	}
	return nil
}

func epochZeroHookUsageIDs(ctx context.Context, db *sql.DB) ([]string, error) {
	rows, err := db.QueryContext(ctx, `
SELECT observation.observation_id
  FROM usage_observations AS observation
 WHERE ts_norm(observation.observed_at) = ?
   AND `+epochZeroHookUsageSourceFilter, epochZeroHookUsageObservedAt)
	if err != nil {
		return nil, fmt.Errorf("list epoch-zero hook usage ids: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var ids []string
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("scan epoch-zero hook usage id: %w", err)
		}
		ids = append(ids, id)
	}
	if err = rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate epoch-zero hook usage ids: %w", err)
	}
	return ids, nil
}

func usageObservationsExcludingIDsQuery(columns, ids []string) (string, []any) {
	query := `SELECT ` + joinQuotedIdentifiers(columns) + ` FROM usage_observations`
	if len(ids) == 0 {
		return query, nil
	}
	placeholders := make([]string, len(ids))
	args := make([]any, len(ids))
	for i, id := range ids {
		placeholders[i] = "?"
		args[i] = id
	}
	return query + ` WHERE observation_id NOT IN (` + strings.Join(placeholders, ",") + `)`, args
}

func identicalObservationCounts(ctx context.Context, sourceDB, candidateDB *sql.DB) error {
	var sourceRows, candidateRows int64
	if err := sourceDB.QueryRowContext(ctx, `SELECT COUNT(*) FROM session_workspace_observations`).Scan(&sourceRows); err != nil {
		return fmt.Errorf("count source observations for identical-counts: %w", err)
	}
	if err := candidateDB.QueryRowContext(ctx, `SELECT COUNT(*) FROM session_workspace_observations`).Scan(&candidateRows); err != nil {
		return fmt.Errorf("count candidate observations for identical-counts: %w", err)
	}
	if sourceRows != candidateRows {
		return fmt.Errorf("identical-counts comparison rejected observation rows source=%d candidate=%d", sourceRows, candidateRows)
	}
	return nil
}
