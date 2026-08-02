package sqlite

import (
	"context"

	"golang.org/x/xerrors"

	"github.com/duck8823/traceary/application/queryservice"
	apptypes "github.com/duck8823/traceary/application/types"
	"github.com/duck8823/traceary/domain/model"
)

var _ queryservice.EventPageQueryService = (*EventDatasource)(nil)
var _ queryservice.EventReadQueryService = (*EventDatasource)(nil)

// ListRecentPage selects membership and hydrates full events inside one SQLite
// read snapshot. Continuation requests carry the same logical upper bound and
// descending keyset anchor in criteria.
func (d *EventDatasource) ListRecentPage(
	ctx context.Context,
	criteria apptypes.EventListCriteria,
) ([]*model.Event, error) {
	if err := validateMetadataListCriteria(criteria, false); err != nil {
		return nil, err
	}
	db, tx, err := d.beginEventProjectionRead(ctx, "full event page listing")
	if err != nil {
		return nil, err
	}
	defer closeEventProjectionRead(db, tx)

	rows, err := queryRecentEventMetadataTx(
		ctx,
		tx,
		criteria,
		formatMetadataOptionalTimestamp(criteria.From()),
		formatMetadataOptionalTimestamp(criteria.To()),
		criteria.Limit(),
		criteria.Offset(),
	)
	if err != nil {
		return nil, xerrors.Errorf("failed to query full event page membership: %w", err)
	}
	metadata, err := collectEventMetadata(rows, criteria.Limit(), "full event page membership")
	if err != nil {
		return nil, err
	}
	events, err := hydrateFullEventMetadata(ctx, tx, metadata)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, xerrors.Errorf("failed to finish full event page listing: %w", err)
	}
	return events, nil
}

// SearchPage applies the FTS or bounded legacy candidate planner before LIMIT,
// then hydrates exactly those IDs in the same SQLite read snapshot.
func (d *EventDatasource) SearchPage(
	ctx context.Context,
	criteria apptypes.EventSearchCriteria,
) ([]*model.Event, error) {
	if criteria.Limit() <= 0 {
		return nil, xerrors.Errorf("limit must be greater than or equal to 1")
	}
	if criteria.Offset() < 0 {
		return nil, xerrors.Errorf("offset must be greater than or equal to 0")
	}
	if !criteria.PageAnchor().IsZero() && criteria.Offset() != 0 {
		return nil, xerrors.Errorf("event page anchor cannot be combined with offset")
	}
	if !criteria.From().IsZero() && !criteria.To().IsZero() && criteria.From().After(criteria.To()) {
		return nil, xerrors.Errorf("from must be earlier than to")
	}
	return d.searchByPersistedAuthority(ctx, criteria)
}

func (d *EventDatasource) searchLegacyPageAware(ctx context.Context, criteria apptypes.EventSearchCriteria) ([]*model.Event, error) {
	db, tx, err := d.beginEventProjectionRead(ctx, "full event page search")
	if err != nil {
		return nil, err
	}
	defer closeEventProjectionRead(db, tx)

	searchSchemaAvailable, err := eventSearchSchemaAvailable(ctx, tx)
	if err != nil {
		return nil, err
	}
	var candidateIDs []string
	if searchSchemaAvailable {
		candidateIDs, err = selectEventSearchCandidateIDs(ctx, tx, criteria)
	} else {
		candidateIDs, err = queryLegacyEventIDs(ctx, tx, criteria, criteria.Query())
	}
	if err != nil {
		return nil, err
	}
	events, err := hydrateEventSearchCandidates(ctx, tx, candidateIDs)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, xerrors.Errorf("failed to finish full event page search: %w", err)
	}
	return events, nil
}

// GetContextPage selects context membership and hydrates full events inside one
// SQLite read snapshot.
func (d *EventDatasource) GetContextPage(
	ctx context.Context,
	criteria apptypes.EventContextCriteria,
) ([]*model.Event, error) {
	if criteria.Limit() <= 0 {
		return nil, xerrors.Errorf("limit must be greater than or equal to 1")
	}
	if criteria.Offset() < 0 {
		return nil, xerrors.Errorf("offset must be greater than or equal to 0")
	}
	if !criteria.PageAnchor().IsZero() && criteria.Offset() != 0 {
		return nil, xerrors.Errorf("event page anchor cannot be combined with offset")
	}
	db, tx, err := d.beginEventProjectionRead(ctx, "full context event page")
	if err != nil {
		return nil, err
	}
	defer closeEventProjectionRead(db, tx)

	query, args := contextEventMetadataQuery(
		criteria,
		criteria.Workspace().String(),
		criteria.SessionID().String(),
	)
	rows, err := tx.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, xerrors.Errorf("failed to query full context page membership: %w", err)
	}
	metadata, err := collectEventMetadata(rows, criteria.Limit(), "full context page membership")
	if err != nil {
		return nil, err
	}
	events, err := hydrateFullEventMetadata(ctx, tx, metadata)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, xerrors.Errorf("failed to finish full context event page: %w", err)
	}
	return events, nil
}

func hydrateFullEventMetadata(
	ctx context.Context,
	queryer eventSearchQueryer,
	metadata []apptypes.EventMetadata,
) ([]*model.Event, error) {
	ids := make([]string, 0, len(metadata))
	for _, event := range metadata {
		ids = append(ids, event.EventID().String())
	}
	return hydrateEventSearchCandidates(ctx, queryer, ids)
}
