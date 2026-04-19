package usecase

import (
	"context"

	apptypes "github.com/duck8823/traceary/application/types"
)

// MemoryImportUsecase turns host-native memory sources (for example, Codex
// MEMORY.md handbooks) into Traceary durable-memory candidates. The import
// runs are deliberately candidate-only: nothing is auto-accepted, so an
// operator retains final say over which imported facts enter the active
// memory layer.
type MemoryImportUsecase interface {
	ImportCodex(ctx context.Context, criteria apptypes.CodexImportCriteria) (apptypes.MemoryImportResult, error)
}
