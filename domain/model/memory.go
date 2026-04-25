package model

import (
	"slices"
	"strings"
	"time"

	"golang.org/x/xerrors"

	"github.com/duck8823/traceary/domain/types"
)

// Memory is the aggregate root for durable memory in Traceary.
//
// validFrom / validTo describe the **content validity window** —
// the period during which the fact is asserted to be true. They are
// distinct from expiresAt (the lifecycle timestamp written when an
// operator runs `memory expire`) and from createdAt / updatedAt
// (which describe when the row itself was recorded). See
// docs/memory/README.md and docs/architecture/memory-blocks.md.
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
	validFrom    time.Time
	validTo      types.Optional[time.Time]
	createdAt    time.Time
	updatedAt    time.Time
	clock        types.Clock
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
	return NewMemoryCandidateWithClock(memoryID, memoryType, scope, fact, source, evidenceRefs, artifactRefs, supersedes, types.SystemClock{})
}

// NewMemoryCandidateWithClock creates a candidate memory using the provided clock.
func NewMemoryCandidateWithClock(
	memoryID types.MemoryID,
	memoryType types.MemoryType,
	scope types.MemoryScope,
	fact string,
	source types.MemorySource,
	evidenceRefs []types.EvidenceRef,
	artifactRefs []types.ArtifactRef,
	supersedes types.Optional[types.MemoryID],
	clock types.Clock,
) (*Memory, error) {
	return newMemory(memoryID, memoryType, scope, fact, types.MemoryStatusCandidate, types.ConfidenceLow, source, evidenceRefs, artifactRefs, supersedes, types.None[time.Time](), clock)
}

// NewAcceptedMemory creates an accepted memory with the default
// validity window (validFrom=now, validTo=open-ended).
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
	return NewAcceptedMemoryWithValidity(memoryID, memoryType, scope, fact, confidence, source, evidenceRefs, artifactRefs, supersedes, types.None[time.Time](), types.None[time.Time]())
}

// NewAcceptedMemoryWithClock creates an accepted memory using the provided clock.
func NewAcceptedMemoryWithClock(
	memoryID types.MemoryID,
	memoryType types.MemoryType,
	scope types.MemoryScope,
	fact string,
	confidence types.Confidence,
	source types.MemorySource,
	evidenceRefs []types.EvidenceRef,
	artifactRefs []types.ArtifactRef,
	supersedes types.Optional[types.MemoryID],
	clock types.Clock,
) (*Memory, error) {
	return NewAcceptedMemoryWithValidityAndClock(memoryID, memoryType, scope, fact, confidence, source, evidenceRefs, artifactRefs, supersedes, types.None[time.Time](), types.None[time.Time](), clock)
}

// NewAcceptedMemoryWithValidity creates an accepted memory with an
// explicit temporal validity window. When validFrom is None the
// memory starts valid from the current wall clock. validTo is
// optional regardless: None means open-ended validity. Use this
// variant when superseding a time-bounded memory so the replacement
// inherits the intended window instead of silently resetting to
// "valid from now".
func NewAcceptedMemoryWithValidity(
	memoryID types.MemoryID,
	memoryType types.MemoryType,
	scope types.MemoryScope,
	fact string,
	confidence types.Confidence,
	source types.MemorySource,
	evidenceRefs []types.EvidenceRef,
	artifactRefs []types.ArtifactRef,
	supersedes types.Optional[types.MemoryID],
	validFrom types.Optional[time.Time],
	validTo types.Optional[time.Time],
) (*Memory, error) {
	return NewAcceptedMemoryWithValidityAndClock(memoryID, memoryType, scope, fact, confidence, source, evidenceRefs, artifactRefs, supersedes, validFrom, validTo, types.SystemClock{})
}

