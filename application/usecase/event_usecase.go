package usecase

import (
	"context"

	"github.com/duck8823/traceary/domain/model"
	"github.com/duck8823/traceary/domain/types"
)

// AuditParams holds parameters for recording a command audit event.
type AuditParams struct {
	Command             string
	Input               string
	Output              string
	Client              types.Client
	Agent               types.Agent
	SessionID           types.SessionID
	Workspace           types.Workspace
	ExitCode            *int
	AllowSecrets        bool
	MaxInputBytes       int
	MaxOutputBytes      int
	ExtraRedactPatterns []string
}

// EventUsecase consolidates event recording and query operations.
type EventUsecase interface {
	// Log records a note event.
	Log(ctx context.Context, message string, client types.Client, agent types.Agent, sessionID types.SessionID, workspace types.Workspace) (*model.Event, error)

	// Audit records a command execution audit event.
	Audit(ctx context.Context, params AuditParams) (*model.Event, *model.CommandAudit, error)

	// Search performs full-text search across events.
	Search(ctx context.Context, criteria EventSearchCriteria) ([]*model.Event, error)

	// List returns events in descending time order.
	List(ctx context.Context, criteria EventListCriteria) ([]*model.Event, error)

	// Show returns the details for a single event.
	// Returns usecase.EventDetails; port.EventDetails will be removed in Phase C.
	Show(ctx context.Context, eventID types.EventID) (*EventDetails, error)

	// Context returns recent events for the given context.
	Context(ctx context.Context, criteria EventContextCriteria) ([]*model.Event, error)
}
