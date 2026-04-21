package usecase

import (
	"context"

	apptypes "github.com/duck8823/traceary/application/types"
)

// ReplayUsecase assembles a cross-aggregate bundle for the replay HTML
// export. It hides the session / event / memory query orchestration
// behind a single call so the CLI layer only renders.
type ReplayUsecase interface {
	// Bundle returns a ReplayBundle that captures recent sessions (each
	// with its per-session event slice) plus the memory panel scoped to
	// those sessions' workspaces.
	Bundle(ctx context.Context, criteria apptypes.ReplayCriteria) (apptypes.ReplayBundle, error)
}
