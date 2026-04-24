package usecase

import (
	"context"
	"time"

	"golang.org/x/xerrors"

	"github.com/duck8823/traceary/domain/model"
	"github.com/duck8823/traceary/domain/types"
)

type memoryEdgeUsecase struct {
	repository model.MemoryEdgeRepository
	queryService model.MemoryEdgeQueryService
	nowFunc    func() time.Time
}

// NewMemoryEdgeUsecase constructs a MemoryEdgeUsecase bound to the
// given write + read dependencies. nowFunc is injected so tests can
// pin validFrom's default without touching real clocks.
func NewMemoryEdgeUsecase(
	repository model.MemoryEdgeRepository,
	queryService model.MemoryEdgeQueryService,
	nowFunc func() time.Time,
) MemoryEdgeUsecase {
	if nowFunc == nil {
		nowFunc = time.Now
	}
	return &memoryEdgeUsecase{
		repository:   repository,
		queryService: queryService,
		nowFunc:      nowFunc,
	}
}

// Add builds and persists an edge. When validFrom is None the
// usecase stamps the current time so callers can omit the flag for
// "valid from now" semantics — matching `memory remember`.
func (u *memoryEdgeUsecase) Add(
	ctx context.Context,
	fromMemoryID types.MemoryID,
	toMemoryID types.MemoryID,
	relation types.MemoryEdgeRelation,
	validFrom types.Optional[time.Time],
	validTo types.Optional[time.Time],
) (*model.MemoryEdge, error) {
	edgeID, err := newMemoryEdgeID()
	if err != nil {
		return nil, xerrors.Errorf("failed to generate edge ID: %w", err)
	}
	effectiveFrom := u.nowFunc().UTC()
	if from, ok := validFrom.Value(); ok {
		effectiveFrom = from
	}
	edge, err := model.NewMemoryEdge(
		edgeID,
		fromMemoryID,
		toMemoryID,
		relation,
		effectiveFrom,
		validTo,
		u.nowFunc().UTC(),
	)
	if err != nil {
		return nil, xerrors.Errorf("failed to build memory edge: %w", err)
	}
	if err := u.repository.Save(ctx, edge); err != nil {
		return nil, xerrors.Errorf("failed to save memory edge: %w", err)
	}
	return edge, nil
}

// List delegates to the query service.
func (u *memoryEdgeUsecase) List(ctx context.Context, filter model.MemoryEdgeListFilter) ([]*model.MemoryEdge, error) {
	edges, err := u.queryService.List(ctx, filter)
	if err != nil {
		return nil, xerrors.Errorf("failed to list memory edges: %w", err)
	}
	return edges, nil
}
