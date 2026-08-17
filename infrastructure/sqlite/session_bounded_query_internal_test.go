package sqlite

import (
	"strings"
	"testing"
)

func TestFindLatestSelectsCandidatesWithoutEventBodies(t *testing.T) {
	normalized := strings.Join(strings.Fields(strings.ToLower(findLatestSessionBoundaryQuery)), " ")
	selectedAt := strings.Index(normalized, ") select e.id")
	if selectedAt < 0 {
		t.Fatalf("FindLatest query has no bounded selection phase: %s", normalized)
	}
	candidatePhase := normalized[:selectedAt]
	if !strings.Contains(candidatePhase, "from event_metadata_projection boundary") {
		t.Fatalf("candidate selection does not use the body-free projection: %s", candidatePhase)
	}
	if strings.Contains(candidatePhase, " from events ") || strings.Contains(candidatePhase, " join events ") || strings.Contains(candidatePhase, ".body") {
		t.Fatalf("candidate selection opens body-bearing event rows: %s", candidatePhase)
	}
	if count := strings.Count(normalized, "join events e on e.id = selected.id"); count != 1 {
		t.Fatalf("FindLatest must hydrate exactly the selected event, hydration joins = %d: %s", count, normalized)
	}
}

func TestListSessionsHydratesOnlyLatestBodiesForBoundedPage(t *testing.T) {
	normalized := strings.Join(strings.Fields(strings.ToLower(listSessionsQuery)), " ")
	if !strings.Contains(normalized, "from filtered_sessions fs cross join event_metadata_projection e") {
		t.Fatalf("session aggregation is not driven by the bounded page and metadata projection: %s", normalized)
	}
	if count := strings.Count(normalized, "left join events latest_body on latest_body.id = latest.latest_event_id"); count != 1 {
		t.Fatalf("session list must hydrate only each selected session's latest event, hydration joins = %d: %s", count, normalized)
	}
	if strings.Contains(normalized, "select session_id, id as latest_event_id, created_at as latest_event_at, kind as latest_event_kind, body") {
		t.Fatalf("latest-event candidate selection includes body: %s", normalized)
	}
}
