package usecase

import (
	"context"

	"github.com/duck8823/traceary/domain/model"
	"github.com/duck8823/traceary/domain/types"
)

// EndSessionParams holds parameters for ending a session.
// Zero-value Client/Agent/Workspace falls back to the session start values.
type EndSessionParams struct {
	Client    types.Client
	Agent     types.Agent
	SessionID types.SessionID
	Workspace types.Workspace
	Summary   string
}

// SessionUsecase consolidates session lifecycle and query operations.
type SessionUsecase interface {
	// Start begins a new session. If sessionID is zero, a new ID is generated.
	Start(ctx context.Context, client types.Client, agent types.Agent, sessionID types.SessionID, workspace types.Workspace, parentSessionID types.SessionID) (*model.Event, error)

	// End closes an existing session. Zero-value fields in params fall back
	// to values from the corresponding session_started event.
	End(ctx context.Context, params EndSessionParams) (*model.Event, error)

	// Label updates the label on an existing session.
	Label(ctx context.Context, sessionID types.SessionID, label string) error

	// List returns session summaries matching the criteria.
	List(ctx context.Context, criteria SessionListCriteria) ([]*SessionSummary, error)

	// Tree returns session summaries as a hierarchy for the given workspace.
	Tree(ctx context.Context, workspace types.Workspace, limit int) ([]*SessionSummary, error)

	// Active returns the session_started event for the active session matching the criteria.
	Active(ctx context.Context, criteria SessionLookupCriteria) (*model.Event, error)

	// Latest returns the session_started event for the latest session matching the criteria.
	Latest(ctx context.Context, criteria SessionLookupCriteria) (*model.Event, error)

	// Handoff returns a concise summary for session context transfer between agents.
	Handoff(ctx context.Context, sessionID types.SessionID, workspace types.Workspace, recent int) (*HandoffSummary, error)
}
