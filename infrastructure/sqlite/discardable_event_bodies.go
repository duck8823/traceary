package sqlite

import (
	_ "embed"
	"strings"

	"golang.org/x/xerrors"
)

const discardableEventBodiesPlaceholder = "-- discardable-event-bodies"

//go:embed sql/select_discardable_event_bodies.sql
var selectDiscardableEventBodiesQuery string

//go:embed sql/count_discardable_event_bodies.sql
var countDiscardableEventBodiesQuery string

//go:embed sql/discard_event_bodies.sql
var discardEventBodiesQuery string

//go:embed sql/list_raw_body_candidates.sql
var listRawBodyCandidatesQuery string

//go:embed sql/list_raw_body_exclusions.sql
var listRawBodyExclusionsQuery string

//go:embed sql/verify_raw_body_candidate_state.sql
var verifyRawBodyCandidateStateQuery string

func composeDiscardableEventBodiesQuery(wrapper string) (string, error) {
	if strings.Count(wrapper, discardableEventBodiesPlaceholder) != 1 {
		return "", xerrors.Errorf("discardable event bodies wrapper must contain exactly one placeholder")
	}
	return strings.Replace(wrapper, discardableEventBodiesPlaceholder, selectDiscardableEventBodiesQuery, 1), nil
}
