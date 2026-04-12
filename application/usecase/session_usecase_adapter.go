package usecase

import (
	"context"
	"time"

	"golang.org/x/xerrors"

	apptypes "github.com/duck8823/traceary/application/types"
	"github.com/duck8823/traceary/application/queryservice"
	"github.com/duck8823/traceary/domain/model"
	"github.com/duck8823/traceary/domain/types"
)

type sessionUsecaseAdapter struct {
	recordBoundary RecordSessionBoundaryUsecase
	updateLabel    UpdateSessionLabelUsecase
	sessionQuery   queryservice.SessionQueryService
	eventQuery     queryservice.EventQueryService
}

// NewSessionUsecaseAdapter creates a SessionUsecase that delegates to existing usecases and queryservices.
func NewSessionUsecaseAdapter(
	recordBoundary RecordSessionBoundaryUsecase,
	updateLabel UpdateSessionLabelUsecase,
	sessionQuery queryservice.SessionQueryService,
	eventQuery queryservice.EventQueryService,
) SessionUsecase {
	return &sessionUsecaseAdapter{
		recordBoundary: recordBoundary,
		updateLabel:    updateLabel,
		sessionQuery:   sessionQuery,
		eventQuery:     eventQuery,
	}
}

func (a *sessionUsecaseAdapter) Start(ctx context.Context, client types.Client, agent types.Agent, sessionID types.SessionID, workspace types.Workspace, parentSessionID types.SessionID) (*model.Event, error) {
	event, err := a.recordBoundary.Run(ctx, RecordSessionBoundaryInput{
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

func (a *sessionUsecaseAdapter) End(ctx context.Context, client types.Client, agent types.Agent, sessionID types.SessionID, workspace types.Workspace, summary string) (*model.Event, error) {
	event, err := a.recordBoundary.Run(ctx, RecordSessionBoundaryInput{
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

func (a *sessionUsecaseAdapter) Label(ctx context.Context, sessionID types.SessionID, label string) error {
	if err := a.updateLabel.Run(ctx, UpdateSessionLabelInput{
		SessionID: sessionID.String(),
		Label:     label,
	}); err != nil {
		return xerrors.Errorf("failed to update session label: %w", err)
	}
	return nil
}

func (a *sessionUsecaseAdapter) List(ctx context.Context, criteria SessionListCriteria) ([]apptypes.SessionSummary, error) {
	summaries, err := a.sessionQuery.ListSummaries(ctx, criteria.Limit, criteria.Offset, criteria.SessionID, criteria.Workspace, criteria.Client, criteria.Agent, criteria.Label, criteria.From, criteria.To)
	if err != nil {
		return nil, xerrors.Errorf("failed to list sessions: %w", err)
	}
	return summaries, nil
}

func (a *sessionUsecaseAdapter) Tree(ctx context.Context, workspace types.Workspace, limit int) ([]apptypes.SessionSummary, error) {
	summaries, err := a.sessionQuery.ListSummaries(ctx, limit, 0, types.SessionID(""), workspace, types.Client(""), types.Agent(""), "", types.Empty[time.Time](), types.Empty[time.Time]())
	if err != nil {
		return nil, xerrors.Errorf("failed to list sessions for tree: %w", err)
	}
	return summaries, nil
}

func (a *sessionUsecaseAdapter) Active(ctx context.Context, criteria SessionLookupCriteria) (types.Optional[*model.Event], error) {
	result, err := a.sessionQuery.FindLatest(ctx, criteria.Client, criteria.Agent, criteria.Workspace, true)
	if err != nil {
		return types.Empty[*model.Event](), xerrors.Errorf("failed to find active session: %w", err)
	}
	return result, nil
}

func (a *sessionUsecaseAdapter) Latest(ctx context.Context, criteria SessionLookupCriteria) (types.Optional[*model.Event], error) {
	result, err := a.sessionQuery.FindLatest(ctx, criteria.Client, criteria.Agent, criteria.Workspace, false)
	if err != nil {
		return types.Empty[*model.Event](), xerrors.Errorf("failed to find latest session: %w", err)
	}
	return result, nil
}

func (a *sessionUsecaseAdapter) Handoff(ctx context.Context, sessionID types.SessionID, workspace types.Workspace, recent int) (types.Optional[apptypes.HandoffSummary], error) {
	sessions, err := a.sessionQuery.ListSummaries(ctx, 1, 0, sessionID, workspace, types.Client(""), types.Agent(""), "", types.Empty[time.Time](), types.Empty[time.Time]())
	if err != nil {
		return types.Empty[apptypes.HandoffSummary](), xerrors.Errorf("failed to list sessions for handoff: %w", err)
	}
	if len(sessions) == 0 {
		return types.Empty[apptypes.HandoffSummary](), nil
	}

	session := sessions[0]
	events, err := a.eventQuery.ListRecent(ctx, recent, 0, types.EventKindCommandExecuted, types.Client(""), types.Agent(""), session.SessionID(), types.Workspace(""), false, time.Time{}, time.Time{})
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
