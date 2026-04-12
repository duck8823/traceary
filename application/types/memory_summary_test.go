package types_test

import (
	"testing"
	"time"

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
		domtypes.Of(supersedes),
		domtypes.Of(expiresAt),
		createdAt,
		updatedAt,
	)
	if err != nil {
		t.Fatalf("MemorySummaryOf() error = %v", err)
	}
	if got := summary.Fact(); got != "Release issues close only after tagged release" {
		t.Fatalf("Fact() = %q", got)
	}
	if got, ok := summary.ExpiresAt().Get(); !ok || !got.Equal(expiresAt) {
		t.Fatalf("ExpiresAt() = (%v, %v), want (%v, true)", got, ok, expiresAt)
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
		domtypes.Empty[domtypes.MemoryID](),
		domtypes.Empty[time.Time](),
		time.Date(2026, 4, 12, 9, 0, 0, 0, time.UTC),
		time.Date(2026, 4, 12, 9, 30, 0, 0, time.UTC),
	)

	summary, err := apptypes.MemorySummaryFrom(memory)
	if err != nil {
		t.Fatalf("MemorySummaryFrom() error = %v", err)
	}
	if got := summary.MemoryID(); got != memoryID {
		t.Fatalf("MemoryID() = %s, want %s", got, memoryID)
	}
}
