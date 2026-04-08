package queryservice

import (
	"context"
	"strings"

	"golang.org/x/xerrors"

	"github.com/duck8823/traceary/domain/model"
)

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
	GetEventDetails(ctx context.Context, dbPath string, eventID string) (*EventDetails, error)
}

// GetEventDetailsQueryService returns event details.
type GetEventDetailsQueryService interface {
	// Run executes the event-details query.
	Run(ctx context.Context, dbPath string, eventID string) (*EventDetails, error)
}

type getEventDetailsQueryService struct {
	eventDetailsFinder EventDetailsFinder
}

// NewGetEventDetailsQueryService creates a GetEventDetailsQueryService.
func NewGetEventDetailsQueryService(eventDetailsFinder EventDetailsFinder) GetEventDetailsQueryService {
	return &getEventDetailsQueryService{eventDetailsFinder: eventDetailsFinder}
}

// Run executes the event-details query.
func (s *getEventDetailsQueryService) Run(
	ctx context.Context,
	dbPath string,
	eventID string,
) (*EventDetails, error) {
	if s.eventDetailsFinder == nil {
		return nil, xerrors.Errorf("event details finder is not configured")
	}
	if strings.TrimSpace(dbPath) == "" {
		return nil, xerrors.Errorf("DB path must not be empty")
	}
	if strings.TrimSpace(eventID) == "" {
		return nil, xerrors.Errorf("event ID must not be empty")
	}

	eventDetails, err := s.eventDetailsFinder.GetEventDetails(ctx, dbPath, eventID)
	if err != nil {
		return nil, xerrors.Errorf("failed to get event details: %w", err)
	}

	return eventDetails, nil
}
