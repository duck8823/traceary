package usecase

import (
	"context"

	apptypes "github.com/duck8823/traceary/application/types"
)

// MemoryBridgeImportUsecase reads a host instruction file (CLAUDE.md /
// AGENTS.md / GEMINI.md) and records free-form content outside the
// Traceary-managed block as durable-memory candidates. Content inside the
// Traceary markers is already round-trippable via export, so it is
// intentionally ignored on import to keep re-runs idempotent.
type MemoryBridgeImportUsecase interface {
	ImportInstructions(ctx context.Context, criteria apptypes.MemoryBridgeImportCriteria) (apptypes.MemoryBridgeImportResult, error)
}
