package usecase

import (
	"context"
	"time"

	apptypes "github.com/duck8823/traceary/application/types"
	"github.com/duck8823/traceary/domain/model"
	"github.com/duck8823/traceary/domain/types"
)

// SessionUsecase consolidates session lifecycle operations plus the legacy
// session-level query surfaces that remain for compatibility.
type SessionUsecase interface {
	// Start begins a new session. If sessionID is zero, a new ID is generated.
	// Zero-value parentSessionID means no parent (top-level session).
	Start(ctx context.Context, client types.Client, agent types.Agent, sessionID types.SessionID, workspace types.Workspace, parentSessionID types.SessionID) (*model.Event, error)

	// StartChild begins a child session spawned from an existing parent.
	StartChild(ctx context.Context, parent types.SessionID, childID types.SessionID, agent types.Agent, workspace types.Workspace, spawnEventID types.EventID, kind string, startedAt time.Time) (*model.Event, error)

	// End closes an existing session. Zero-value client/agent/workspace
	// falls back to values from the corresponding session_started event.
	End(ctx context.Context, client types.Client, agent types.Agent, sessionID types.SessionID, workspace types.Workspace, summary string) (*model.Event, error)

	// Label updates the label on an existing session.
	Label(ctx context.Context, sessionID types.SessionID, label string) error

	// List returns session summaries matching the criteria.
	List(ctx context.Context, criteria apptypes.SessionListCriteria) ([]apptypes.SessionSummary, error)

	// FindEndedSessionIDs returns the subset of sessionIDs that have a persisted end boundary.
	FindEndedSessionIDs(ctx context.Context, sessionIDs []types.SessionID) (map[types.SessionID]struct{}, error)

	// Tree returns session summaries as a hierarchy for the given workspace.
	// Zero-value workspace returns sessions across all workspaces. When rootSessionID
	// is set, the requested root is included regardless of the limit window.
	Tree(ctx context.Context, workspace types.Workspace, rootSessionID types.SessionID, limit int) ([]apptypes.SessionSummary, error)

	// Lineage returns the full hierarchy rooted at the topmost ancestor of sessionID.
	Lineage(ctx context.Context, sessionID types.SessionID) ([]apptypes.SessionSummary, error)

	// Active returns the session_started event for the active session matching the criteria.
	// Returns an empty Optional when no active session exists.
	Active(ctx context.Context, criteria apptypes.SessionLookupCriteria) (types.Optional[*model.Event], error)

	// Latest returns the session_started event for the latest session matching the criteria.
	// Returns an empty Optional when no matching session exists.
	Latest(ctx context.Context, criteria apptypes.SessionLookupCriteria) (types.Optional[*model.Event], error)

	// SetSummaryIfEmpty stores summary into sessions.summary when the existing
	// value is NULL or empty. Manually authored summaries are preserved.
	// Returns true when a row was actually updated.
	SetSummaryIfEmpty(ctx context.Context, sessionID types.SessionID, summary string) (bool, error)

	// SetModelIfEmpty stores a host-reported model identifier when the session
	// row still has an empty model. Empty input is a no-op. Never fabricates
	// a model value.
	SetModelIfEmpty(ctx context.Context, sessionID types.SessionID, model string) (bool, error)

	// Handoff returns the legacy session handoff summary shape used by older CLI
	// and MCP callers.
	//
	// New callers that want the structured working-memory pack should prefer
	// ContextUsecase.Handoff instead. Zero-value workspace means no workspace
	// filter. Returns an empty Optional when no matching session exists.
	Handoff(ctx context.Context, sessionID types.SessionID, workspace types.Workspace, recent int) (types.Optional[apptypes.HandoffSummary], error)
}
