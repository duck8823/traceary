package usecase

import (
	"context"
	"strings"
	"time"

	"golang.org/x/xerrors"

	apptypes "github.com/duck8823/traceary/application/types"
	"github.com/duck8823/traceary/application/queryservice"
	"github.com/duck8823/traceary/domain/model"
	"github.com/duck8823/traceary/domain/types"
)

// RecordSessionBoundaryInput is the input for session start/end recording.
type RecordSessionBoundaryInput struct {
	Client           string
	DefaultClient    string
	Agent            string
	DefaultAgent     string
	SessionID        string
	Workspace        string
	DefaultWorkspace string
	Kind             types.EventKind
	Summary          string
	ParentSessionID  string
}

type sessionUsecase struct {
	eventRepo    model.EventRepository
	sessionRepo  model.SessionRepository
	sessionQuery queryservice.SessionQueryService
	eventQuery   queryservice.EventQueryService
}

// NewSessionUsecase creates a SessionUsecase.
func NewSessionUsecase(
	eventRepo model.EventRepository,
	sessionRepo model.SessionRepository,
	sessionQuery queryservice.SessionQueryService,
	eventQuery queryservice.EventQueryService,
) SessionUsecase {
	return &sessionUsecase{
		eventRepo:    eventRepo,
		sessionRepo:  sessionRepo,
		sessionQuery: sessionQuery,
		eventQuery:   eventQuery,
	}
}

func (u *sessionUsecase) Start(ctx context.Context, client types.Client, agent types.Agent, sessionID types.SessionID, workspace types.Workspace, parentSessionID types.SessionID) (*model.Event, error) {
	event, err := u.recordSessionBoundary(ctx, RecordSessionBoundaryInput{
		Client:          client.String(),
		Agent:           agent.String(),
		SessionID:       sessionID.String(),
		Workspace:       workspace.String(),
		Kind:            types.EventKindSessionStarted,
		ParentSessionID: parentSessionID.String(),
	})
	if err != nil {
		return nil, xerrors.Errorf("failed to start session: %w", err)
	}
	return event, nil
}

func (u *sessionUsecase) End(ctx context.Context, client types.Client, agent types.Agent, sessionID types.SessionID, workspace types.Workspace, summary string) (*model.Event, error) {
	event, err := u.recordSessionBoundary(ctx, RecordSessionBoundaryInput{
		Client:    client.String(),
		Agent:     agent.String(),
		SessionID: sessionID.String(),
		Workspace: workspace.String(),
		Kind:      types.EventKindSessionEnded,
		Summary:   summary,
	})
	if err != nil {
		return nil, xerrors.Errorf("failed to end session: %w", err)
	}
	return event, nil
}

func (u *sessionUsecase) Label(ctx context.Context, sessionID types.SessionID, label string) error {
	trimmedSessionID := strings.TrimSpace(sessionID.String())
	if trimmedSessionID == "" {
		return xerrors.Errorf("session ID must not be empty")
	}

	resolvedSessionID, err := types.SessionIDOf(trimmedSessionID)
	if err != nil {
		return xerrors.Errorf("failed to resolve session ID: %w", err)
	}

	result, err := u.sessionRepo.FindByID(ctx, resolvedSessionID)
	if err != nil {
		return xerrors.Errorf("failed to find session: %w", err)
	}
	if !result.IsPresent() {
		return xerrors.Errorf("session not found: %s", resolvedSessionID)
	}

	session, _ := result.Get()
	session.SetLabel(label)

	if err := u.sessionRepo.Save(ctx, session); err != nil {
		return xerrors.Errorf("failed to save session with label: %w", err)
	}

	return nil
}

func (u *sessionUsecase) List(ctx context.Context, criteria SessionListCriteria) ([]apptypes.SessionSummary, error) {
	if criteria.Limit <= 0 {
		return nil, xerrors.Errorf("limit must be greater than or equal to 1")
	}
	if criteria.Offset < 0 {
		return nil, xerrors.Errorf("offset must be greater than or equal to 0")
	}

	summaries, err := u.sessionQuery.ListSummaries(ctx, criteria.Limit, criteria.Offset, criteria.SessionID, criteria.Workspace, criteria.Client, criteria.Agent, criteria.Label, criteria.From, criteria.To)
	if err != nil {
		return nil, xerrors.Errorf("failed to list sessions: %w", err)
	}
	return summaries, nil
}

