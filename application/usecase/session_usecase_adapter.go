package usecase

import (
	"context"

	"golang.org/x/xerrors"

	"github.com/duck8823/traceary/application/queryservice"
	"github.com/duck8823/traceary/domain/model"
	"github.com/duck8823/traceary/domain/port"
	"github.com/duck8823/traceary/domain/types"
)

type sessionUsecaseAdapter struct {
	recordBoundary    RecordSessionBoundaryUsecase
	updateLabel       UpdateSessionLabelUsecase
	findLatestSession queryservice.FindLatestSessionQueryService
	listSessions      queryservice.ListSessionsQueryService
	listRecentEvents  queryservice.ListRecentEventsQueryService
}

// NewSessionUsecaseAdapter creates a SessionUsecase that delegates to existing usecases and queryservices.
func NewSessionUsecaseAdapter(
		recordBoundary RecordSessionBoundaryUsecase,
	updateLabel UpdateSessionLabelUsecase,
	findLatestSession queryservice.FindLatestSessionQueryService,
	listSessions queryservice.ListSessionsQueryService,
	listRecentEvents queryservice.ListRecentEventsQueryService,
) SessionUsecase {
	return &sessionUsecaseAdapter{
		recordBoundary:    recordBoundary,
		updateLabel:       updateLabel,
		findLatestSession: findLatestSession,
		listSessions:      listSessions,
		listRecentEvents:  listRecentEvents,
	}
}

func (a *sessionUsecaseAdapter) Start(ctx context.Context, client types.Client, agent types.Agent, sessionID types.SessionID, workspace types.Workspace, parentSessionID types.SessionID) (*model.Event, error) {
	event, err := a.recordBoundary.Run(ctx, RecordSessionBoundaryInput{
		Client:          client.String(),
		Agent:           agent.String(),
		SessionID:       sessionID.String(),
		Workspace:            workspace.String(),
		Kind:            types.EventKindSessionStarted,
		ParentSessionID: parentSessionID.String(),
	})
	if err != nil {
		return nil, xerrors.Errorf("failed to start session: %w", err)
	}
	return event, nil
}

func (a *sessionUsecaseAdapter) End(ctx context.Context, client types.Client, agent types.Agent, sessionID types.SessionID, workspace types.Workspace, summary string) (*model.Event, error) {
	event, err := a.recordBoundary.Run(ctx, RecordSessionBoundaryInput{
		Client:    client.String(),
		Agent:     agent.String(),
		SessionID: sessionID.String(),
		Workspace:      workspace.String(),
		Kind:      types.EventKindSessionEnded,
		Summary:   summary,
	})
	if err != nil {
		return nil, xerrors.Errorf("failed to end session: %w", err)
	}
	return event, nil
}

func (a *sessionUsecaseAdapter) Label(ctx context.Context, sessionID types.SessionID, label string) error {
	if err := a.updateLabel.Run(ctx, UpdateSessionLabelInput{
		SessionID: sessionID.String(),
		Label:     label,
	}); err != nil {
		return xerrors.Errorf("failed to update session label: %w", err)
	}
	return nil
}

func (a *sessionUsecaseAdapter) List(ctx context.Context, criteria SessionListCriteria) ([]*SessionSummary, error) {
	portSummaries, err := a.listSessions.Run(ctx, port.ListSessionsInput{
		Limit:     criteria.Limit,
		Offset:    criteria.Offset,
		SessionID: criteria.SessionID.String(),
		Workspace:      criteria.Workspace.String(),
		Client:    criteria.Client.String(),
		Agent:     criteria.Agent.String(),
		Label:     criteria.Label,
		From:      criteria.From,
		To:        criteria.To,
	})
	if err != nil {
		return nil, xerrors.Errorf("failed to list sessions: %w", err)
	}
	return convertSessionSummaries(portSummaries), nil
}

