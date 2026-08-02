//nolint:wrapcheck // SQLite search errors are contextualized at query boundaries.
package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"strings"

	"golang.org/x/xerrors"

	apptypes "github.com/duck8823/traceary/application/types"
	"github.com/duck8823/traceary/domain/model"
)

// searchByPersistedAuthority is the only normal full-event authority selector.
// CLI and MCP both reach it through EventUsecase, so presentation flags cannot
// accidentally choose a storage implementation.
func (d *EventDatasource) searchByPersistedAuthority(ctx context.Context, criteria apptypes.EventSearchCriteria) ([]*model.Event, error) {
	db, err := d.db.open(ctx)
	if err != nil {
		return nil, xerrors.Errorf("open search authority: %w", err)
	}
	defer d.db.release(db)
	var authority string
	err = db.QueryRowContext(ctx, `SELECT authority FROM search_maintenance_control WHERE singleton=1`).Scan(&authority)
	if err != nil {
		// Focused historical-schema tests and pre-v40 readers preserve legacy.
		if errors.Is(err, sql.ErrNoRows) || strings.Contains(err.Error(), "no such table") {
			return d.searchLegacyPageAware(ctx, criteria)
		}
		return nil, xerrors.Errorf("read persisted search authority: %w", err)
	}
	if authority == "legacy" {
		return d.searchLegacyPageAware(ctx, criteria)
	}
	if authority != "tiered" {
		return nil, xerrors.Errorf("unsupported persisted search authority %q", authority)
	}
	return d.searchTieredCanonical(ctx, criteria)
}

// searchTieredCanonical verifies literal membership against codec-decoded
// canonical history. Fingerprints/recent FTS are accelerators only; selecting
// from the source sequence keeps the result complete after legacy retirement.
func (d *EventDatasource) searchTieredCanonical(ctx context.Context, criteria apptypes.EventSearchCriteria) ([]*model.Event, error) {
	query := apptypes.CharacterizeLiteralQuery(criteria.Query())
	if len(query.Canonical()) > apptypes.MaxLiteralSearchQueryBytes {
		return nil, apptypes.ErrLiteralSearchQueryTooLarge
	}
	if query.Canonical() == "" {
		return nil, xerrors.New("tiered search requires a non-empty literal query")
	}
	db, tx, err := d.beginEventProjectionRead(ctx, "tiered canonical search")
	if err != nil {
		return nil, err
	}
	defer closeEventProjectionRead(db, tx)
	var generation, literalState, projectionState, activeGeneration string
	var projectionHigh, sourceHigh int64
	if err = tx.QueryRowContext(ctx, `SELECT generation_id,high_water,state FROM literal_search_projection_state WHERE singleton=1`).Scan(&generation, &projectionHigh, &literalState); err != nil {
		return nil, xerrors.Errorf("read tiered literal state: %w", err)
	}
	if err = tx.QueryRowContext(ctx, `SELECT state,COALESCE(active_generation_id,'') FROM search_projection_state WHERE singleton=1`).Scan(&projectionState, &activeGeneration); err != nil {
		return nil, xerrors.Errorf("read tiered projection state: %w", err)
	}
	if err = tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(sequence),0) FROM search_projection_source_sequence`).Scan(&sourceHigh); err != nil {
		return nil, err
	}
	if literalState != "complete" || projectionState != "complete" || generation == "" || generation != activeGeneration || projectionHigh != sourceHigh {
		return nil, xerrors.New("tiered search projection is incomplete or stale")
	}
	rows, err := tx.QueryContext(ctx, `SELECT q.event_id FROM search_projection_source_sequence q JOIN events e ON e.id=q.event_id LEFT JOIN command_audits a ON a.event_id=e.id WHERE (?='' OR e.workspace=?) AND (?='' OR e.session_id=?) AND (?='' OR e.client=?) AND (?='' OR e.agent=?) AND (?='' OR e.kind=?) AND (?='' OR ts_norm(e.created_at)>=ts_norm(?)) AND (?='' OR ts_norm(e.created_at)<ts_norm(?)) AND (?=0 OR COALESCE(a.failed,0)=1) AND (?='' OR ts_norm(e.created_at)<ts_norm(?) OR (ts_norm(e.created_at)=ts_norm(?) AND e.id<?)) ORDER BY ts_norm(e.created_at) DESC,e.id DESC`, criteria.Workspace().String(), criteria.Workspace().String(), criteria.SessionID().String(), criteria.SessionID().String(), criteria.Client().String(), criteria.Client().String(), criteria.Agent().String(), criteria.Agent().String(), criteria.Kind().String(), criteria.Kind().String(), formatMetadataOptionalTimestamp(criteria.From()), formatMetadataOptionalTimestamp(criteria.From()), formatMetadataOptionalTimestamp(criteria.To()), formatMetadataOptionalTimestamp(criteria.To()), boolToInt(criteria.FailuresOnly()), formatMetadataOptionalTimestamp(criteria.PageAnchor().CreatedAt()), formatMetadataOptionalTimestamp(criteria.PageAnchor().CreatedAt()), formatMetadataOptionalTimestamp(criteria.PageAnchor().CreatedAt()), criteria.PageAnchor().EventID().String())
	if err != nil {
		return nil, xerrors.Errorf("select tiered canonical candidates: %w", err)
	}
	ids := make([]string, 0, criteria.Limit())
	skipped := 0
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			_ = rows.Close()
			return nil, err
		}
		matched, matchErr := decodedEventSearchMatch(ctx, tx, id, query.Canonical())
		if matchErr != nil {
			_ = rows.Close()
			return nil, matchErr
		}
		if !matched {
			continue
		}
		if skipped < criteria.Offset() {
			skipped++
			continue
		}
		ids = append(ids, id)
		if len(ids) == criteria.Limit() {
			break
		}
	}
	if err = rows.Err(); err != nil {
		_ = rows.Close()
		return nil, xerrors.Errorf("iterate tiered canonical candidates: %w", err)
	}
	if closeErr := rows.Close(); closeErr != nil {
		slog.Debug("failed to close tiered candidates", "error", closeErr)
	}
	events, err := hydrateEventSearchCandidates(ctx, tx, ids)
	if err != nil {
		return nil, err
	}
	if err = tx.Commit(); err != nil {
		return nil, xerrors.Errorf("finish tiered canonical search: %w", err)
	}
	return events, nil
}
