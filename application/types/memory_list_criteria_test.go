package types_test

import (
	"testing"
	"time"

	apptypes "github.com/duck8823/traceary/application/types"
	domtypes "github.com/duck8823/traceary/domain/types"
)

func TestDefaultActiveMemoryStatuses(t *testing.T) {
	t.Parallel()

	got := apptypes.DefaultActiveMemoryStatuses()
	if len(got) != 2 {
		t.Fatalf("len(DefaultActiveMemoryStatuses()) = %d, want 2", len(got))
	}
	if got[0] != domtypes.MemoryStatusCandidate || got[1] != domtypes.MemoryStatusAccepted {
		t.Fatalf("DefaultActiveMemoryStatuses() = %v", got)
	}
}

func TestMemoryListCriteriaBuilder(t *testing.T) {
	t.Parallel()

	workspace, _ := domtypes.WorkspaceFrom("github.com/duck8823/traceary")
	updatedBefore := time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)
	updatedAfter := time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC)
	criteria := apptypes.NewMemoryListCriteriaBuilder(20).
		Offset(5).
		Scope(domtypes.WorkspaceScopeOf(workspace)).
		Status(domtypes.MemoryStatusAccepted).
		MemoryType(domtypes.MemoryTypeDecision).
		UpdatedBefore(updatedBefore).
		UpdatedAfter(updatedAfter).
		Build()

	if got := criteria.Limit(); got != 20 {
		t.Fatalf("Limit() = %d, want 20", got)
	}
	if got := criteria.Offset(); got != 5 {
		t.Fatalf("Offset() = %d, want 5", got)
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
	if got, ok := criteria.UpdatedBefore().Value(); !ok || !got.Equal(updatedBefore) {
		t.Fatalf("UpdatedBefore() = %v ok=%v, want %v", got, ok, updatedBefore)
	}
	if got, ok := criteria.UpdatedAfter().Value(); !ok || !got.Equal(updatedAfter) {
		t.Fatalf("UpdatedAfter() = %v ok=%v, want %v", got, ok, updatedAfter)
	}
}
