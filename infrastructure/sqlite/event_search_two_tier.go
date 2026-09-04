package sqlite

import (
	"context"
	"strings"

	"golang.org/x/xerrors"

	"github.com/duck8823/traceary/application/queryservice"
	apptypes "github.com/duck8823/traceary/application/types"
)

var _ queryservice.TwoTierSearch = (*EventDatasource)(nil)

func validateTwoTierSearchCriteria(criteria apptypes.EventSearchCriteria) error {
	if criteria.Limit() <= 0 {
		return xerrors.New("limit must be greater than or equal to 1")
	}
	if criteria.Offset() < 0 {
		return xerrors.New("offset must be greater than or equal to 0")
	}
	if !criteria.From().IsZero() && !criteria.To().IsZero() && criteria.From().After(criteria.To()) {
		return xerrors.New("from must be earlier than to")
	}
	return nil
}

func twoTierDisposition(criteria apptypes.EventSearchCriteria) apptypes.RefinementDisposition {
	if strings.TrimSpace(criteria.Kind().String()) != "" {
		return apptypes.RefinementDispositionKindExcluded
	}
	if criteria.FailuresOnly() {
		return apptypes.RefinementDispositionFailuresExcluded
	}
	return apptypes.RefinementDispositionApplied
}

// SearchTwoTier is the single queryservice entry for refinement-primary
// phrase search plus the unindexed fallback scan. Both lanes run inside one
// read-only snapshot. There is no row or time budget; cancellation is the
// only stop.
func (d *EventDatasource) SearchTwoTier(
	ctx context.Context,
	criteria apptypes.EventSearchCriteria,
) (apptypes.TwoTierSearchPage, error) {
	if err := validateTwoTierSearchCriteria(criteria); err != nil {
		return apptypes.TwoTierSearchPage{}, err
	}
	disposition := twoTierDisposition(criteria)
	phrase := apptypes.SearchPhraseOf(criteria.Query())
	if phrase.IsEmpty() {
		return apptypes.TwoTierSearchPageOf(nil, nil, disposition, 0), nil
	}

	db, tx, err := d.beginEventProjectionRead(ctx, "two-tier search")
	if err != nil {
		return apptypes.TwoTierSearchPage{}, err
	}
	defer closeEventProjectionRead(db, tx)

	refinementHits, err := queryTwoTierRefinementHits(ctx, tx, criteria, phrase)
	if err != nil {
		return apptypes.TwoTierSearchPage{}, err
	}
	refinementMatchCount := len(refinementHits)
	includeRefinement := disposition == apptypes.RefinementDispositionApplied
	var refinementIDs map[string]struct{}
	if includeRefinement {
		refinementIDs = twoTierSessionIDSet(refinementHits)
	} else {
		refinementHits = nil
	}

	fallbackEvents, fallbackSessions, err := scanTwoTierFallback(
		ctx, tx, criteria, phrase, refinementIDs, includeRefinement,
	)
	if err != nil {
		return apptypes.TwoTierSearchPage{}, err
	}

	merged := mergeTwoTierHits(refinementHits, fallbackEvents, fallbackSessions, criteria.Offset(), criteria.Limit())
	eventHits, err := hydrateTwoTierEventHits(ctx, tx, merged)
	if err != nil {
		return apptypes.TwoTierSearchPage{}, err
	}
	sessionHits := twoTierSessionHitsFromMerged(merged)
	if err := tx.Commit(); err != nil {
		return apptypes.TwoTierSearchPage{}, xerrors.Errorf("finish two-tier search: %w", err)
	}
	return apptypes.TwoTierSearchPageOf(eventHits, sessionHits, disposition, refinementMatchCount), nil
}

func twoTierSessionIDSet(hits []twoTierHit) map[string]struct{} {
	ids := make(map[string]struct{}, len(hits))
	for _, hit := range hits {
		if hit.sessionID != "" {
			ids[hit.sessionID] = struct{}{}
		}
	}
	return ids
}

func twoTierFilterCriteria(criteria apptypes.EventSearchCriteria) apptypes.EventSearchCriteria {
	return apptypes.NewEventSearchCriteriaBuilder(criteria.Limit()).
		Query(criteria.Query()).
		Workspace(criteria.Workspace()).
		SessionID(criteria.SessionID()).
		Client(criteria.Client()).
		Agent(criteria.Agent()).
		Kind(criteria.Kind()).
		From(criteria.From()).
		To(criteria.To()).
		Offset(criteria.Offset()).
		FailuresOnly(criteria.FailuresOnly()).
		Build()
}
