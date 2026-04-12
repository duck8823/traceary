package types

import (
	"slices"

	"golang.org/x/xerrors"

	"github.com/duck8823/traceary/domain/model"
	domtypes "github.com/duck8823/traceary/domain/types"
)

// MemoryDetails holds a MemorySummary plus traceable refs.
type MemoryDetails struct {
	summary      MemorySummary
	evidenceRefs []domtypes.EvidenceRef
	artifactRefs []domtypes.ArtifactRef
}

// MemoryDetailsOf creates MemoryDetails from a summary and refs.
func MemoryDetailsOf(summary MemorySummary, evidenceRefs []domtypes.EvidenceRef, artifactRefs []domtypes.ArtifactRef) MemoryDetails {
	return MemoryDetails{
		summary:      summary,
		evidenceRefs: slices.Clone(evidenceRefs),
		artifactRefs: slices.Clone(artifactRefs),
	}
}

// MemoryDetailsFrom creates MemoryDetails from a Memory aggregate.
func MemoryDetailsFrom(memory *model.Memory) (MemoryDetails, error) {
	summary, err := MemorySummaryFrom(memory)
	if err != nil {
		return MemoryDetails{}, xerrors.Errorf("failed to build memory summary: %w", err)
	}
	return MemoryDetailsOf(summary, memory.EvidenceRefs(), memory.ArtifactRefs()), nil
}

// Summary returns the memory summary.
func (d MemoryDetails) Summary() MemorySummary { return d.summary }

// EvidenceRefs returns the supporting evidence refs.
func (d MemoryDetails) EvidenceRefs() []domtypes.EvidenceRef { return slices.Clone(d.evidenceRefs) }

// ArtifactRefs returns the related artifact refs.
func (d MemoryDetails) ArtifactRefs() []domtypes.ArtifactRef { return slices.Clone(d.artifactRefs) }
