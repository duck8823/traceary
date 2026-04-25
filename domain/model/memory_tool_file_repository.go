package model

import (
	"context"
	"time"

	"github.com/duck8823/traceary/domain/types"
)

// MemoryToolFileRepository persists Anthropic memory-tool files.
type MemoryToolFileRepository interface {
	Save(ctx context.Context, file *MemoryToolFile) error
	FindByPath(ctx context.Context, path types.MemoryToolPath) (types.Optional[*MemoryToolFile], error)
	List(ctx context.Context) ([]*MemoryToolFile, error)
	DeletePathPrefix(ctx context.Context, path types.MemoryToolPath) (int64, error)
	RenamePathPrefix(ctx context.Context, oldPath types.MemoryToolPath, newPath types.MemoryToolPath, updatedAt time.Time) (int64, error)
}
