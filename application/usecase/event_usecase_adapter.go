package usecase

import (
	"context"

	"golang.org/x/xerrors"

	"github.com/duck8823/traceary/application/queryservice"
	"github.com/duck8823/traceary/domain/model"
	"github.com/duck8823/traceary/domain/port"
	"github.com/duck8823/traceary/domain/types"
)

type eventUsecaseAdapter struct {
	recordLog       RecordLogUsecase
	recordAudit     RecordCommandAuditUsecase
	listRecent      queryservice.ListRecentEventsQueryService
	searchEvents    queryservice.SearchEventsQueryService
	getEventDetails queryservice.GetEventDetailsQueryService
	getContext      queryservice.GetContextQueryService
}

// NewEventUsecaseAdapter creates an EventUsecase that delegates to existing usecases and queryservices.
func NewEventUsecaseAdapter(
		recordLog RecordLogUsecase,
	recordAudit RecordCommandAuditUsecase,
	listRecent queryservice.ListRecentEventsQueryService,
	searchEvents queryservice.SearchEventsQueryService,
	getEventDetails queryservice.GetEventDetailsQueryService,
	getContext queryservice.GetContextQueryService,
) EventUsecase {
	return &eventUsecaseAdapter{
		recordLog:       recordLog,
		recordAudit:     recordAudit,
		listRecent:      listRecent,
		searchEvents:    searchEvents,
		getEventDetails: getEventDetails,
		getContext:      getContext,
	}
}

func (a *eventUsecaseAdapter) Log(ctx context.Context, message string, kind types.EventKind, client types.Client, agent types.Agent, sessionID types.SessionID, workspace types.Workspace) (*model.Event, error) {
	event, err := a.recordLog.Run(ctx, RecordLogInput{
		Message:   message,
		Kind:      kind.String(),
		Client:    client.String(),
		Agent:     agent.String(),
		SessionID: sessionID.String(),
		Workspace:      workspace.String(),
	})
	if err != nil {
		return nil, xerrors.Errorf("failed to record log: %w", err)
	}
	return event, nil
}

func (a *eventUsecaseAdapter) Audit(ctx context.Context, command string, input string, output string, client types.Client, agent types.Agent, sessionID types.SessionID, workspace types.Workspace, exitCode *int, redaction AuditRedaction) (*model.Event, *model.CommandAudit, error) {
	event, audit, err := a.recordAudit.Run(ctx, RecordCommandAuditInput{
		Command:             command,
		Input:               input,
		Output:              output,
		Client:              client.String(),
		Agent:               agent.String(),
		SessionID:           sessionID.String(),
		Workspace:                workspace.String(),
		ExitCode:            exitCode,
		AllowSecrets:        redaction.AllowSecrets,
		MaxInputBytes:       redaction.MaxInputBytes,
		MaxOutputBytes:      redaction.MaxOutputBytes,
		ExtraRedactPatterns: redaction.ExtraRedactPatterns,
	})
	if err != nil {
		return nil, nil, xerrors.Errorf("failed to record audit: %w", err)
	}
	return event, audit, nil
}

func (a *eventUsecaseAdapter) Search(ctx context.Context, criteria EventSearchCriteria) ([]*model.Event, error) {
	events, err := a.searchEvents.Run(ctx, port.SearchEventsInput{
		Query:        criteria.Query,
		Workspace:         criteria.Workspace.String(),
		SessionID:    criteria.SessionID.String(),
		Client:       criteria.Client.String(),
		Agent:        criteria.Agent.String(),
		Kind:         criteria.Kind.String(),
		From:         criteria.From,
		To:           criteria.To,
		Limit:        criteria.Limit,
		Offset:       criteria.Offset,
		FailuresOnly: criteria.FailuresOnly,
	})
	if err != nil {
		return nil, xerrors.Errorf("failed to search events: %w", err)
	}
	return events, nil
}

func (a *eventUsecaseAdapter) List(ctx context.Context, criteria EventListCriteria) ([]*model.Event, error) {
	events, err := a.listRecent.Run(ctx, port.ListRecentEventsInput{
		Limit:        criteria.Limit,
		Offset:       criteria.Offset,
		Kind:         criteria.Kind.String(),
		Client:       criteria.Client.String(),
		Agent:        criteria.Agent.String(),
		SessionID:    criteria.SessionID.String(),
		Workspace:         criteria.Workspace.String(),
		FailuresOnly: criteria.FailuresOnly,
		From:         criteria.From,
		To:           criteria.To,
	})
	if err != nil {
		return nil, xerrors.Errorf("failed to list events: %w", err)
	}
	return events, nil
}

func (a *eventUsecaseAdapter) Show(ctx context.Context, eventID types.EventID) (*EventDetails, error) {
	portDetails, err := a.getEventDetails.Run(ctx, eventID.String())
	if err != nil {
		return nil, xerrors.Errorf("failed to get event details: %w", err)
	}
	return NewEventDetails(portDetails.Event(), portDetails.CommandAudit())
}

func (a *eventUsecaseAdapter) Context(ctx context.Context, criteria EventContextCriteria) ([]*model.Event, error) {
	events, err := a.getContext.Run(ctx, port.GetContextInput{
		Workspace:      criteria.Workspace.String(),
		SessionID: criteria.SessionID.String(),
		Limit:     criteria.Limit,
	})
	if err != nil {
		return nil, xerrors.Errorf("failed to get context events: %w", err)
	}
	return events, nil
}
