package queryservice

import (
	"context"

	apptypes "github.com/duck8823/traceary/application/types"
	"github.com/duck8823/traceary/domain/types"
)

// MemoryQueryService provides read-side operations for durable memories.
type MemoryQueryService interface {
	// List returns memory summaries matching the provided criteria.
	List(ctx context.Context, criteria apptypes.MemoryListCriteria) ([]apptypes.MemorySummary, error)
	// Search performs text search across durable memories and their refs.
	Search(ctx context.Context, criteria apptypes.MemorySearchCriteria) ([]apptypes.MemorySummary, error)
	// GetDetails returns the details for a single memory.
	GetDetails(ctx context.Context, memoryID types.MemoryID) (apptypes.MemoryDetails, error)
}

// MemoryStatusCountQueryService is the additive read-side query used by the
// reliability pane to report true candidate/accepted totals when the bounded
// summary scan is saturated. It mirrors the additive StaleMemoryQueryService
// pattern so implementers opt in without widening MemoryQueryService.
type MemoryStatusCountQueryService interface {
	// CountByStatus returns the true per-status row counts matching the
	// criteria, ignoring its Limit/Offset.
	CountByStatus(ctx context.Context, criteria apptypes.MemoryListCriteria) (apptypes.MemoryStatusCounts, error)
}

// StaleMemoryQueryService provides the additive read-side query used by
// `traceary top --snapshot --json` to surface stale durable memories without
// introducing a write-side use case.
type StaleMemoryQueryService interface {
	// ListStale returns stale durable-memory rows plus the total count before
	// paging.
	ListStale(ctx context.Context, criteria apptypes.StaleMemoryListCriteria) (apptypes.StaleMemoryListResult, error)
}
