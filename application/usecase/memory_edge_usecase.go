package usecase

import (
	"context"
	"time"

	"github.com/duck8823/traceary/domain/model"
	"github.com/duck8823/traceary/domain/types"
)

// MemoryEdgeUsecase exposes write + read operations on the memory
// graph overlay introduced for #573.
type MemoryEdgeUsecase interface {
	// Add persists a typed relationship between two memories. See
	// model.NewMemoryEdge for validation rules.
	Add(
		ctx context.Context,
		fromMemoryID types.MemoryID,
		toMemoryID types.MemoryID,
		relation types.MemoryEdgeRelation,
		validFrom types.Optional[time.Time],
		validTo types.Optional[time.Time],
	) (*model.MemoryEdge, error)

	// List returns edges matching the filter. The filter's zero
	// value returns every edge up to the limit.
	List(ctx context.Context, filter model.MemoryEdgeListFilter) ([]*model.MemoryEdge, error)
}
