//nolint:wrapcheck // SQLite search errors are contextualized at query boundaries.
package sqlite

import (
	"context"
	"database/sql"
	"sort"
	"strings"

	"golang.org/x/xerrors"

	"github.com/duck8823/traceary/application/queryservice"
	apptypes "github.com/duck8823/traceary/application/types"
	"github.com/duck8823/traceary/domain/model"
)

func validateSearchCriteriaForAuthority(criteria apptypes.EventSearchCriteria) error {
	if criteria.Limit() <= 0 {
		return xerrors.New("limit must be greater than or equal to 1")
	}
	if criteria.Offset() < 0 {
		return xerrors.New("offset must be greater than or equal to 0")
	}
	if !criteria.PageAnchor().IsZero() && criteria.Offset() != 0 {
		return xerrors.New("event page anchor cannot be combined with offset")
	}
	if !criteria.From().IsZero() && !criteria.To().IsZero() && criteria.From().After(criteria.To()) {
		return xerrors.New("from must be earlier than to")
	}
	return nil
}

// searchMetadataByPersistedAuthority is the shared projection-neutral boundary
// for full, metadata, and bounded normal search surfaces.
func (d *EventDatasource) searchMetadataByPersistedAuthority(ctx context.Context, criteria apptypes.EventSearchCriteria) ([]apptypes.EventMetadata, error) {
	db, tx, err := d.beginEventProjectionRead(ctx, "persisted search authority")
	if err != nil {
		return nil, err
	}
	defer closeEventProjectionRead(db, tx)
	metadata, err := d.searchMetadataByPersistedAuthorityTx(ctx, tx, criteria)
	if err != nil {
		return nil, err
	}
	if err = tx.Commit(); err != nil {
		return nil, xerrors.Errorf("finish persisted search: %w", err)
	}
	return metadata, nil
}

func (d *EventDatasource) searchMetadataByPersistedAuthorityTx(ctx context.Context, tx *sql.Tx, criteria apptypes.EventSearchCriteria) ([]apptypes.EventMetadata, error) {
	var authority string
	if err := tx.QueryRowContext(ctx, `SELECT authority FROM search_maintenance_control WHERE singleton=1`).Scan(&authority); err != nil {
		return nil, xerrors.Errorf("read explicit persisted search authority: %w", err)
	}
	switch authority {
	case "legacy":
		available, err := eventSearchSchemaAvailable(ctx, tx)
		if err != nil {
			return nil, err
		}
		var ids []string
		if available {
			ids, err = selectEventSearchCandidateIDs(ctx, tx, criteria)
		} else {
			ids, err = queryLegacyEventIDs(ctx, tx, criteria, criteria.Query())
		}
		if err != nil {
			return nil, err
		}
		return hydrateEventSearchMetadataCandidates(ctx, tx, criteria, ids)
	case "tiered":
		return d.searchTieredMetadataTx(ctx, tx, criteria)
	default:
		return nil, xerrors.Errorf("unsupported persisted search authority %q", authority)
	}
}

func (d *EventDatasource) readSearchAuthority(ctx context.Context) (string, error) {
	db, err := d.db.openReadOnly(ctx)
	if err != nil {
		return "", xerrors.Errorf("open persisted search authority: %w", err)
	}
	defer d.db.release(db)
	var authority string
	if err = db.QueryRowContext(ctx, `SELECT authority FROM search_maintenance_control WHERE singleton=1`).Scan(&authority); err != nil {
		return "", xerrors.Errorf("read explicit persisted search authority: %w", err)
	}
	return authority, nil
}

func (d *EventDatasource) searchFullByPersistedAuthority(ctx context.Context, criteria apptypes.EventSearchCriteria) ([]*model.Event, error) {
	db, tx, err := d.beginEventProjectionRead(ctx, "full persisted search")
	if err != nil {
		return nil, err
	}
	defer closeEventProjectionRead(db, tx)
	metadata, err := d.searchMetadataByPersistedAuthorityTx(ctx, tx, criteria)
	if err != nil {
		return nil, err
	}
	events, err := hydrateFullEventMetadata(ctx, tx, metadata)
	if err != nil {
		return nil, err
	}
	if err = tx.Commit(); err != nil {
		return nil, xerrors.Errorf("finish full persisted search: %w", err)
	}
	return events, nil
}

func (d *EventDatasource) searchTieredMetadata(ctx context.Context, criteria apptypes.EventSearchCriteria) ([]apptypes.EventMetadata, error) {
	db, tx, err := d.beginEventProjectionRead(ctx, "tiered search metadata")
	if err != nil {
		return nil, err
	}
	defer closeEventProjectionRead(db, tx)
	metadata, err := d.searchTieredMetadataTx(ctx, tx, criteria)
	if err != nil {
		return nil, err
	}
	if err = tx.Commit(); err != nil {
		return nil, xerrors.Errorf("finish tiered search metadata: %w", err)
	}
	return metadata, nil
}

