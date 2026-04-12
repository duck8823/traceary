package usecase

import (
	"context"
	"strings"
	"time"

	"golang.org/x/xerrors"

	"github.com/duck8823/traceary/domain/model"
	"github.com/duck8823/traceary/domain/types"
)

// RecordSessionBoundaryInput is the input for session start/end recording.
type RecordSessionBoundaryInput struct {
	Client        string
	DefaultClient string
	Agent         string
	DefaultAgent  string
	SessionID     string
	Workspace string
	DefaultWorkspace   string
	Kind              types.EventKind
	Summary           string
	ParentSessionID   string
}

// RecordSessionBoundaryUsecase records session boundary events.
type RecordSessionBoundaryUsecase interface {
	// Run persists a session boundary event.
	Run(ctx context.Context, input RecordSessionBoundaryInput) (*model.Event, error)
}

type recordSessionBoundaryUsecase struct {
	eventRepo   model.EventRepository
	sessionRepo model.SessionRepository
}

// NewRecordSessionBoundaryUsecase creates RecordSessionBoundaryUsecase.
func NewRecordSessionBoundaryUsecase(
	eventRepo model.EventRepository,
	sessionRepo model.SessionRepository,
) RecordSessionBoundaryUsecase {
	return &recordSessionBoundaryUsecase{
		eventRepo:   eventRepo,
		sessionRepo: sessionRepo,
	}
}

// Run persists a session boundary event.
func (u *recordSessionBoundaryUsecase) Run(
	ctx context.Context,
	input RecordSessionBoundaryInput,
) (*model.Event, error) {
	if u.eventRepo == nil {
		return nil, xerrors.Errorf("event repository is not configured")
	}

	sessionID, err := resolveSessionBoundaryID(input.Kind, input.SessionID)
	if err != nil {
		return nil, xerrors.Errorf("failed to resolve session ID: %w", err)
	}
	resolvedClient, resolvedAgentValue, resolvedWorkspace, err := u.resolveSessionBoundaryAttribution(
		ctx,
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
		resolvedWorkspace,
		sessionBoundaryBody(input.Kind),
	)
	if err != nil {
		return nil, xerrors.Errorf("failed to build session boundary event: %w", err)
	}
	if err := u.eventRepo.Save(ctx, event); err != nil {
		return nil, xerrors.Errorf("failed to save session boundary event: %w", err)
	}

	if u.sessionRepo != nil {
		if input.Kind == types.EventKindSessionStarted {
			session := buildSessionFromBoundary(event, input.ParentSessionID)
			if err := u.sessionRepo.Save(ctx, session); err != nil {
				return nil, xerrors.Errorf("failed to save session metadata: %w", err)
			}
		} else {
			existing, err := u.sessionRepo.FindByID(ctx, event.SessionID())
			if err != nil {
				return nil, xerrors.Errorf("failed to find session for end: %w", err)
			}
			if session, ok := existing.Get(); ok {
				session.End(event.CreatedAt(), input.Summary)
				if err := u.sessionRepo.Save(ctx, session); err != nil {
					return nil, xerrors.Errorf("failed to save session end: %w", err)
				}
			}
		}
	}

	return event, nil
}

func buildSessionFromBoundary(event *model.Event, parentSessionID string) *model.Session {
	return model.SessionOf(
		event.SessionID(),
		event.CreatedAt(),
		types.Empty[time.Time](),
		event.Client(),
		event.Agent(),
		event.Workspace(),
		"", "", parentSessionID,
	)
}

func (u *recordSessionBoundaryUsecase) resolveSessionBoundaryAttribution(
	ctx context.Context,
	input RecordSessionBoundaryInput,
	sessionID types.SessionID,
) (string, string, string, error) {
	resolvedClient := strings.TrimSpace(input.Client)
	resolvedAgentValue := strings.TrimSpace(input.Agent)
	resolvedWorkspace := strings.TrimSpace(input.Workspace)

	if input.Kind == types.EventKindSessionEnded && u.sessionRepo != nil {
		if resolvedClient == "" || resolvedAgentValue == "" || resolvedWorkspace == "" {
			result, err := u.sessionRepo.FindByID(ctx, sessionID)
			if err != nil {
				return "", "", "", xerrors.Errorf("failed to get session: %w", err)
			}
			if startedSession, ok := result.Get(); ok {
				if resolvedClient == "" {
					resolvedClient = startedSession.Client()
				}
				if resolvedAgentValue == "" {
					resolvedAgentValue = startedSession.Agent().String()
				}
				if resolvedWorkspace == "" {
					resolvedWorkspace = startedSession.Workspace()
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
	if resolvedWorkspace == "" {
		resolvedWorkspace = strings.TrimSpace(input.DefaultWorkspace)
	}

	return resolvedClient, resolvedAgentValue, resolvedWorkspace, nil
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
