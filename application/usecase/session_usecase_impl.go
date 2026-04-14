package usecase

import (
	"context"
	"strings"
	"time"

	"golang.org/x/xerrors"

	"github.com/duck8823/traceary/application/queryservice"
	apptypes "github.com/duck8823/traceary/application/types"
	"github.com/duck8823/traceary/domain/model"
	"github.com/duck8823/traceary/domain/types"
)

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
	if u.sessionRepo == nil {
		return nil, xerrors.Errorf("session repository is not configured")
	}

	resolvedSessionID, generated, err := u.resolveSessionStartID(sessionID)
	if err != nil {
		return nil, xerrors.Errorf("failed to start session: %w", err)
	}
	if _, err := types.AgentOf(agent.String()); err != nil {
		return nil, xerrors.Errorf("failed to start session: %w", err)
	}

	// When the caller provided an explicit session ID, the session must not
	// already exist; otherwise the start would silently no-op the session row
	// while still appending a session_started event.
	if !generated {
		existing, err := u.sessionRepo.FindByID(ctx, resolvedSessionID)
		if err != nil {
			return nil, xerrors.Errorf("failed to check existing session: %w", err)
		}
		if _, ok := existing.Value(); ok {
			return nil, xerrors.Errorf("cannot start session %s: %w", resolvedSessionID, model.ErrInvalidSessionState)
		}
	}

	event, err := u.buildBoundaryEvent(types.EventKindSessionStarted, client, agent, resolvedSessionID, workspace)
	if err != nil {
		return nil, xerrors.Errorf("failed to start session: %w", err)
	}

	session := buildSessionFromBoundary(event, parentSessionID)
	if err := u.sessionRepo.SaveBoundary(ctx, session, event); err != nil {
		return nil, xerrors.Errorf("failed to save session start: %w", err)
	}

	return event, nil
}

