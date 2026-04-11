package queryservice

import (
	"context"
	"time"

	"golang.org/x/xerrors"

	"github.com/duck8823/traceary/domain/model"
	"github.com/duck8823/traceary/domain/types"
)

// EventQueryService provides read-side operations for events.
type EventQueryService interface {
	// ListRecent returns events in descending time order.
	ListRecent(ctx context.Context, limit, offset int, kind types.EventKind, client types.Client, agent types.Agent, sessionID types.SessionID, workspace types.Workspace, failuresOnly bool, from, to time.Time) ([]*model.Event, error)
	// Search performs full-text search across events.
	Search(ctx context.Context, query string, workspace types.Workspace, sessionID types.SessionID, client types.Client, agent types.Agent, kind types.EventKind, from, to time.Time, limit, offset int, failuresOnly bool) ([]*model.Event, error)
	// GetContext returns recent events for context retrieval.
	GetContext(ctx context.Context, workspace types.Workspace, sessionID types.SessionID, limit int) ([]*model.Event, error)
	// GetDetails returns the details for a single event.
	GetDetails(ctx context.Context, eventID types.EventID) (*EventDetails, error)
	// ListTimelineBlocks returns work blocks separated by idle gaps.
	ListTimelineBlocks(ctx context.Context, workspace types.Workspace, from, to time.Time, gapSeconds, limit int) ([]*TimelineBlock, error)
}

// EventDetails pairs an Event with its optional CommandAudit.
type EventDetails struct {
	event        *model.Event
	commandAudit *model.CommandAudit
}

// NewEventDetails creates an EventDetails value.
func NewEventDetails(event *model.Event, commandAudit *model.CommandAudit) (*EventDetails, error) {
	if event == nil {
		return nil, xerrors.Errorf("event must not be nil")
	}
	return &EventDetails{
		event:        event,
		commandAudit: commandAudit,
	}, nil
}

// Event returns the event.
func (d *EventDetails) Event() *model.Event { return d.event }

// CommandAudit returns the linked command audit, or nil.
func (d *EventDetails) CommandAudit() *model.CommandAudit { return d.commandAudit }

// TimelineBlock represents a contiguous work block separated by idle gaps.
type TimelineBlock struct {
	BlockStart time.Time
	BlockEnd   time.Time
	EventCount int
	Workspaces []string
	Agents     []string
	Kinds      []string
}
