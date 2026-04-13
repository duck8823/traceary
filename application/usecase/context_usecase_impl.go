package usecase

import (
	"context"

	"golang.org/x/xerrors"

	"github.com/duck8823/traceary/application/queryservice"
	apptypes "github.com/duck8823/traceary/application/types"
	"github.com/duck8823/traceary/domain/types"
)

type contextUsecase struct {
	builder *contextPackBuilder
	err     error
}

// NewContextUsecase creates a ContextUsecase.
func NewContextUsecase(
	sessionQuery queryservice.SessionQueryService,
	eventQuery queryservice.EventQueryService,
	memoryQuery queryservice.MemoryQueryService,
) ContextUsecase {
	builder, err := newContextPackBuilder(sessionQuery, eventQuery, memoryQuery)
	return &contextUsecase{builder: builder, err: err}
}

func (u *contextUsecase) Handoff(ctx context.Context, criteria apptypes.ContextPackCriteria) (types.Optional[apptypes.ContextPack], error) {
	if u.err != nil {
		return types.Empty[apptypes.ContextPack](), xerrors.Errorf("context usecase is not configured: %w", u.err)
	}
	if u.builder == nil {
		return types.Empty[apptypes.ContextPack](), xerrors.Errorf("context pack builder is not configured")
	}
	return u.builder.Build(ctx, criteria)
}
