package types_test

import (
	"testing"

	apptypes "github.com/duck8823/traceary/application/types"
	domtypes "github.com/duck8823/traceary/domain/types"
)

func TestMemorySearchCriteriaBuilder(t *testing.T) {
	t.Parallel()

	agent, _ := domtypes.AgentOf("codex")
	criteria := apptypes.NewMemorySearchCriteriaBuilder(10).
		Query("release").
		Offset(2).
		Scope(domtypes.AgentScopeOf(agent)).
		Status(domtypes.MemoryStatusCandidate).
		MemoryType(domtypes.MemoryTypeLesson).
		Build()

	if got := criteria.Query(); got != "release" {
		t.Fatalf("Query() = %q, want %q", got, "release")
	}
	if got := criteria.Offset(); got != 2 {
		t.Fatalf("Offset() = %d, want 2", got)
	}
	if got := len(criteria.Scopes()); got != 1 {
		t.Fatalf("len(Scopes()) = %d, want 1", got)
	}
	if got := len(criteria.Statuses()); got != 1 {
		t.Fatalf("len(Statuses()) = %d, want 1", got)
	}
	if got := len(criteria.MemoryTypes()); got != 1 {
		t.Fatalf("len(MemoryTypes()) = %d, want 1", got)
	}
}
