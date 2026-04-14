package types_test

import (
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

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
		domtypes.None[domtypes.MemoryID](),
		domtypes.None[time.Time](),
		time.Date(2026, 4, 12, 8, 0, 0, 0, time.UTC),
		time.Date(2026, 4, 12, 8, 15, 0, 0, time.UTC),
	)

	details, err := apptypes.MemoryDetailsFrom(memory)
	if err != nil {
		t.Fatalf("MemoryDetailsFrom() error = %v", err)
	}
	if diff := cmp.Diff(memoryID, details.Summary().MemoryID()); diff != "" {
		t.Fatalf("Summary().MemoryID() mismatch (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff(1, len(details.EvidenceRefs())); diff != "" {
		t.Fatalf("len(EvidenceRefs()) mismatch (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff(1, len(details.ArtifactRefs())); diff != "" {
		t.Fatalf("len(ArtifactRefs()) mismatch (-want +got):\n%s", diff)
	}
}