func (d *EventDatasource) searchTieredMetadataTx(ctx context.Context, tx *sql.Tx, criteria apptypes.EventSearchCriteria) ([]apptypes.EventMetadata, error) {
	if criteria.Offset() < 0 {
		return nil, xerrors.New("offset must be greater than or equal to 0")
	}
	var literalState, literalGeneration, boundedState, boundedGeneration string
	var literalHigh, sourceHigh int64
	if err := tx.QueryRowContext(ctx, `SELECT generation_id,high_water,state FROM literal_search_projection_state WHERE singleton=1`).Scan(&literalGeneration, &literalHigh, &literalState); err == nil {
		err = tx.QueryRowContext(ctx, `SELECT COALESCE(active_generation_id,''),state FROM search_projection_state WHERE singleton=1`).Scan(&boundedGeneration, &boundedState)
		if err == nil {
			err = tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(sequence),0) FROM search_projection_source_sequence`).Scan(&sourceHigh)
		}
		if err != nil {
			return nil, xerrors.Errorf("read tiered authority projection state: %w", err)
		}
	} else {
		return nil, xerrors.Errorf("read tiered authority projection state: %w", err)
	}
	if literalState != "complete" || boundedState != "complete" || literalGeneration == "" || literalGeneration != boundedGeneration || literalHigh != sourceHigh {
		return nil, xerrors.New("tiered search projection is incomplete or stale")
	}
	// Empty queries are structural-only searches. They do not require literal
	// traversal and remain available after the legacy FTS objects are retired.
	if strings.TrimSpace(criteria.Query()) == "" {
		candidateIDs, queryErr := queryStructuralEventIDs(ctx, tx, criteria)
		if queryErr != nil {
			return nil, queryErr
		}
		return hydrateEventSearchMetadataCandidates(ctx, tx, criteria, candidateIDs)
	}
	matches := make(map[string]apptypes.EventMetadata)
	literalCriteria := apptypes.NewEventSearchCriteriaBuilder(apptypes.MaxLiteralSearchLimit).Query(criteria.Query()).
		Workspace(criteria.Workspace()).SessionID(criteria.SessionID()).Client(criteria.Client()).Agent(criteria.Agent()).
		Kind(criteria.Kind()).From(criteria.From()).To(criteria.To()).FailuresOnly(criteria.FailuresOnly()).Build()
	page, err := d.searchLiteralPageTx(ctx, tx, apptypes.LiteralSearchRequest{
		Criteria: literalCriteria, Budget: apptypes.DeepLiteralSearchBudget,
		BodyRuneLimit: 1,
	})
	if err != nil {
		return nil, xerrors.Errorf("read bounded tiered search page: %w", err)
	}
	if !page.Coverage.Complete {
		return nil, &queryservice.EventSearchUnavailableError{
			Reason:         queryservice.EventSearchUnavailableIndexIncomplete,
			CandidateLimit: apptypes.DeepLiteralSearchBudget.SourceRows,
		}
	}
	for _, event := range page.Events {
		metadata := event.Metadata()
		matches[metadata.EventID().String()] = metadata
	}
	ordered := make([]apptypes.EventMetadata, 0, len(matches))
	for _, metadata := range matches {
		ordered = append(ordered, metadata)
	}
	sort.Slice(ordered, func(i, j int) bool {
		if ordered[i].CreatedAt().Equal(ordered[j].CreatedAt()) {
			return ordered[i].EventID().String() > ordered[j].EventID().String()
		}
		return ordered[i].CreatedAt().After(ordered[j].CreatedAt())
	})
	if anchor := criteria.PageAnchor(); !anchor.IsZero() {
		filtered := ordered[:0]
		for _, m := range ordered {
			if m.CreatedAt().Before(anchor.CreatedAt()) || (m.CreatedAt().Equal(anchor.CreatedAt()) && m.EventID().String() < anchor.EventID().String()) {
				filtered = append(filtered, m)
			}
		}
		ordered = filtered
	}
	start := criteria.Offset()
	if start > len(ordered) {
		start = len(ordered)
	}
	end := start + criteria.Limit()
	if end > len(ordered) {
		end = len(ordered)
	}
	return append([]apptypes.EventMetadata(nil), ordered[start:end]...), nil
}

func hydrateFullSearchMetadata(ctx context.Context, d *EventDatasource, metadata []apptypes.EventMetadata) ([]*model.Event, error) {
	db, tx, err := d.beginEventProjectionRead(ctx, "full tiered search hydration")
	if err != nil {
		return nil, err
	}
	defer closeEventProjectionRead(db, tx)
	events, err := hydrateFullEventMetadata(ctx, tx, metadata)
	if err != nil {
		return nil, err
	}
	if err = tx.Commit(); err != nil {
		return nil, xerrors.Errorf("finish full tiered search hydration: %w", err)
	}
	return events, nil
}
