package usecase

import (
	"context"

	apptypes "github.com/duck8823/traceary/application/types"
)

// MemoryImportUsecase is a legacy adapter interface for Codex import capture
// now exposed by MemoryUsecase.
//
// Deprecated: use MemoryUsecase.ImportCodex instead. This shim remains until
// DI is collapsed in the follow-up consolidation PR.
type MemoryImportUsecase interface {
	ImportCodex(ctx context.Context, criteria apptypes.CodexImportCriteria) (apptypes.MemoryImportResult, error)
}
