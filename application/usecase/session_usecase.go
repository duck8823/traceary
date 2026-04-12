package usecase

import (
	"context"

	apptypes "github.com/duck8823/traceary/application/types"
	"github.com/duck8823/traceary/domain/model"
	"github.com/duck8823/traceary/domain/types"
)

// SessionUsecase consolidates session lifecycle and query operations.
type SessionUsecase interface {
	// Start begins a new session. If sessionID is zero, a new ID is generated.
	// Zero-value parentSessionID means no parent (top-level session).
	Start(ctx context.Context, client types.Client, agent types.Agent, sessionID types.SessionID, workspace types.Workspace, parentSessionID types.SessionID) (*model.Event, error)

	// End closes an existing session. Zero-value client/agent/workspace
	// falls back to values from the corresponding session_started event.
	End(ctx context.Context, client types.Client, agent types.Agent, sessionID types.SessionID, workspace types.Workspace, summary string) (*model.Event, error)

	// Label updates the label on an existing session.
	Label(ctx context.Context, sessionID types.SessionID, label string) error

	// List returns session summaries matching the criteria.
	List(ctx context.Context, criteria apptypes.SessionListCriteria) ([]apptypes.SessionSummary, error)

	// Tree returns session summaries as a hierarchy for the given workspace.
	// Zero-value workspace returns sessions across all workspaces.
	Tree(ctx context.Context, workspace types.Workspace, limit int) ([]apptypes.SessionSummary, error)

	// Active returns the session_started event for the active session matching the criteria.
	// Returns an empty Optional when no active session exists.
	Active(ctx context.Context, criteria apptypes.SessionLookupCriteria) (types.Optional[*model.Event], error)

	// Latest returns the session_started event for the latest session matching the criteria.
	// Returns an empty Optional when no matching session exists.
	Latest(ctx context.Context, criteria apptypes.SessionLookupCriteria) (types.Optional[*model.Event], error)

	// Handoff returns a concise summary for session context transfer between agents.
	// Zero-value workspace means no workspace filter.
	// Returns an empty Optional when no matching session exists.
	Handoff(ctx context.Context, sessionID types.SessionID, workspace types.Workspace, recent int) (types.Optional[apptypes.HandoffSummary], error)
}
