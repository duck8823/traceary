package queryservice

import (
	"context"
	"errors"
	"strings"

	"golang.org/x/xerrors"

	"github.com/duck8823/traceary/domain/model"
)

// FindLatestSessionInput is the input for latest-session lookup.
type FindLatestSessionInput struct {
	Client     string
	Agent      string
	Repo       string
	ActiveOnly bool
}

var (
	// ErrSessionNotFound indicates that no session matches the filters.
	ErrSessionNotFound = xerrors.New("no matching session found")
	// ErrActiveSessionNotFound indicates that no active session matches the filters.
	ErrActiveSessionNotFound = xerrors.New("no matching active session found")
)

// LatestSessionFinder provides access to the latest session-started event.
type LatestSessionFinder interface {
	// FindLatestSessionStartedEvent returns the latest session_started event.
	FindLatestSessionStartedEvent(
		ctx context.Context,
		dbPath string,
		input FindLatestSessionInput,
	) (*model.Event, error)
}

// FindLatestSessionQueryService returns the latest session.
type FindLatestSessionQueryService interface {
	// Run executes the latest-session query.
	Run(ctx context.Context, dbPath string, input FindLatestSessionInput) (*model.Event, error)
}

type findLatestSessionQueryService struct {
	latestSessionFinder LatestSessionFinder
}

// NewFindLatestSessionQueryService creates a FindLatestSessionQueryService.
func NewFindLatestSessionQueryService(latestSessionFinder LatestSessionFinder) FindLatestSessionQueryService {
	return &findLatestSessionQueryService{latestSessionFinder: latestSessionFinder}
}

// Run executes the latest-session query.
func (s *findLatestSessionQueryService) Run(
	ctx context.Context,
	dbPath string,
	input FindLatestSessionInput,
) (*model.Event, error) {
	if s.latestSessionFinder == nil {
		return nil, xerrors.Errorf("latest session finder is not configured")
	}
	if strings.TrimSpace(dbPath) == "" {
		return nil, xerrors.Errorf("DB path must not be empty")
	}

	event, err := s.latestSessionFinder.FindLatestSessionStartedEvent(ctx, dbPath, input)
	if err != nil {
		if errors.Is(err, ErrSessionNotFound) || errors.Is(err, ErrActiveSessionNotFound) {
			//nolint:wrapcheck // Preserve the user-facing not-found message.
			return nil, err
		}
		return nil, xerrors.Errorf("failed to get latest session: %w", err)
	}

	return event, nil
}

// IsSessionLookupNotFound reports whether err is a session-lookup not-found error.
func IsSessionLookupNotFound(err error) bool {
	return errors.Is(err, ErrSessionNotFound) || errors.Is(err, ErrActiveSessionNotFound)
}