func (u *sessionUsecase) Tree(ctx context.Context, workspace types.Workspace, limit int) ([]apptypes.SessionSummary, error) {
	if limit <= 0 {
		return nil, xerrors.Errorf("limit must be greater than or equal to 1")
	}

	summaries, err := u.sessionQuery.ListSummaries(ctx, limit, 0, types.SessionID(""), workspace, types.Client(""), types.Agent(""), "", types.Empty[time.Time](), types.Empty[time.Time]())
	if err != nil {
		return nil, xerrors.Errorf("failed to list sessions for tree: %w", err)
	}
	return summaries, nil
}

func (u *sessionUsecase) Active(ctx context.Context, criteria SessionLookupCriteria) (types.Optional[*model.Event], error) {
	result, err := u.sessionQuery.FindLatest(ctx, criteria.Client, criteria.Agent, criteria.Workspace, true)
	if err != nil {
		return types.Empty[*model.Event](), xerrors.Errorf("failed to find active session: %w", err)
	}
	return result, nil
}

func (u *sessionUsecase) Latest(ctx context.Context, criteria SessionLookupCriteria) (types.Optional[*model.Event], error) {
	result, err := u.sessionQuery.FindLatest(ctx, criteria.Client, criteria.Agent, criteria.Workspace, false)
	if err != nil {
		return types.Empty[*model.Event](), xerrors.Errorf("failed to find latest session: %w", err)
	}
	return result, nil
}

func (u *sessionUsecase) Handoff(ctx context.Context, sessionID types.SessionID, workspace types.Workspace, recent int) (types.Optional[apptypes.HandoffSummary], error) {
	sessions, err := u.sessionQuery.ListSummaries(ctx, 1, 0, sessionID, workspace, types.Client(""), types.Agent(""), "", types.Empty[time.Time](), types.Empty[time.Time]())
	if err != nil {
		return types.Empty[apptypes.HandoffSummary](), xerrors.Errorf("failed to list sessions for handoff: %w", err)
	}
	if len(sessions) == 0 {
		return types.Empty[apptypes.HandoffSummary](), nil
	}

	session := sessions[0]
	events, err := u.eventQuery.ListRecent(ctx, recent, 0, types.EventKindCommandExecuted, types.Client(""), types.Agent(""), session.SessionID(), types.Workspace(""), false, time.Time{}, time.Time{})
	if err != nil {
		return types.Empty[apptypes.HandoffSummary](), xerrors.Errorf("failed to list recent events for handoff: %w", err)
	}

	recentCommands := make([]string, 0, len(events))
	for _, event := range events {
		cmd := event.Body()
		if runes := []rune(cmd); len(runes) > 60 {
			cmd = string(runes[:60]) + "\u2026"
		}
		recentCommands = append(recentCommands, cmd)
	}

	return types.Of(apptypes.HandoffSummaryOf(
		session.SessionID(),
		session.Workspace(),
		session.Label(),
		session.Status(),
		session.TotalEvents(),
		session.CommandCount(),
		session.Agents(),
		session.Summary(),
		recentCommands,
	)), nil
}

// recordSessionBoundary persists a session boundary event.
func (u *sessionUsecase) recordSessionBoundary(
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
	// For session end, verify the session exists before writing anything.
	// This avoids creating an orphaned session_ended event for a missing session.
	var existingSession *model.Session
	if input.Kind == types.EventKindSessionEnded && u.sessionRepo != nil {
		existing, err := u.sessionRepo.FindByID(ctx, sessionID)
		if err != nil {
			return nil, xerrors.Errorf("failed to find session for end: %w", err)
		}
		session, ok := existing.Get()
		if !ok {
			return nil, xerrors.Errorf("cannot end session %s: %w", sessionID, model.ErrInvalidSessionState)
		}
		existingSession = session
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
		} else if existingSession != nil {
			existingSession.End(event.CreatedAt(), input.Summary)
			if err := u.sessionRepo.Save(ctx, existingSession); err != nil {
				return nil, xerrors.Errorf("failed to save session end: %w", err)
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

func (u *sessionUsecase) resolveSessionBoundaryAttribution(
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
