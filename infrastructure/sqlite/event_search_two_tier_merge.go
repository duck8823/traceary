package sqlite

import (
	"context"
	"slices"
	"time"

	"golang.org/x/xerrors"

	apptypes "github.com/duck8823/traceary/application/types"
	"github.com/duck8823/traceary/domain/model"
)

// twoTierResultKind is the total-order kind rank: event before session.
type twoTierResultKind int

const (
	twoTierResultKindEvent twoTierResultKind = iota
	twoTierResultKindSession
)

type twoTierHit struct {
	resultTime time.Time
	tier       apptypes.SearchHitTier
	kind       twoTierResultKind
	sessionID  string
	eventID    string
	session    apptypes.SearchSessionHit
}

// compareTwoTierHits implements the fixed total order:
// (1) result time descending as parsed instants,
// (2) tier then result kind in the named enum order,
// (3) session_id then event_id in binary string order.
func compareTwoTierHits(a, b twoTierHit) int {
	if !a.resultTime.Equal(b.resultTime) {
		if a.resultTime.After(b.resultTime) {
			return -1
		}
		return 1
	}
	if ar, br := a.tier.Rank(), b.tier.Rank(); ar != br {
		return ar - br
	}
	if a.kind != b.kind {
		return int(a.kind) - int(b.kind)
	}
	if a.sessionID != b.sessionID {
		if a.sessionID < b.sessionID {
			return -1
		}
		return 1
	}
	if a.eventID != b.eventID {
		if a.eventID < b.eventID {
			return -1
		}
		return 1
	}
	return 0
}

func mergeTwoTierHits(
	refinement []twoTierHit,
	fallbackEvents []twoTierHit,
	fallbackSessions []twoTierHit,
	offset, limit int,
) []twoTierHit {
	merged := make([]twoTierHit, 0, len(refinement)+len(fallbackEvents)+len(fallbackSessions))
	merged = append(merged, refinement...)
	merged = append(merged, fallbackEvents...)
	merged = append(merged, fallbackSessions...)
	slices.SortFunc(merged, compareTwoTierHits)
	if offset >= len(merged) {
		return []twoTierHit{}
	}
	merged = merged[offset:]
	if limit < len(merged) {
		merged = merged[:limit]
	}
	return merged
}

func hydrateTwoTierEventHits(
	ctx context.Context,
	queryer eventSearchQueryer,
	merged []twoTierHit,
) ([]apptypes.SearchEventHit, error) {
	ids := make([]string, 0)
	for _, hit := range merged {
		if hit.kind == twoTierResultKindEvent {
			ids = append(ids, hit.eventID)
		}
	}
	if len(ids) == 0 {
		return []apptypes.SearchEventHit{}, nil
	}
	events, err := hydrateEventSearchCandidates(ctx, queryer, ids)
	if err != nil {
		return nil, err
	}
	byID := make(map[string]*model.Event, len(events))
	for _, event := range events {
		byID[event.EventID().String()] = event
	}
	hits := make([]apptypes.SearchEventHit, 0, len(ids))
	for _, hit := range merged {
		if hit.kind != twoTierResultKindEvent {
			continue
		}
		event, ok := byID[hit.eventID]
		if !ok {
			return nil, xerrors.Errorf("two-tier event %s disappeared during hydrate", hit.eventID)
		}
		hits = append(hits, apptypes.SearchEventHitOf(event, apptypes.SearchHitTierFallback))
	}
	return hits, nil
}

func twoTierSessionHitsFromMerged(merged []twoTierHit) []apptypes.SearchSessionHit {
	hits := make([]apptypes.SearchSessionHit, 0)
	for _, hit := range merged {
		if hit.kind == twoTierResultKindSession {
			hits = append(hits, hit.session)
		}
	}
	return hits
}
