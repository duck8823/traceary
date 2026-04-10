package usecase

import (
	"context"
	"errors"
	"strings"
	"time"

	"golang.org/x/xerrors"

	"github.com/duck8823/traceary/domain/model"
	"github.com/duck8823/traceary/domain/types"
)

// RecordSessionBoundaryInput is the input for session start/end recording.
type RecordSessionBoundaryInput struct {
	DBPath        string
	Client        string
	DefaultClient string
	Agent         string
	DefaultAgent  string
	SessionID     string
	Repo          string
	DefaultRepo   string
	Kind              types.EventKind
	Summary           string
	ParentSessionID   string
}

// ErrSessionStartedEventNotFound indicates the target session has no start event.
var ErrSessionStartedEventNotFound = xerrors.New("session_started event was not found for the target session")

// SessionSaver persists session metadata.
type SessionSaver interface {
	// SaveSession creates or updates a session record.
	SaveSession(ctx context.Context, dbPath string, session *model.Session) error
}

// SessionStartedEventFinder provides lookup for session_started events.
type SessionStartedEventFinder interface {
	// FindSessionStartedEvent returns the latest session_started event for the target session.
	FindSessionStartedEvent(
		ctx context.Context,
		dbPath string,
		sessionID types.SessionID,
	) (*model.Event, error)
}

// RecordSessionBoundaryUsecase records session boundary events.
type RecordSessionBoundaryUsecase interface {
	// Run persists a session boundary event.
	Run(ctx context.Context, input RecordSessionBoundaryInput) (*model.Event, error)
}

type recordSessionBoundaryUsecase struct {
	eventSaver                EventSaver
	sessionSaver              SessionSaver
	sessionStartedEventFinder SessionStartedEventFinder
}

// NewRecordSessionBoundaryUsecase creates RecordSessionBoundaryUsecase.
func NewRecordSessionBoundaryUsecase(
	eventSaver EventSaver,
	sessionStartedEventFinder SessionStartedEventFinder,
	sessionSavers ...SessionSaver,
) RecordSessionBoundaryUsecase {
	var sessionSaver SessionSaver
	if len(sessionSavers) > 0 {
		sessionSaver = sessionSavers[0]
	}
	return &recordSessionBoundaryUsecase{
		eventSaver:                eventSaver,
		sessionSaver:              sessionSaver,
		sessionStartedEventFinder: sessionStartedEventFinder,
	}
}

// Run persists a session boundary event.
func (u *recordSessionBoundaryUsecase) Run(
	ctx context.Context,
	input RecordSessionBoundaryInput,
) (*model.Event, error) {
	if u.eventSaver == nil {
		return nil, xerrors.Errorf("event saver is not configured")
	}
	trimmedDBPath := strings.TrimSpace(input.DBPath)
	if trimmedDBPath == "" {
		return nil, xerrors.Errorf("DB path must not be empty")
	}

	sessionID, err := resolveSessionBoundaryID(input.Kind, input.SessionID)
	if err != nil {
		return nil, xerrors.Errorf("failed to resolve session ID: %w", err)
	}
	resolvedClient, resolvedAgentValue, resolvedRepo, err := u.resolveSessionBoundaryAttribution(
		ctx,
		trimmedDBPath,
		input,
		sessionID,
	)
	if err != nil {
		return nil, xerrors.Errorf("failed to resolve session boundary attribution: %w", err)
	}
	agent, err := types.AgentOf(resolvedAgentValue)
	if err != nil {
		return nil, xerrors.Errorf("failed to resolve agent: %w", err)
	}
	eventID, err := newEventID()
	if err != nil {
		return nil, xerrors.Errorf("failed to generate event ID: %w", err)
	}

	event, err := model.NewEvent(
		eventID,
		input.Kind,
		resolvedClient,
		agent,
		sessionID,
		resolvedRepo,
		sessionBoundaryBody(input.Kind),
	)
	if err != nil {
		return nil, xerrors.Errorf("failed to build session boundary event: %w", err)
	}
	if err := u.eventSaver.Save(ctx, trimmedDBPath, event); err != nil {
		return nil, xerrors.Errorf("failed to save session boundary event: %w", err)
	}

	if u.sessionSaver != nil {
		session := buildSessionFromBoundary(event, input.Kind, input.Summary, input.ParentSessionID)
		if err := u.sessionSaver.SaveSession(ctx, trimmedDBPath, session); err != nil {
			return nil, xerrors.Errorf("failed to save session metadata: %w", err)
		}
	}

	return event, nil
}

