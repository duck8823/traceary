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
	dbPath          string
	recordLog       RecordLogUsecase
	recordAudit     RecordCommandAuditUsecase
	listRecent      queryservice.ListRecentEventsQueryService
	searchEvents    queryservice.SearchEventsQueryService
	getEventDetails queryservice.GetEventDetailsQueryService
	getContext      queryservice.GetContextQueryService
}

// NewEventUsecaseAdapter creates an EventUsecase that delegates to existing usecases and queryservices.
func NewEventUsecaseAdapter(
	dbPath string,
	recordLog RecordLogUsecase,
	recordAudit RecordCommandAuditUsecase,
	listRecent queryservice.ListRecentEventsQueryService,
	searchEvents queryservice.SearchEventsQueryService,
	getEventDetails queryservice.GetEventDetailsQueryService,
	getContext queryservice.GetContextQueryService,
) EventUsecase {
	return &eventUsecaseAdapter{
		dbPath:          dbPath,
		recordLog:       recordLog,
		recordAudit:     recordAudit,
		listRecent:      listRecent,
		searchEvents:    searchEvents,
		getEventDetails: getEventDetails,
		getContext:      getContext,
	}
}

func (a *eventUsecaseAdapter) Log(ctx context.Context, message string, client types.Client, agent types.Agent, sessionID types.SessionID, workspace types.Workspace) (*model.Event, error) {
	event, err := a.recordLog.Run(ctx, RecordLogInput{
		DBPath:    a.dbPath,
		Message:   message,
		Client:    client.String(),
		Agent:     agent.String(),
		SessionID: sessionID.String(),
		Repo:      workspace.String(),
	})
	if err != nil {
		return nil, xerrors.Errorf("failed to record log: %w", err)
	}
	return event, nil
}

func (a *eventUsecaseAdapter) Audit(ctx context.Context, params AuditParams) (*model.Event, *model.CommandAudit, error) {
	event, audit, err := a.recordAudit.Run(ctx, RecordCommandAuditInput{
		DBPath:              a.dbPath,
		Command:             params.Command,
		Input:               params.Input,
		Output:              params.Output,
		Client:              params.Client.String(),
		Agent:               params.Agent.String(),
		SessionID:           params.SessionID.String(),
		Repo:                params.Workspace.String(),
		ExitCode:            params.ExitCode,
		AllowSecrets:        params.AllowSecrets,
		MaxInputBytes:       params.MaxInputBytes,
		MaxOutputBytes:      params.MaxOutputBytes,
		ExtraRedactPatterns: params.ExtraRedactPatterns,
	})
	if err != nil {
		return nil, nil, xerrors.Errorf("failed to record audit: %w", err)
	}
	return event, audit, nil
}

func (a *eventUsecaseAdapter) Search(ctx context.Context, criteria EventSearchCriteria) ([]*model.Event, error) {
	events, err := a.searchEvents.Run(ctx, a.dbPath, port.SearchEventsInput{
		Query:        criteria.Query,
		Repo:         criteria.Workspace.String(),
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
	events, err := a.listRecent.Run(ctx, a.dbPath, port.ListRecentEventsInput{
		Limit:        criteria.Limit,
		Offset:       criteria.Offset,
		Kind:         criteria.Kind.String(),
		Client:       criteria.Client.String(),
		Agent:        criteria.Agent.String(),
		SessionID:    criteria.SessionID.String(),
		Repo:         criteria.Workspace.String(),
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
	portDetails, err := a.getEventDetails.Run(ctx, a.dbPath, eventID.String())
	if err != nil {
		return nil, xerrors.Errorf("failed to get event details: %w", err)
	}
	return NewEventDetails(portDetails.Event(), portDetails.CommandAudit())
}

func (a *eventUsecaseAdapter) Context(ctx context.Context, criteria EventContextCriteria) ([]*model.Event, error) {
	events, err := a.getContext.Run(ctx, a.dbPath, port.GetContextInput{
		Repo:      criteria.Workspace.String(),
		SessionID: criteria.SessionID.String(),
		Limit:     criteria.Limit,
	})
	if err != nil {
		return nil, xerrors.Errorf("failed to get context events: %w", err)
	}
	return events, nil
}
