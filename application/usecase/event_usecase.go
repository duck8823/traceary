package usecase

import (
	"context"

	"github.com/duck8823/traceary/domain/model"
	"github.com/duck8823/traceary/domain/types"
)

// AuditRedaction holds redaction and truncation settings for command audit recording.
type AuditRedaction struct {
	AllowSecrets        bool
	MaxInputBytes       int
	MaxOutputBytes      int
	ExtraRedactPatterns []string
}

// EventUsecase consolidates event recording and query operations.
type EventUsecase interface {
	// Log records a log event. Zero-value kind defaults to note.
	Log(ctx context.Context, message string, kind types.EventKind, client types.Client, agent types.Agent, sessionID types.SessionID, workspace types.Workspace) (*model.Event, error)

	// Audit records a command execution audit event.
	Audit(ctx context.Context, command string, input string, output string, client types.Client, agent types.Agent, sessionID types.SessionID, workspace types.Workspace, exitCode *int, redaction AuditRedaction) (*model.Event, *model.CommandAudit, error)

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
