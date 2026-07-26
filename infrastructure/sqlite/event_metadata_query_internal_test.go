package sqlite

import (
	"regexp"
	"strings"
	"testing"
	"time"

	apptypes "github.com/duck8823/traceary/application/types"
	"github.com/duck8823/traceary/domain/types"
)

var metadataSelectListPattern = regexp.MustCompile(`(?is)\bselect\b(.*?)\bfrom\b`)
var bodyBearingColumnPattern = regexp.MustCompile(`(?i)\b(?:e\.)?body\b|\bbody_blocks\b|\bcommand_text\b|\binput_text\b|\boutput_text\b`)

func TestMetadataQueries_DoNotSelectBodyColumns(t *testing.T) {
	t.Parallel()

	queries := map[string]string{
		"recent":                    selectRecentEventMetadataQuery,
		"recent workspace":          selectRecentEventMetadataByWorkspaceQuery,
		"recent session":            selectRecentEventMetadataBySessionQuery,
		"recent workspace session":  selectRecentEventMetadataByWorkspaceSessionQuery,
		"recent source hook":        selectRecentEventMetadataBySourceHookQuery,
		"recent legacy hook":        selectRecentEventMetadataBySourceHookWithLegacyQuery,
		"search":                    searchEventMetadataQuery,
		"indexed search hydration":  hydrateEventSearchMetadataCandidatesQuery,
		"context":                   getContextEventMetadataQuery,
		"context workspace":         getContextEventMetadataByWorkspaceQuery,
		"context session":           getContextEventMetadataBySessionQuery,
		"context workspace session": getContextEventMetadataByWorkspaceSessionQuery,
		"report usage":              listReportUsageQuery,
	}
	for name, query := range queries {
		name, query := name, query
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			selectLists := metadataSelectListPattern.FindAllStringSubmatch(strings.ToLower(query), -1)
			if len(selectLists) == 0 {
				t.Fatalf("query has no FROM clause: %s", query)
			}
			for _, match := range selectLists {
				selectList := match[1]
				if forbidden := bodyBearingColumnPattern.FindString(selectList); forbidden != "" {
					t.Fatalf("SELECT list contains body-bearing column %q: %s", forbidden, selectList)
				}
			}
		})
	}
}

func TestMetadataListAndContextQueriesUsePersistentProjection(t *testing.T) {
	t.Parallel()

	queries := map[string]string{
		"latest":                    selectLatestEventMetadataFastQuery,
		"latest workspace":          selectLatestEventMetadataFastByWorkspaceQuery,
		"latest timestamp kind":     selectLatestEventTimestampKindQuery,
		"latest workspace kind":     selectLatestEventTimestampKindByWorkspaceQuery,
		"recent":                    selectRecentEventMetadataQuery,
		"recent workspace":          selectRecentEventMetadataByWorkspaceQuery,
		"recent session":            selectRecentEventMetadataBySessionQuery,
		"recent workspace session":  selectRecentEventMetadataByWorkspaceSessionQuery,
		"recent source hook":        selectRecentEventMetadataBySourceHookQuery,
		"recent legacy hook":        selectRecentEventMetadataBySourceHookWithLegacyQuery,
		"context":                   getContextEventMetadataQuery,
		"context workspace":         getContextEventMetadataByWorkspaceQuery,
		"context session":           getContextEventMetadataBySessionQuery,
		"context workspace session": getContextEventMetadataByWorkspaceSessionQuery,
	}
	for name, query := range queries {
		name, query := name, query
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			normalized := strings.Join(strings.Fields(strings.ToLower(query)), " ")
			if !strings.Contains(normalized, "event_metadata_projection") {
				t.Fatalf("metadata query does not use persistent projection: %s", normalized)
			}
			for _, forbidden := range []string{" from events ", " join events "} {
				if strings.Contains(" "+normalized+" ", forbidden) {
					t.Fatalf("metadata query opens body-bearing events table: %s", normalized)
				}
			}
		})
	}
}

func TestMetadataPageQueriesRenderCompositeAnchorWithoutSentinelOrOffset(t *testing.T) {
	t.Parallel()

	anchor, err := apptypes.EventPageAnchorOf(
		time.Date(2026, 7, 26, 1, 2, 3, 456789, time.UTC),
		types.EventID("event-anchor"),
	)
	if err != nil {
		t.Fatalf("EventPageAnchorOf() error = %v", err)
	}
	queries := map[string]string{
		"recent":                    selectRecentEventMetadataQuery,
		"recent workspace":          selectRecentEventMetadataByWorkspaceQuery,
		"recent session":            selectRecentEventMetadataBySessionQuery,
		"recent workspace session":  selectRecentEventMetadataByWorkspaceSessionQuery,
		"recent source hook":        selectRecentEventMetadataBySourceHookQuery,
		"recent legacy hook":        selectRecentEventMetadataBySourceHookWithLegacyQuery,
		"context":                   getContextEventMetadataQuery,
		"context workspace":         getContextEventMetadataByWorkspaceQuery,
		"context session":           getContextEventMetadataBySessionQuery,
		"context workspace session": getContextEventMetadataByWorkspaceSessionQuery,
	}
	for name, query := range queries {
		name, query := name, query
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if count := strings.Count(query, metadataPageAnchorMarker); count != 1 {
				t.Fatalf("page anchor marker count = %d, want 1:\n%s", count, query)
			}
			rendered := strings.Join(strings.Fields(metadataPageQuery(query, anchor)), " ")
			if !strings.Contains(rendered, metadataPageAnchorPredicate) {
				t.Fatalf("composite keyset predicate is absent:\n%s", rendered)
			}
			if strings.Contains(rendered, metadataPageAnchorMarker) ||
				strings.Contains(rendered, "OR (e.created_at_norm = ? AND e.id < ?)") ||
				strings.Contains(rendered, "OFFSET") {
				t.Fatalf("anchored page retained marker, sentinel, or offset:\n%s", rendered)
			}
		})
	}
}

func TestReportUsageQuery_CurrentHeadRequiresFinalizedSuccessor(t *testing.T) {
	t.Parallel()
	normalized := strings.Join(strings.Fields(strings.ToLower(listReportUsageQuery)), " ")
	if !strings.Contains(normalized, "successor.supersedes_id = observation.observation_id and successor.status = 'finalized'") {
		t.Fatalf("report usage current-head predicate does not require a finalized successor: %s", normalized)
	}
}
