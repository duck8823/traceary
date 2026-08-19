package sqlite

import (
	"strings"
	"testing"

	apptypes "github.com/duck8823/traceary/application/types"
	"github.com/duck8823/traceary/domain/types"
)

func TestBuildProjectionSessionHitQueryFilterableUsesKeywordIndex(t *testing.T) {
	t.Parallel()
	criteria := apptypes.NewEventSearchCriteriaBuilder(5).
		Query("xyzzy-nomatch-2170").
		Workspace(types.Workspace("github.com/duck8823/traceary")).
		Build()
	query, args := buildProjectionSessionHitQuery(criteria, "xyzzy-nomatch-2170", nil)
	if strings.Contains(query, "LIKE") {
		t.Fatal("filterable session SQL still LIKE-scans summary_text")
	}
	if !strings.Contains(query, "idx_search_projection_session_keywords_by_kw") {
		t.Fatalf("filterable session SQL does not force keyword index:\n%s", query)
	}
	if strings.Contains(query, "ts_norm") {
		t.Fatalf("filterable session SQL still wraps started_at in ts_norm:\n%s", query)
	}
	if len(args) < 2 {
		t.Fatalf("args = %#v, want keyword plus limit", args)
	}
}

func TestBuildProjectionSessionHitQueryUnfilterableKeepsLike(t *testing.T) {
	t.Parallel()
	criteria := apptypes.NewEventSearchCriteriaBuilder(5).Query("ab").Build()
	query, _ := buildProjectionSessionHitQuery(criteria, "ab", nil)
	if !strings.Contains(query, "LIKE") {
		t.Fatal("unfilterable session SQL dropped the summary LIKE walk")
	}
}
