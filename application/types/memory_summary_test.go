package types_test

import (
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	apptypes "github.com/duck8823/traceary/application/types"
	"github.com/duck8823/traceary/domain/model"
	domtypes "github.com/duck8823/traceary/domain/types"
)

func TestMemorySummaryOf(t *testing.T) {
	t.Parallel()

	memoryID, _ := domtypes.MemoryIDOf("mem-1")
	workspace, _ := domtypes.WorkspaceOf("github.com/duck8823/traceary")
	supersedes, _ := domtypes.MemoryIDOf("mem-0")
	expiresAt := time.Date(2026, 4, 13, 10, 0, 0, 0, time.UTC)
	createdAt := time.Date(2026, 4, 12, 10, 0, 0, 0, time.UTC)
	updatedAt := time.Date(2026, 4, 12, 12, 0, 0, 0, time.UTC)

	summary, err := apptypes.MemorySummaryOf(
		memoryID,
		domtypes.MemoryTypeDecision,
		domtypes.WorkspaceScopeOf(workspace),
		"  Release issues close only after tagged release  ",
		domtypes.MemoryStatusAccepted,
		domtypes.ConfidenceVerified,
		domtypes.MemorySourceManual,
		domtypes.Some(supersedes),
		domtypes.Some(expiresAt),
		createdAt,
		updatedAt,
	)
	if err != nil {
		t.Fatalf("MemorySummaryOf() error = %v", err)
	}
	if diff := cmp.Diff("Release issues close only after tagged release", summary.Fact()); diff != "" {
		t.Fatalf("Fact() mismatch (-want +got):\n%s", diff)
	}
	gotExpiresAt, ok := summary.ExpiresAt().Value()
	if diff := cmp.Diff(true, ok); diff != "" {
		t.Fatalf("ExpiresAt() presence mismatch (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff(expiresAt, gotExpiresAt); diff != "" {
		t.Fatalf("ExpiresAt() mismatch (-want +got):\n%s", diff)
	}
}

func TestMemorySummaryFrom(t *testing.T) {
	t.Parallel()

	memoryID, _ := domtypes.MemoryIDOf("mem-2")
	agent, _ := domtypes.AgentOf("codex")
	memory := model.MemoryOf(
		memoryID,
		domtypes.MemoryTypeLesson,
		domtypes.AgentScopeOf(agent),
		"Codex prompt capture is optional",
		domtypes.MemoryStatusCandidate,
		domtypes.ConfidenceLow,
		domtypes.MemorySourceExtracted,
		nil,
		nil,
		domtypes.None[domtypes.MemoryID](),
		domtypes.None[time.Time](),
		time.Date(2026, 4, 12, 9, 0, 0, 0, time.UTC),
		time.Date(2026, 4, 12, 9, 30, 0, 0, time.UTC),
	)

	summary, err := apptypes.MemorySummaryFrom(memory)
	if err != nil {
		t.Fatalf("MemorySummaryFrom() error = %v", err)
	}
	if diff := cmp.Diff(memoryID, summary.MemoryID()); diff != "" {
		t.Fatalf("MemoryID() mismatch (-want +got):\n%s", diff)
	}
}
