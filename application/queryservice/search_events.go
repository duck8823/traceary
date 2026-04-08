package queryservice

import (
	"context"
	"slices"
	"strings"
	"time"

	"golang.org/x/xerrors"

	"github.com/duck8823/traceary/domain/model"
	"github.com/duck8823/traceary/domain/types"
)

// SearchEventsInput is the input for event search.
type SearchEventsInput struct {
	Query     string
	Repo      string
	SessionID string
	Client    string
	Agent     string
	Kind      string
	From      time.Time
	To        time.Time
	Limit     int
	Offset    int
}

// EventSearcher provides event search.
type EventSearcher interface {
	// SearchEvents returns matching events in descending time order.
	SearchEvents(ctx context.Context, dbPath string, input SearchEventsInput) ([]*model.Event, error)
}

// SearchEventsQueryService searches events.
type SearchEventsQueryService interface {
	// Run executes the event search query.
	Run(ctx context.Context, dbPath string, input SearchEventsInput) ([]*model.Event, error)
}

type searchEventsQueryService struct {
	eventSearcher EventSearcher
}

// NewSearchEventsQueryService creates a SearchEventsQueryService.
func NewSearchEventsQueryService(eventSearcher EventSearcher) SearchEventsQueryService {
	return &searchEventsQueryService{eventSearcher: eventSearcher}
}

// Run executes the event search query.
func (s *searchEventsQueryService) Run(
	ctx context.Context,
	dbPath string,
	input SearchEventsInput,
) ([]*model.Event, error) {
	if s.eventSearcher == nil {
		return nil, xerrors.Errorf("event searcher is not configured")
	}
	if strings.TrimSpace(dbPath) == "" {
		return nil, xerrors.Errorf("DB path must not be empty")
	}
	if !hasSearchConstraint(input) {
		return nil, xerrors.Errorf("at least one search filter is required")
	}
	if input.Limit <= 0 {
		return nil, xerrors.Errorf("limit must be greater than or equal to 1")
	}
	if input.Offset < 0 {
		return nil, xerrors.Errorf("offset must be greater than or equal to 0")
	}
	if !input.From.IsZero() && !input.To.IsZero() && input.From.After(input.To) {
		return nil, xerrors.Errorf("from must be earlier than to")
	}
	input.Query = strings.TrimSpace(input.Query)
	input.Repo = strings.TrimSpace(input.Repo)
	input.SessionID = strings.TrimSpace(input.SessionID)
	input.Client = strings.TrimSpace(input.Client)
	input.Agent = strings.TrimSpace(input.Agent)

	kind, err := resolveOptionalSearchKind(input.Kind)
	if err != nil {
		return nil, err
	}
	input.Kind = kind.String()

	events, err := s.eventSearcher.SearchEvents(ctx, dbPath, input)
	if err != nil {
		return nil, xerrors.Errorf("failed to search events: %w", err)
	}

	return events, nil
}

func hasSearchConstraint(input SearchEventsInput) bool {
	return strings.TrimSpace(input.Query) != "" ||
		strings.TrimSpace(input.Repo) != "" ||
		strings.TrimSpace(input.SessionID) != "" ||
		strings.TrimSpace(input.Client) != "" ||
		strings.TrimSpace(input.Agent) != "" ||
		strings.TrimSpace(input.Kind) != "" ||
		!input.From.IsZero() ||
		!input.To.IsZero()
}

func resolveOptionalSearchKind(value string) (types.EventKind, error) {
	trimmedValue := strings.TrimSpace(value)
	if trimmedValue == "" {
		return "", nil
	}
	if slices.Contains([]string{"audit"}, trimmedValue) {
		return types.EventKindCommandExecuted, nil
	}

	kind, err := types.EventKindOf(trimmedValue)
	if err != nil {
		return "", xerrors.Errorf("failed to resolve kind: %w", err)
	}

	return kind, nil
}
