package usecase

import (
	"context"

	apptypes "github.com/duck8823/traceary/application/types"
	"github.com/duck8823/traceary/domain/types"
)

// ContextUsecase assembles structured working-memory packs for handoff and
// context resumption.
//
// This is the preferred read/write orchestration surface behind
// operator-facing `traceary handoff` output and MCP tools such as
// `session_handoff` and `memory_pack`.
type ContextUsecase interface {
	// Handoff builds a structured ContextPack. Returns an empty Optional when no
	// matching session exists.
	Handoff(ctx context.Context, criteria apptypes.ContextPackCriteria) (types.Optional[apptypes.ContextPack], error)
}
