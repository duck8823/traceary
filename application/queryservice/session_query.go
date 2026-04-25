package queryservice

import (
	"context"
	"time"

	apptypes "github.com/duck8823/traceary/application/types"
	"github.com/duck8823/traceary/domain/model"
	"github.com/duck8823/traceary/domain/types"
)

// SessionQueryService provides read-side operations for sessions.
type SessionQueryService interface {
	// FindLatest returns the session_started event for the latest matching session.
	// Returns an empty Optional when no matching session exists.
	FindLatest(ctx context.Context, client types.Client, agent types.Agent, workspace types.Workspace, activeOnly bool) (types.Optional[*model.Event], error)
	// ListSummaries returns session summaries matching the criteria.
	ListSummaries(ctx context.Context, limit, offset int, sessionID types.SessionID, workspace types.Workspace, client types.Client, agent types.Agent, label string, from, to types.Optional[time.Time]) ([]apptypes.SessionSummary, error)
	// LineageOf returns the full session tree rooted at the topmost ancestor of sessionID.
	LineageOf(ctx context.Context, sessionID types.SessionID) ([]apptypes.SessionSummary, error)
}
