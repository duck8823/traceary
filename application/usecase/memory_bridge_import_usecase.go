package usecase

import (
	"context"

	apptypes "github.com/duck8823/traceary/application/types"
)

// MemoryBridgeImportUsecase is a legacy adapter interface for instruction-file
// import capture now exposed by MemoryUsecase.
//
// Deprecated: use MemoryUsecase.ImportInstructions instead. This shim remains
// until DI is collapsed in the follow-up consolidation PR.
type MemoryBridgeImportUsecase interface {
	ImportInstructions(ctx context.Context, criteria apptypes.MemoryBridgeImportCriteria) (apptypes.MemoryBridgeImportResult, error)
}
