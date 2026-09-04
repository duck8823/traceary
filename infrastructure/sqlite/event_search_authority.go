//nolint:wrapcheck // SQLite search errors are contextualized at query boundaries.
package sqlite

import (
	"context"
	"strings"

	"golang.org/x/xerrors"

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
	maxWindow := apptypes.DeepLiteralSearchBudget.SourceRows
	if criteria.Limit() > maxWindow || criteria.Offset() > maxWindow-criteria.Limit() {
		return xerrors.Errorf("offset plus limit must not exceed bounded search window %d", maxWindow)
	}
	if !criteria.PageAnchor().IsZero() && criteria.Offset() != 0 {
		return xerrors.New("event page anchor cannot be combined with offset")
	}
	if !criteria.From().IsZero() && !criteria.To().IsZero() && criteria.From().After(criteria.To()) {
		return xerrors.New("from must be earlier than to")
	}
	return nil
}

func (d *EventDatasource) searchMetadataByCanonicalMembership(ctx context.Context, criteria apptypes.EventSearchCriteria) ([]apptypes.EventMetadata, error) {
	db, tx, err := d.beginEventProjectionRead(ctx, "canonical search")
	if err != nil {
		return nil, err
	}
	defer closeEventProjectionRead(db, tx)
	metadata, err := searchMetadataByCanonicalMembershipTx(ctx, tx, criteria)
	if err != nil {
		return nil, err
	}
	if err = tx.Commit(); err != nil {
		return nil, xerrors.Errorf("finish canonical search: %w", err)
	}
	return metadata, nil
}

func (d *EventDatasource) searchFullByCanonicalMembership(ctx context.Context, criteria apptypes.EventSearchCriteria) ([]*model.Event, error) {
	db, tx, err := d.beginEventProjectionRead(ctx, "full canonical search")
	if err != nil {
		return nil, err
	}
	defer closeEventProjectionRead(db, tx)
	metadata, err := searchMetadataByCanonicalMembershipTx(ctx, tx, criteria)
	if err != nil {
		return nil, err
	}
	events, err := hydrateFullEventMetadata(ctx, tx, metadata)
	if err != nil {
		return nil, err
	}
	if err = tx.Commit(); err != nil {
		return nil, xerrors.Errorf("finish full canonical search: %w", err)
	}
	return events, nil
}

func searchMetadataByCanonicalMembershipTx(ctx context.Context, tx eventSearchQueryer, criteria apptypes.EventSearchCriteria) ([]apptypes.EventMetadata, error) {
	if err := validateSearchCriteriaForAuthority(criteria); err != nil {
		return nil, err
	}
	candidateIDs, err := queryCanonicalSearchEventIDs(ctx, tx, criteria)
	if err != nil {
		return nil, err
	}
	return hydrateEventSearchMetadataCandidates(ctx, tx, criteria, candidateIDs)
}

func queryCanonicalSearchEventIDs(ctx context.Context, queryer eventSearchQueryer, criteria apptypes.EventSearchCriteria) ([]string, error) {
	if strings.TrimSpace(criteria.Query()) == "" {
		return queryStructuralEventIDs(ctx, queryer, criteria)
	}
	phrase := apptypes.SearchPhraseOf(criteria.Query())
	hits, _, err := scanTwoTierFallback(ctx, queryer, criteria, phrase, nil, false)
	if err != nil {
		return nil, err
	}
	start := min(criteria.Offset(), len(hits))
	end := min(start+criteria.Limit(), len(hits))
	ids := make([]string, 0, end-start)
	for _, hit := range hits[start:end] {
		ids = append(ids, hit.eventID)
	}
	return ids, nil
}
