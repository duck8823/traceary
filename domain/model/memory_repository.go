package model

import (
	"context"
	"time"

	"github.com/duck8823/traceary/domain/types"
)

// MemoryRepository persists Memory aggregates.
type MemoryRepository interface {
	// Save persists a memory aggregate.
	Save(ctx context.Context, memory *Memory) error
	// SaveDistillation persists a newly distilled accepted memory together with
	// any source-candidate lifecycle updates atomically.
	SaveDistillation(ctx context.Context, distilled *Memory, sources []*Memory) error
	// SaveSupersession persists a superseded memory state and its replacement
	// atomically.
	SaveSupersession(ctx context.Context, superseded *Memory, replacement *Memory) error
	// FindByID returns the memory for the given ID.
	// Returns an empty Optional when the memory does not exist.
	FindByID(ctx context.Context, memoryID types.MemoryID) (types.Optional[*Memory], error)
	// BackfillCandidateTTLs stamps expires_at = created_at + olderThan on
	// extracted / extracted-hidden candidates that have no scheduled TTL.
	// Returns the number of rows stamped.
	BackfillCandidateTTLs(ctx context.Context, olderThan time.Duration) (int, error)
}
