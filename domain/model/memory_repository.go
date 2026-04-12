package model

import (
	"context"

	"github.com/duck8823/traceary/domain/types"
)

// MemoryRepository persists Memory aggregates.
type MemoryRepository interface {
	// Save persists a memory aggregate.
	Save(ctx context.Context, memory *Memory) error
	// SaveSupersession persists a superseded memory state and its replacement
	// atomically.
	SaveSupersession(ctx context.Context, superseded *Memory, replacement *Memory) error
	// FindByID returns the memory for the given ID.
	// Returns an empty Optional when the memory does not exist.
	FindByID(ctx context.Context, memoryID types.MemoryID) (types.Optional[*Memory], error)
}
