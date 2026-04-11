package port

import (
	"context"
	"time"

	"golang.org/x/xerrors"

	"github.com/duck8823/traceary/domain/model"
)

// --- List Recent Events ---

// ListRecentEventsInput is the input for recent event listing.
type ListRecentEventsInput struct {
	Limit        int
	Offset       int
	Kind         string
	Client       string
	Agent        string
	SessionID    string
	Workspace string
	FailuresOnly bool
	From         time.Time
	To           time.Time
}

// RecentEventFinder provides recent event lookup.
type RecentEventFinder interface {
	// ListRecent returns events in descending time order.
	ListRecent(ctx context.Context, input ListRecentEventsInput) ([]*model.Event, error)
}

// --- Search Events ---

// SearchEventsInput is the input for event search.
type SearchEventsInput struct {
	Query        string
	Workspace string
	SessionID    string
	Client       string
	Agent        string
	Kind         string
	From         time.Time
	To           time.Time
	Limit        int
	Offset       int
	FailuresOnly bool
}

// EventSearcher provides event search.
type EventSearcher interface {
	// SearchEvents returns matching events in descending time order.
	SearchEvents(ctx context.Context, input SearchEventsInput) ([]*model.Event, error)
}

// --- Get Context ---

// GetContextInput is the input for context retrieval.
type GetContextInput struct {
	Workspace string
	SessionID string
	Limit     int
}

// ContextEventFinder provides contextual event lookup.
type ContextEventFinder interface {
	// GetContextEvents returns matching events in descending time order.
	GetContextEvents(ctx context.Context, input GetContextInput) ([]*model.Event, error)
}

// --- Get Event Details ---

// EventDetails represents the details for a single event.
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

// CommandAudit returns the linked command audit.
func (d *EventDetails) CommandAudit() *model.CommandAudit { return d.commandAudit }

// EventDetailsFinder provides event-details lookup.
type EventDetailsFinder interface {
	// GetEventDetails returns the details for the given event ID.
	GetEventDetails(ctx context.Context, eventID string) (*EventDetails, error)
}

// --- Find Latest Session ---

// FindLatestSessionInput is the input for latest-session lookup.
type FindLatestSessionInput struct {
	Client     string
	Agent      string
	Workspace string
	ActiveOnly bool
}

var (
	// ErrSessionNotFound indicates that no session matches the filters.
	ErrSessionNotFound = xerrors.New("no matching session found")
	// ErrActiveSessionNotFound indicates that no active session matches the filters.
	ErrActiveSessionNotFound = xerrors.New("no matching active session found")
)

// LatestSessionFinder provides access to the latest matching session start event.
type LatestSessionFinder interface {
	// FindLatestSessionStartedEvent returns the session_started event for the latest matching session.
	FindLatestSessionStartedEvent(
		ctx context.Context,
		input FindLatestSessionInput,
	) (*model.Event, error)
}

// --- List Sessions ---

// SessionSummary holds aggregated information about a single session.
type SessionSummary struct {
	SessionID       string
	Workspace string
	StartedAt       time.Time
	EndedAt         *time.Time
	Status          string
	TotalEvents     int
	CommandCount    int
	Agents          []string
	Label           string
	Summary         string
	ParentSessionID string
}

// ListSessionsInput is the input for session listing.
type ListSessionsInput struct {
	Limit     int
	Offset    int
	SessionID string
	Workspace string
	Client    string
	Agent     string
	Label     string
	From      *time.Time
	To        *time.Time
}

// SessionSummaryFinder provides session summary lookup.
type SessionSummaryFinder interface {
	ListSessionSummaries(ctx context.Context, input ListSessionsInput) ([]*SessionSummary, error)
}

// --- Timeline Blocks ---

// TimelineBlock represents a contiguous work block separated by idle gaps.
type TimelineBlock struct {
	BlockNum   int
	BlockStart time.Time
	BlockEnd   time.Time
	EventCount int
	Workspaces []string
	Agents     []string
	Kinds      []string
}

// ListTimelineBlocksInput is the input for timeline block listing.
type ListTimelineBlocksInput struct {
	Workspace  string
	From       time.Time
	To         time.Time
	GapSeconds int
	Limit      int
}

// TimelineBlockFinder provides timeline block lookup.
type TimelineBlockFinder interface {
	ListTimelineBlocks(ctx context.Context, input ListTimelineBlocksInput) ([]*TimelineBlock, error)
}
