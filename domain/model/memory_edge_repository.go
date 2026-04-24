package model

import (
	"context"
	"time"

	"github.com/duck8823/traceary/domain/types"
)

// MemoryEdgeRepository persists typed relationships between memories.
// Implementations live under infrastructure/sqlite.
type MemoryEdgeRepository interface {
	// Save persists a new edge. Edges are append-only in v1 (see
	// #573): update a relationship by inserting a new edge with a
	// different `valid_from`, not by mutating an existing row.
	Save(ctx context.Context, edge *MemoryEdge) error
}

// MemoryEdgeQueryService provides read-side operations on the graph
// overlay.
type MemoryEdgeQueryService interface {
	// List returns edges matching the given filter, ordered by
	// `valid_from` desc and then `created_at` desc so callers see
	// the most recently effective relationship first.
	//
	// An empty MemoryEdgeListFilter.MemoryID widens the search to
	// every edge; Relation filters by exact match; AsOf, when set,
	// restricts to edges whose `[valid_from, valid_to)` window
	// contains the timestamp.
	List(ctx context.Context, filter MemoryEdgeListFilter) ([]*MemoryEdge, error)
}

// MemoryEdgeListFilter captures the subset of edges the caller wants
// to see. The zero value returns every edge.
type MemoryEdgeListFilter struct {
	// MemoryID restricts results to edges touching this memory
	// (either as source or target). Empty string disables the
	// filter.
	MemoryID types.MemoryID
	// Relation restricts results to a single relation type. Empty
	// string disables the filter.
	Relation types.MemoryEdgeRelation
	// AsOf, when set, restricts to edges whose validity window
	// contains the timestamp (valid_from <= AsOf < valid_to; an
	// open-ended valid_to is treated as +∞).
	AsOf types.Optional[time.Time]
	// Limit caps the number of rows returned. Zero means "no cap".
	Limit int
}
