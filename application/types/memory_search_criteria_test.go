package types_test

import (
	"testing"

	"github.com/google/go-cmp/cmp"

	apptypes "github.com/duck8823/traceary/application/types"
	domtypes "github.com/duck8823/traceary/domain/types"
)

func TestMemorySearchCriteriaBuilder(t *testing.T) {
	t.Parallel()

	agent, _ := domtypes.AgentFrom("codex")
	criteria := apptypes.NewMemorySearchCriteriaBuilder(10).
		Query("release").
		Offset(2).
		Scope(domtypes.AgentScopeOf(agent)).
		Status(domtypes.MemoryStatusCandidate).
		MemoryType(domtypes.MemoryTypeLesson).
		Build()

	if diff := cmp.Diff("release", criteria.Query()); diff != "" {
		t.Fatalf("Query() mismatch (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff(2, criteria.Offset()); diff != "" {
		t.Fatalf("Offset() mismatch (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff(1, len(criteria.Scopes())); diff != "" {
		t.Fatalf("len(Scopes()) mismatch (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff(1, len(criteria.Statuses())); diff != "" {
		t.Fatalf("len(Statuses()) mismatch (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff(1, len(criteria.MemoryTypes())); diff != "" {
		t.Fatalf("len(MemoryTypes()) mismatch (-want +got):\n%s", diff)
	}
}
