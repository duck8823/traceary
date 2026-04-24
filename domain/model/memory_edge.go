package model

import (
	"strings"
	"time"

	"golang.org/x/xerrors"

	"github.com/duck8823/traceary/domain/types"
)

// MemoryEdge is a typed, time-bounded relationship between two
// memories. It is part of the additive graph overlay introduced for
// #573 and lives alongside the canonical memories table — an edge
// never owns content, only the relationship.
//
// validFrom / validTo use the same half-open `[valid_from, valid_to)`
// semantics the Memory aggregate uses, so `as-of` queries compose
// naturally across nodes and edges.
type MemoryEdge struct {
	edgeID       types.MemoryEdgeID
	fromMemoryID types.MemoryID
	toMemoryID   types.MemoryID
	relationType types.MemoryEdgeRelation
	validFrom    time.Time
	validTo      types.Optional[time.Time]
	createdAt    time.Time
}

// NewMemoryEdge constructs a fresh edge. fromMemoryID must differ
// from toMemoryID so self-loops stay an explicit error (they are
// rarely what the caller meant and silently accepting them hides
// typos). validTo, when set, must not be earlier than validFrom.
func NewMemoryEdge(
	edgeID types.MemoryEdgeID,
	fromMemoryID types.MemoryID,
	toMemoryID types.MemoryID,
	relationType types.MemoryEdgeRelation,
	validFrom time.Time,
	validTo types.Optional[time.Time],
	createdAt time.Time,
) (*MemoryEdge, error) {
	if strings.TrimSpace(fromMemoryID.String()) == "" {
		return nil, xerrors.Errorf("from memory ID must not be empty")
	}
	if strings.TrimSpace(toMemoryID.String()) == "" {
		return nil, xerrors.Errorf("to memory ID must not be empty")
	}
	if fromMemoryID == toMemoryID {
		return nil, xerrors.Errorf("from and to memory IDs must differ (self-loops are not accepted)")
	}
	if strings.TrimSpace(relationType.String()) == "" {
		return nil, xerrors.Errorf("relation_type must not be empty")
	}
	if validFrom.IsZero() {
		return nil, xerrors.Errorf("valid_from must be set")
	}
	if to, ok := validTo.Value(); ok && to.Before(validFrom) {
		return nil, xerrors.Errorf("valid_to must not be earlier than valid_from")
	}
	return &MemoryEdge{
		edgeID:       edgeID,
		fromMemoryID: fromMemoryID,
		toMemoryID:   toMemoryID,
		relationType: relationType,
		validFrom:    validFrom,
		validTo:      validTo,
		createdAt:    createdAt,
	}, nil
}

// MemoryEdgeOf reconstructs a MemoryEdge from a persisted row without
// re-running invariant checks. Datasource code calls this; runtime
// callers should use NewMemoryEdge.
func MemoryEdgeOf(
	edgeID types.MemoryEdgeID,
	fromMemoryID types.MemoryID,
	toMemoryID types.MemoryID,
	relationType types.MemoryEdgeRelation,
	validFrom time.Time,
	validTo types.Optional[time.Time],
	createdAt time.Time,
) *MemoryEdge {
	return &MemoryEdge{
		edgeID:       edgeID,
		fromMemoryID: fromMemoryID,
		toMemoryID:   toMemoryID,
		relationType: relationType,
		validFrom:    validFrom,
		validTo:      validTo,
		createdAt:    createdAt,
	}
}

// EdgeID returns the edge's unique identifier.
func (e *MemoryEdge) EdgeID() types.MemoryEdgeID { return e.edgeID }

// FromMemoryID returns the edge source memory ID.
func (e *MemoryEdge) FromMemoryID() types.MemoryID { return e.fromMemoryID }

// ToMemoryID returns the edge target memory ID.
func (e *MemoryEdge) ToMemoryID() types.MemoryID { return e.toMemoryID }

// RelationType returns the semantic relation carried by the edge.
func (e *MemoryEdge) RelationType() types.MemoryEdgeRelation { return e.relationType }

// ValidFrom returns the inclusive lower bound of the edge's validity
// window.
func (e *MemoryEdge) ValidFrom() time.Time { return e.validFrom }

// ValidTo returns the exclusive upper bound of the edge's validity
// window, or None for open-ended edges.
func (e *MemoryEdge) ValidTo() types.Optional[time.Time] { return e.validTo }

// CreatedAt returns when the edge row was written.
func (e *MemoryEdge) CreatedAt() time.Time { return e.createdAt }
