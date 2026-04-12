package types_test

import (
	"testing"
	"time"

	apptypes "github.com/duck8823/traceary/application/types"
	"github.com/duck8823/traceary/domain/model"
	domtypes "github.com/duck8823/traceary/domain/types"
)

func TestMemoryDetailsFrom(t *testing.T) {
	t.Parallel()

	memoryID, _ := domtypes.MemoryIDOf("mem-details")
	workspace, _ := domtypes.WorkspaceOf("github.com/duck8823/traceary")
	evidenceRef, _ := domtypes.EvidenceRefOf(domtypes.EvidenceRefKindIssue, "454")
	artifactRef, _ := domtypes.ArtifactRefOf(domtypes.ArtifactRefKindPR, "466")
	memory := model.MemoryOf(
		memoryID,
		domtypes.MemoryTypeArtifact,
		domtypes.WorkspaceScopeOf(workspace),
		"Implementation PRs close sub-issues only",
		domtypes.MemoryStatusAccepted,
		domtypes.ConfidenceHigh,
		domtypes.MemorySourceManual,
		[]domtypes.EvidenceRef{evidenceRef},
		[]domtypes.ArtifactRef{artifactRef},
		domtypes.Empty[domtypes.MemoryID](),
		domtypes.Empty[time.Time](),
		time.Date(2026, 4, 12, 8, 0, 0, 0, time.UTC),
		time.Date(2026, 4, 12, 8, 15, 0, 0, time.UTC),
	)

	details, err := apptypes.MemoryDetailsFrom(memory)
	if err != nil {
		t.Fatalf("MemoryDetailsFrom() error = %v", err)
	}
	if got := details.Summary().MemoryID(); got != memoryID {
		t.Fatalf("Summary().MemoryID() = %s, want %s", got, memoryID)
	}
	if got := len(details.EvidenceRefs()); got != 1 {
		t.Fatalf("len(EvidenceRefs()) = %d, want 1", got)
	}
	if got := len(details.ArtifactRefs()); got != 1 {
		t.Fatalf("len(ArtifactRefs()) = %d, want 1", got)
	}
}
