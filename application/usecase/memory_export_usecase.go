package usecase

import (
	"context"

	apptypes "github.com/duck8823/traceary/application/types"
)

// MemoryExportUsecase is a legacy adapter interface for export behavior now
// exposed by MemoryUsecase.
//
// Deprecated: use MemoryUsecase.Export instead. This shim remains until DI is
// collapsed in the follow-up consolidation PR.
type MemoryExportUsecase interface {
	Export(ctx context.Context, criteria apptypes.MemoryExportCriteria) (apptypes.MemoryExportResult, error)
}