func (a *sessionUsecaseAdapter) Tree(ctx context.Context, workspace types.Workspace, limit int) ([]*SessionSummary, error) {
	portSummaries, err := a.listSessions.Run(ctx, port.ListSessionsInput{
		Limit: limit,
		Workspace:  workspace.String(),
	})
	if err != nil {
		return nil, xerrors.Errorf("failed to list sessions for tree: %w", err)
	}
	return convertSessionSummaries(portSummaries), nil
}

func (a *sessionUsecaseAdapter) Active(ctx context.Context, criteria SessionLookupCriteria) (*model.Event, error) {
	event, err := a.findLatestSession.Run(ctx, port.FindLatestSessionInput{
		Client:     criteria.Client.String(),
		Agent:      criteria.Agent.String(),
		Workspace:       criteria.Workspace.String(),
		ActiveOnly: true,
	})
	if err != nil {
		return nil, xerrors.Errorf("failed to find active session: %w", err)
	}
	return event, nil
}

func (a *sessionUsecaseAdapter) Latest(ctx context.Context, criteria SessionLookupCriteria) (*model.Event, error) {
	event, err := a.findLatestSession.Run(ctx, port.FindLatestSessionInput{
		Client:     criteria.Client.String(),
		Agent:      criteria.Agent.String(),
		Workspace:       criteria.Workspace.String(),
		ActiveOnly: false,
	})
	if err != nil {
		return nil, xerrors.Errorf("failed to find latest session: %w", err)
	}
	return event, nil
}

func (a *sessionUsecaseAdapter) Handoff(ctx context.Context, sessionID types.SessionID, workspace types.Workspace, recent int) (*HandoffSummary, error) {
	sessions, err := a.listSessions.Run(ctx, port.ListSessionsInput{
		Limit:     1,
		SessionID: sessionID.String(),
		Workspace:      workspace.String(),
	})
	if err != nil {
		return nil, xerrors.Errorf("failed to list sessions for handoff: %w", err)
	}
	if len(sessions) == 0 {
		return nil, nil
	}

	session := sessions[0]
	events, err := a.listRecentEvents.Run(ctx, port.ListRecentEventsInput{
		Limit:     recent,
		SessionID: session.SessionID,
		Kind:      types.EventKindCommandExecuted.String(),
	})
	if err != nil {
		return nil, xerrors.Errorf("failed to list recent events for handoff: %w", err)
	}

	recentCommands := make([]string, 0, len(events))
	for _, event := range events {
		cmd := event.Body()
		if runes := []rune(cmd); len(runes) > 60 {
			cmd = string(runes[:60]) + "…"
		}
		recentCommands = append(recentCommands, cmd)
	}

	sid := types.SessionID(session.SessionID)
	ws := types.Workspace(session.Workspace)

	return &HandoffSummary{
		SessionID:      sid,
		Workspace:      ws,
		Label:          session.Label,
		Status:         session.Status,
		TotalEvents:    session.TotalEvents,
		CommandCount:   session.CommandCount,
		Agents:         session.Agents,
		Summary:        session.Summary,
		RecentCommands: recentCommands,
	}, nil
}

func convertSessionSummaries(portSummaries []*port.SessionSummary) []*SessionSummary {
	summaries := make([]*SessionSummary, 0, len(portSummaries))
	for _, ps := range portSummaries {
		summaries = append(summaries, &SessionSummary{
			SessionID:       types.SessionID(ps.SessionID),
			Workspace:       types.Workspace(ps.Workspace),
			StartedAt:       ps.StartedAt,
			EndedAt:         ps.EndedAt,
			Status:          ps.Status,
			TotalEvents:     ps.TotalEvents,
			CommandCount:    ps.CommandCount,
			Agents:          ps.Agents,
			Label:           ps.Label,
			Summary:         ps.Summary,
			ParentSessionID: types.SessionID(ps.ParentSessionID),
		})
	}
	return summaries
}