func (u *sessionUsecase) End(ctx context.Context, client types.Client, agent types.Agent, sessionID types.SessionID, workspace types.Workspace, summary string) (*model.Event, error) {
	if u.sessionRepo == nil {
		return nil, xerrors.Errorf("session repository is not configured")
	}

	resolvedSessionID, err := types.SessionIDOf(sessionID.String())
	if err != nil {
		return nil, xerrors.Errorf("failed to end session: %w", err)
	}

	existing, err := u.sessionRepo.FindByID(ctx, resolvedSessionID)
	if err != nil {
		return nil, xerrors.Errorf("failed to find session for end: %w", err)
	}
	existingSession, ok := existing.Value()
	if !ok {
		return nil, xerrors.Errorf("cannot end session %s: %w", resolvedSessionID, model.ErrInvalidSessionState)
	}

	resolvedClient, resolvedAgent, resolvedWorkspace := inheritAttribution(client, agent, workspace, existingSession)
	if _, err := types.AgentOf(resolvedAgent.String()); err != nil {
		return nil, xerrors.Errorf("failed to end session: %w", err)
	}

	event, err := u.buildBoundaryEvent(types.EventKindSessionEnded, resolvedClient, resolvedAgent, resolvedSessionID, resolvedWorkspace)
	if err != nil {
		return nil, xerrors.Errorf("failed to end session: %w", err)
	}

	if err := existingSession.End(event.CreatedAt(), summary); err != nil {
		return nil, xerrors.Errorf("failed to end session: %w", err)
	}
	if err := u.sessionRepo.SaveBoundary(ctx, existingSession, event); err != nil {
		return nil, xerrors.Errorf("failed to save session end: %w", err)
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
	if _, ok := result.Value(); !ok {
		return xerrors.Errorf("session not found: %s", resolvedSessionID)
	}

	session, _ := result.Value()
	session.SetLabel(label)

	if err := u.sessionRepo.Save(ctx, session); err != nil {
		return xerrors.Errorf("failed to save session with label: %w", err)
	}

	return nil
}

func (u *sessionUsecase) List(ctx context.Context, criteria apptypes.SessionListCriteria) ([]apptypes.SessionSummary, error) {
	if criteria.Limit() <= 0 {
		return nil, xerrors.Errorf("limit must be greater than or equal to 1")
	}
	if criteria.Offset() < 0 {
		return nil, xerrors.Errorf("offset must be greater than or equal to 0")
	}

	summaries, err := u.sessionQuery.ListSummaries(ctx, criteria.Limit(), criteria.Offset(), criteria.SessionID(), criteria.Workspace(), criteria.Client(), criteria.Agent(), criteria.Label(), criteria.From(), criteria.To())
	if err != nil {
		return nil, xerrors.Errorf("failed to list sessions: %w", err)
	}
	return summaries, nil
}

func (u *sessionUsecase) Tree(ctx context.Context, workspace types.Workspace, limit int) ([]apptypes.SessionSummary, error) {
	if limit <= 0 {
		return nil, xerrors.Errorf("limit must be greater than or equal to 1")
	}

	summaries, err := u.sessionQuery.ListSummaries(ctx, limit, 0, types.SessionID(""), workspace, types.Client(""), types.Agent(""), "", types.None[time.Time](), types.None[time.Time]())
	if err != nil {
		return nil, xerrors.Errorf("failed to list sessions for tree: %w", err)
	}
	return summaries, nil
}

func (u *sessionUsecase) Active(ctx context.Context, criteria apptypes.SessionLookupCriteria) (types.Optional[*model.Event], error) {
	result, err := u.sessionQuery.FindLatest(ctx, criteria.Client(), criteria.Agent(), criteria.Workspace(), true)
	if err != nil {
		return types.None[*model.Event](), xerrors.Errorf("failed to find active session: %w", err)
	}
	return result, nil
}

func (u *sessionUsecase) Latest(ctx context.Context, criteria apptypes.SessionLookupCriteria) (types.Optional[*model.Event], error) {
	result, err := u.sessionQuery.FindLatest(ctx, criteria.Client(), criteria.Agent(), criteria.Workspace(), false)
	if err != nil {
		return types.None[*model.Event](), xerrors.Errorf("failed to find latest session: %w", err)
	}
	return result, nil
}

func (u *sessionUsecase) Handoff(ctx context.Context, sessionID types.SessionID, workspace types.Workspace, recent int) (types.Optional[apptypes.HandoffSummary], error) {
	builder, err := newContextPackBuilder(u.sessionQuery, u.eventQuery, nil)
	if err != nil {
		return types.None[apptypes.HandoffSummary](), xerrors.Errorf("failed to initialize handoff context builder: %w", err)
	}

	result, err := builder.Build(ctx, apptypes.NewContextPackCriteriaBuilder().
		SessionID(sessionID).
		Workspace(workspace).
		RecentCommandsLimit(recent).
		MemoryLimit(0).
		Build())
	if err != nil {
		return types.None[apptypes.HandoffSummary](), xerrors.Errorf("failed to build handoff context pack: %w", err)
	}
	if _, ok := result.Value(); !ok {
		return types.None[apptypes.HandoffSummary](), nil
	}

	pack, _ := result.Value()
	return types.Some(apptypes.HandoffSummaryFromContextPack(pack)), nil
}

// resolveSessionStartID returns the session ID for a session start, generating
// a new one when the caller passed an empty value. The boolean return reports
// whether the ID was generated (true) or supplied by the caller (false).
func (u *sessionUsecase) resolveSessionStartID(sessionID types.SessionID) (types.SessionID, bool, error) {
	trimmedValue := strings.TrimSpace(sessionID.String())
	if trimmedValue == "" {
		generated, err := newSessionID()
		if err != nil {
			return types.SessionID(""), false, xerrors.Errorf("failed to generate session ID: %w", err)
		}
		return generated, true, nil
	}

	resolved, err := types.SessionIDOf(trimmedValue)
	if err != nil {
		return types.SessionID(""), false, xerrors.Errorf("failed to convert session ID: %w", err)
	}
	return resolved, false, nil
}

// buildBoundaryEvent constructs the session start/end event.
func (u *sessionUsecase) buildBoundaryEvent(
	kind types.EventKind,
	client types.Client,
	agent types.Agent,
	sessionID types.SessionID,
	workspace types.Workspace,
) (*model.Event, error) {
	eventID, err := newEventID()
	if err != nil {
		return nil, xerrors.Errorf("failed to generate event ID: %w", err)
	}
	event, err := model.NewEvent(eventID, kind, client, agent, sessionID, workspace, sessionBoundaryBody(kind))
	if err != nil {
		return nil, xerrors.Errorf("failed to build session boundary event: %w", err)
	}
	return event, nil
}

// inheritAttribution fills empty caller-provided fields from the stored
// session aggregate. Explicit caller values always win over the stored
// aggregate.
func inheritAttribution(
	client types.Client,
	agent types.Agent,
	workspace types.Workspace,
	stored *model.Session,
) (types.Client, types.Agent, types.Workspace) {
	resolvedClient := types.Client(strings.TrimSpace(client.String()))
	resolvedAgent := types.Agent(strings.TrimSpace(agent.String()))
	resolvedWorkspace := types.Workspace(strings.TrimSpace(workspace.String()))

	if resolvedClient.String() == "" {
		resolvedClient = stored.Client()
	}
	if resolvedAgent.String() == "" {
		resolvedAgent = stored.Agent()
	}
	if resolvedWorkspace.String() == "" {
		resolvedWorkspace = stored.Workspace()
	}
	return resolvedClient, resolvedAgent, resolvedWorkspace
}

func buildSessionFromBoundary(event *model.Event, parentSessionID types.SessionID) *model.Session {
	return model.SessionOf(
		event.SessionID(),
		event.CreatedAt(),
		types.None[time.Time](),
		event.Client(),
		event.Agent(),
		event.Workspace(),
		"", "", parentSessionID,
	)
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
