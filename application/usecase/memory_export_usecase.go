package usecase

import (
	"context"

	apptypes "github.com/duck8823/traceary/application/types"
)

// MemoryExportUsecase serializes accepted durable memories into the
// markdown format each host (Claude Code, Codex, Gemini CLI) expects for
// its project instruction file. The output is deterministic and idempotent
// so the operator can safely re-run the export on every checkout.
type MemoryExportUsecase interface {
	Export(ctx context.Context, criteria apptypes.MemoryExportCriteria) (apptypes.MemoryExportResult, error)
}
