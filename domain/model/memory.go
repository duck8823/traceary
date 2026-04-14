package model

import (
	"slices"
	"strings"
	"time"

	"golang.org/x/xerrors"

	"github.com/duck8823/traceary/domain/types"
)

// Memory is the aggregate root for durable memory in Traceary.
type Memory struct {
	memoryID     types.MemoryID
	memoryType   types.MemoryType
	scope        types.MemoryScope
	fact         string
	status       types.MemoryStatus
	confidence   types.Confidence
	source       types.MemorySource
	evidenceRefs []types.EvidenceRef
	artifactRefs []types.ArtifactRef
	supersedes   types.Optional[types.MemoryID]
	expiresAt    types.Optional[time.Time]
	createdAt    time.Time
	updatedAt    time.Time
}

// NewMemoryCandidate creates a candidate memory.
func NewMemoryCandidate(
	memoryID types.MemoryID,
	memoryType types.MemoryType,
	scope types.MemoryScope,
	fact string,
	source types.MemorySource,
	evidenceRefs []types.EvidenceRef,
	artifactRefs []types.ArtifactRef,
	supersedes types.Optional[types.MemoryID],
) (*Memory, error) {
	return newMemory(memoryID, memoryType, scope, fact, types.MemoryStatusCandidate, types.ConfidenceLow, source, evidenceRefs, artifactRefs, supersedes, types.None[time.Time]())
}

// NewAcceptedMemory creates an accepted memory.
func NewAcceptedMemory(
	memoryID types.MemoryID,
	memoryType types.MemoryType,
	scope types.MemoryScope,
	fact string,
	confidence types.Confidence,
	source types.MemorySource,
	evidenceRefs []types.EvidenceRef,
	artifactRefs []types.ArtifactRef,
	supersedes types.Optional[types.MemoryID],
) (*Memory, error) {
	return newMemory(memoryID, memoryType, scope, fact, types.MemoryStatusAccepted, confidence, source, evidenceRefs, artifactRefs, supersedes, types.None[time.Time]())
}

func newMemory(
	memoryID types.MemoryID,
	memoryType types.MemoryType,
	scope types.MemoryScope,
	fact string,
	status types.MemoryStatus,
	confidence types.Confidence,
	source types.MemorySource,
	evidenceRefs []types.EvidenceRef,
	artifactRefs []types.ArtifactRef,
	supersedes types.Optional[types.MemoryID],
	expiresAt types.Optional[time.Time],
) (*Memory, error) {
	trimmedFact := strings.TrimSpace(fact)
	if trimmedFact == "" {
		return nil, xerrors.Errorf("memory fact must not be empty")
	}
	if scope == nil {
		return nil, xerrors.Errorf("memory scope must not be nil")
	}

	now := nowFunc()
	return &Memory{
		memoryID:     memoryID,
		memoryType:   memoryType,
		scope:        scope,
		fact:         trimmedFact,
		status:       status,
		confidence:   confidence,
		source:       source,
		evidenceRefs: slices.Clone(evidenceRefs),
		artifactRefs: slices.Clone(artifactRefs),
		supersedes:   supersedes,
		expiresAt:    expiresAt,
		createdAt:    now,
		updatedAt:    now,
	}, nil
}

// MemoryOf restores a Memory from persisted values.
func MemoryOf(
	memoryID types.MemoryID,
	memoryType types.MemoryType,
	scope types.MemoryScope,
	fact string,
	status types.MemoryStatus,
	confidence types.Confidence,
	source types.MemorySource,
	evidenceRefs []types.EvidenceRef,
	artifactRefs []types.ArtifactRef,
	supersedes types.Optional[types.MemoryID],
	expiresAt types.Optional[time.Time],
	createdAt time.Time,
	updatedAt time.Time,
) *Memory {
	return &Memory{
		memoryID:     memoryID,
		memoryType:   memoryType,
		scope:        scope,
		fact:         fact,
		status:       status,
		confidence:   confidence,
		source:       source,
		evidenceRefs: slices.Clone(evidenceRefs),
		artifactRefs: slices.Clone(artifactRefs),
		supersedes:   supersedes,
		expiresAt:    expiresAt,
		createdAt:    createdAt,
		updatedAt:    updatedAt,
	}
}

// MemoryID returns the memory ID.
func (m *Memory) MemoryID() types.MemoryID { return m.memoryID }

// MemoryType returns the memory type.
func (m *Memory) MemoryType() types.MemoryType { return m.memoryType }

// Scope returns the memory scope.
func (m *Memory) Scope() types.MemoryScope { return m.scope }

// Fact returns the distilled fact stored in the memory.
func (m *Memory) Fact() string { return m.fact }

// Status returns the lifecycle status.
func (m *Memory) Status() types.MemoryStatus { return m.status }

// Confidence returns the memory confidence.
func (m *Memory) Confidence() types.Confidence { return m.confidence }

// Source returns the source attribution for the memory.
func (m *Memory) Source() types.MemorySource { return m.source }

// EvidenceRefs returns the evidence references.
func (m *Memory) EvidenceRefs() []types.EvidenceRef { return slices.Clone(m.evidenceRefs) }

// ArtifactRefs returns the artifact references.
func (m *Memory) ArtifactRefs() []types.ArtifactRef { return slices.Clone(m.artifactRefs) }

// Supersedes returns the previous memory ID superseded by this memory, when present.
func (m *Memory) Supersedes() types.Optional[types.MemoryID] { return m.supersedes }

// ExpiresAt returns the expiry time, if present.
func (m *Memory) ExpiresAt() types.Optional[time.Time] { return m.expiresAt }

// CreatedAt returns when the memory was created.
func (m *Memory) CreatedAt() time.Time { return m.createdAt }

// UpdatedAt returns when the memory was last updated.
func (m *Memory) UpdatedAt() time.Time { return m.updatedAt }

// Accept transitions a candidate memory to accepted.
func (m *Memory) Accept(confidence types.Confidence) error {
	if m.status != types.MemoryStatusCandidate {
		return ErrInvalidMemoryState
	}
	m.status = types.MemoryStatusAccepted
	m.confidence = confidence
	m.updatedAt = nowFunc()
	return nil
}

// Reject transitions a candidate memory to rejected.
func (m *Memory) Reject() error {
	if m.status != types.MemoryStatusCandidate {
		return ErrInvalidMemoryState
	}
	m.status = types.MemoryStatusRejected
	m.updatedAt = nowFunc()
	return nil
}

// MarkSuperseded transitions an accepted memory to superseded.
func (m *Memory) MarkSuperseded() error {
	if m.status != types.MemoryStatusAccepted {
		return ErrInvalidMemoryState
	}
	m.status = types.MemoryStatusSuperseded
	m.updatedAt = nowFunc()
	return nil
}

// Expire transitions an active memory to expired.
func (m *Memory) Expire(expiresAt time.Time) error {
	if m.status != types.MemoryStatusCandidate && m.status != types.MemoryStatusAccepted {
		return ErrInvalidMemoryState
	}
	m.status = types.MemoryStatusExpired
	m.expiresAt = types.Some(expiresAt)
	m.updatedAt = nowFunc()
	return nil
}
