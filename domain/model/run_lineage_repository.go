package model

import (
	"context"

	"github.com/duck8823/traceary/domain/types"
)

// RunLineageRepository atomically records immutable host run lineage.
type RunLineageRepository interface {
	Record(ctx context.Context, lineage *RunLineage) (RunLineageTransition, error)
	FindByIdentity(ctx context.Context, identity types.RunIdentity) (types.Optional[*RunLineage], error)
}