func buildSessionFromBoundary(event *model.Event, kind types.EventKind, summary string, parentSessionID string) *model.Session {
	switch kind {
	case types.EventKindSessionStarted:
		return model.SessionOf(
			event.SessionID(),
			event.CreatedAt(),
			nil,
			event.Client(),
			event.Agent(),
			event.Repo(),
			"", "", parentSessionID,
		)
	default:
		// For session end, started_at is not available from the event.
		// Use zero value since SaveSession only updates ended_at.
		endedAt := event.CreatedAt()
		return model.SessionOf(
			event.SessionID(),
			time.Time{},
			&endedAt,
			event.Client(),
			event.Agent(),
			event.Repo(),
			"", summary, "",
		)
	}
}

func (u *recordSessionBoundaryUsecase) resolveSessionBoundaryAttribution(
	ctx context.Context,
	dbPath string,
	input RecordSessionBoundaryInput,
	sessionID types.SessionID,
) (string, string, string, error) {
	resolvedClient := strings.TrimSpace(input.Client)
	resolvedAgentValue := strings.TrimSpace(input.Agent)
	resolvedRepo := strings.TrimSpace(input.Repo)

	if input.Kind == types.EventKindSessionEnded && u.sessionStartedEventFinder != nil {
		if resolvedClient == "" || resolvedAgentValue == "" || resolvedRepo == "" {
			startedEvent, err := u.sessionStartedEventFinder.FindSessionStartedEvent(ctx, dbPath, sessionID)
			if err != nil && !errors.Is(err, ErrSessionStartedEventNotFound) {
				return "", "", "", xerrors.Errorf("failed to get session_started event: %w", err)
			}
			if err == nil && startedEvent != nil {
				if resolvedClient == "" {
					resolvedClient = startedEvent.Client()
				}
				if resolvedAgentValue == "" {
					resolvedAgentValue = startedEvent.Agent().String()
				}
				if resolvedRepo == "" {
					resolvedRepo = startedEvent.Repo()
				}
			}
		}
	}

	if resolvedClient == "" {
		resolvedClient = strings.TrimSpace(input.DefaultClient)
	}
	if resolvedAgentValue == "" {
		resolvedAgentValue = strings.TrimSpace(input.DefaultAgent)
	}
	if resolvedRepo == "" {
		resolvedRepo = strings.TrimSpace(input.DefaultRepo)
	}

	return resolvedClient, resolvedAgentValue, resolvedRepo, nil
}

func resolveSessionBoundaryID(
	eventKind types.EventKind,
	sessionIDValue string,
) (types.SessionID, error) {
	switch eventKind {
	case types.EventKindSessionStarted:
		trimmedValue := strings.TrimSpace(sessionIDValue)
		if trimmedValue == "" {
			sessionID, err := newSessionID()
			if err != nil {
				return types.SessionID(""), xerrors.Errorf("failed to generate session ID: %w", err)
			}
			return sessionID, nil
		}

		sessionID, err := types.SessionIDOf(trimmedValue)
		if err != nil {
			return types.SessionID(""), xerrors.Errorf("failed to convert session ID: %w", err)
		}

		return sessionID, nil
	case types.EventKindSessionEnded:
		sessionID, err := types.SessionIDOf(sessionIDValue)
		if err != nil {
			return types.SessionID(""), xerrors.Errorf("failed to convert session ID: %w", err)
		}

		return sessionID, nil
	default:
		return types.SessionID(""), xerrors.Errorf("unsupported event kind for session boundary: %s", eventKind)
	}
}

func sessionBoundaryBody(eventKind types.EventKind) string {
	switch eventKind {
	case types.EventKindSessionStarted:
		return "session started"
	case types.EventKindSessionEnded:
		return "session ended"
	default:
		return "session boundary"
	}
}
