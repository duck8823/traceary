package types

import (
	"strings"
	"time"

	"golang.org/x/xerrors"

	"github.com/duck8823/traceary/domain/model"
	domtypes "github.com/duck8823/traceary/domain/types"
)

// MemorySummary holds read-side information about a single durable memory.
type MemorySummary struct {
	memoryID   domtypes.MemoryID
	memoryType domtypes.MemoryType
	scope      domtypes.MemoryScope
	fact       string
	status     domtypes.MemoryStatus
	confidence domtypes.Confidence
	source     domtypes.MemorySource
	supersedes domtypes.Optional[domtypes.MemoryID]
	expiresAt  domtypes.Optional[time.Time]
	createdAt  time.Time
	updatedAt  time.Time
}

// MemorySummaryOf creates a MemorySummary.
func MemorySummaryOf(
	memoryID domtypes.MemoryID,
	memoryType domtypes.MemoryType,
	scope domtypes.MemoryScope,
	fact string,
	status domtypes.MemoryStatus,
	confidence domtypes.Confidence,
	source domtypes.MemorySource,
	supersedes domtypes.Optional[domtypes.MemoryID],
	expiresAt domtypes.Optional[time.Time],
	createdAt time.Time,
	updatedAt time.Time,
) (MemorySummary, error) {
	trimmedFact := strings.TrimSpace(fact)
	if trimmedFact == "" {
		return MemorySummary{}, xerrors.Errorf("memory fact must not be empty")
	}
	if scope == nil {
		return MemorySummary{}, xerrors.Errorf("memory scope must not be nil")
	}

	return MemorySummary{
		memoryID:   memoryID,
		memoryType: memoryType,
		scope:      scope,
		fact:       trimmedFact,
		status:     status,
		confidence: confidence,
		source:     source,
		supersedes: supersedes,
		expiresAt:  expiresAt,
		createdAt:  createdAt,
		updatedAt:  updatedAt,
	}, nil
}

// MemorySummaryFrom creates a MemorySummary from a Memory aggregate.
func MemorySummaryFrom(memory *model.Memory) (MemorySummary, error) {
	if memory == nil {
		return MemorySummary{}, xerrors.Errorf("memory must not be nil")
	}
	return MemorySummaryOf(
		memory.MemoryID(),
		memory.MemoryType(),
		memory.Scope(),
		memory.Fact(),
		memory.Status(),
		memory.Confidence(),
		memory.Source(),
		memory.Supersedes(),
		memory.ExpiresAt(),
		memory.CreatedAt(),
		memory.UpdatedAt(),
	)
}

// MemoryID returns the memory ID.
func (s MemorySummary) MemoryID() domtypes.MemoryID { return s.memoryID }

// MemoryType returns the memory type.
func (s MemorySummary) MemoryType() domtypes.MemoryType { return s.memoryType }

// Scope returns the memory scope.
func (s MemorySummary) Scope() domtypes.MemoryScope { return s.scope }

// Fact returns the distilled fact.
func (s MemorySummary) Fact() string { return s.fact }

// Status returns the memory lifecycle status.
func (s MemorySummary) Status() domtypes.MemoryStatus { return s.status }

// Confidence returns the memory confidence.
func (s MemorySummary) Confidence() domtypes.Confidence { return s.confidence }

// Source returns the memory source attribution.
func (s MemorySummary) Source() domtypes.MemorySource { return s.source }

// Supersedes returns the superseded memory ID, when present.
func (s MemorySummary) Supersedes() domtypes.Optional[domtypes.MemoryID] { return s.supersedes }

// ExpiresAt returns the expiry timestamp, when present.
func (s MemorySummary) ExpiresAt() domtypes.Optional[time.Time] { return s.expiresAt }

// CreatedAt returns when the memory was created.
func (s MemorySummary) CreatedAt() time.Time { return s.createdAt }

// UpdatedAt returns when the memory was last updated.
func (s MemorySummary) UpdatedAt() time.Time { return s.updatedAt }
