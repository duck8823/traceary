package queryservice

import (
	"context"
	"strings"

	"golang.org/x/xerrors"

	"github.com/duck8823/traceary/domain/port"
)

// GetEventDetailsQueryService returns event details.
type GetEventDetailsQueryService interface {
	// Run executes the event-details query.
	Run(ctx context.Context, eventID string) (*port.EventDetails, error)
}

type getEventDetailsQueryService struct {
	eventDetailsFinder port.EventDetailsFinder
}

// NewGetEventDetailsQueryService creates a GetEventDetailsQueryService.
func NewGetEventDetailsQueryService(eventDetailsFinder port.EventDetailsFinder) GetEventDetailsQueryService {
	return &getEventDetailsQueryService{eventDetailsFinder: eventDetailsFinder}
}

// Run executes the event-details query.
func (s *getEventDetailsQueryService) Run(
	ctx context.Context,
	eventID string,
) (*port.EventDetails, error) {
	if s.eventDetailsFinder == nil {
		return nil, xerrors.Errorf("event details finder is not configured")
	}
	if strings.TrimSpace(eventID) == "" {
		return nil, xerrors.Errorf("event ID must not be empty")
	}

	eventDetails, err := s.eventDetailsFinder.GetEventDetails(ctx, eventID)
	if err != nil {
		return nil, xerrors.Errorf("failed to get event details: %w", err)
	}

	return eventDetails, nil
}