// NewAcceptedMemoryWithValidityAndClock creates an accepted memory with an explicit validity window using the provided clock.
func NewAcceptedMemoryWithValidityAndClock(
	memoryID types.MemoryID,
	memoryType types.MemoryType,
	scope types.MemoryScope,
	fact string,
	confidence types.Confidence,
	source types.MemorySource,
	evidenceRefs []types.EvidenceRef,
	artifactRefs []types.ArtifactRef,
	supersedes types.Optional[types.MemoryID],
	validFrom types.Optional[time.Time],
	validTo types.Optional[time.Time],
	clock types.Clock,
) (*Memory, error) {
	memory, err := newMemory(memoryID, memoryType, scope, fact, types.MemoryStatusAccepted, confidence, source, evidenceRefs, artifactRefs, supersedes, types.None[time.Time](), clock)
	if err != nil {
		return nil, err
	}
	if explicit, ok := validFrom.Value(); ok {
		memory.validFrom = explicit
	}
	memory.validTo = validTo
	return memory, nil
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
	clock types.Clock,
) (*Memory, error) {
	trimmedFact := strings.TrimSpace(fact)
	if trimmedFact == "" {
		return nil, xerrors.Errorf("memory fact must not be empty")
	}
	if scope == nil {
		return nil, xerrors.Errorf("memory scope must not be nil")
	}

	clock = clockOrSystem(clock)
	now := clock.Now()
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
		validFrom:    now,
		validTo:      types.None[time.Time](),
		createdAt:    now,
		updatedAt:    now,
		clock:        clock,
	}, nil
}

// MemoryOf restores a Memory from persisted values. `validFrom` is
// required because post-migration every memory row has a non-null
// valid_from (back-filled from created_at); `validTo` stays optional
// because open-ended validity is the default.
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
	validFrom time.Time,
	validTo types.Optional[time.Time],
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
		validFrom:    validFrom,
		validTo:      validTo,
		createdAt:    createdAt,
		updatedAt:    updatedAt,
		clock:        types.SystemClock{},
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

// ExpiresAt returns the lifecycle expire-operation timestamp, if
// present. This is distinct from ValidTo: ExpiresAt records when an
// operator ran `memory expire`; ValidTo records until when the fact
// itself is asserted to be true.
func (m *Memory) ExpiresAt() types.Optional[time.Time] { return m.expiresAt }

// ValidFrom returns the start of the content validity window. Every
// memory has a non-zero ValidFrom; post-migration this defaults to
// createdAt when the caller does not supply a more specific value.
func (m *Memory) ValidFrom() time.Time { return m.validFrom }

// ValidTo returns the end of the content validity window, if
// present. A nil ValidTo means the fact is open-ended — valid until
// explicitly superseded or manually expired.
func (m *Memory) ValidTo() types.Optional[time.Time] { return m.validTo }

// SetValidity sets the memory's content validity window in place.
// callers are expected to pass values that already satisfy
// validFrom <= validTo; enforcement lives in the application layer
// (which owns the end-user-facing errors). Passing an unset
// validFrom falls back to the memory's createdAt to preserve the
// post-migration invariant that validFrom is never zero.
func (m *Memory) SetValidity(validFrom types.Optional[time.Time], validTo types.Optional[time.Time]) {
	if from, ok := validFrom.Value(); ok {
		m.validFrom = from
	}
	m.validTo = validTo
	m.updatedAt = m.now()
}

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
	m.updatedAt = m.now()
	return nil
}

// Reject transitions a candidate memory to rejected.
func (m *Memory) Reject() error {
	if m.status != types.MemoryStatusCandidate {
		return ErrInvalidMemoryState
	}
	m.status = types.MemoryStatusRejected
	m.updatedAt = m.now()
	return nil
}

// MarkSuperseded transitions an accepted memory to superseded.
func (m *Memory) MarkSuperseded() error {
	if m.status != types.MemoryStatusAccepted {
		return ErrInvalidMemoryState
	}
	m.status = types.MemoryStatusSuperseded
	m.updatedAt = m.now()
	return nil
}

// Expire transitions an active memory to expired.
func (m *Memory) Expire(expiresAt time.Time) error {
	if m.status != types.MemoryStatusCandidate && m.status != types.MemoryStatusAccepted {
		return ErrInvalidMemoryState
	}
	m.status = types.MemoryStatusExpired
	m.expiresAt = types.Some(expiresAt)
	m.updatedAt = m.now()
	return nil
}

func (m *Memory) now() time.Time {
	return clockOrSystem(m.clock).Now()
}

func clockOrSystem(clock types.Clock) types.Clock {
	if clock == nil {
		return types.SystemClock{}
	}
	return clock
}
