package usecase

import (
	"context"

	"golang.org/x/xerrors"

	apptypes "github.com/duck8823/traceary/application/types"
	"github.com/duck8823/traceary/application/queryservice"
	"github.com/duck8823/traceary/domain/model"
	"github.com/duck8823/traceary/domain/types"
)

type eventUsecaseAdapter struct {
	recordLog   RecordLogUsecase
	recordAudit RecordCommandAuditUsecase
	eventQuery  queryservice.EventQueryService
}

// NewEventUsecaseAdapter creates an EventUsecase that delegates to existing usecases and queryservices.
func NewEventUsecaseAdapter(
	recordLog RecordLogUsecase,
	recordAudit RecordCommandAuditUsecase,
	eventQuery queryservice.EventQueryService,
) EventUsecase {
	return &eventUsecaseAdapter{
		recordLog:   recordLog,
		recordAudit: recordAudit,
		eventQuery:  eventQuery,
	}
}

func (a *eventUsecaseAdapter) Log(ctx context.Context, message string, kind types.EventKind, client types.Client, agent types.Agent, sessionID types.SessionID, workspace types.Workspace) (*model.Event, error) {
	event, err := a.recordLog.Run(ctx, RecordLogInput{
		Message:   message,
		Kind:      kind.String(),
		Client:    client.String(),
		Agent:     agent.String(),
		SessionID: sessionID.String(),
		Workspace: workspace.String(),
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
		Workspace:           workspace.String(),
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
	events, err := a.eventQuery.Search(ctx, criteria.Query, criteria.Workspace, criteria.SessionID, criteria.Client, criteria.Agent, criteria.Kind, criteria.From, criteria.To, criteria.Limit, criteria.Offset, criteria.FailuresOnly)
	if err != nil {
		return nil, xerrors.Errorf("failed to search events: %w", err)
	}
	return events, nil
}

func (a *eventUsecaseAdapter) List(ctx context.Context, criteria EventListCriteria) ([]*model.Event, error) {
	events, err := a.eventQuery.ListRecent(ctx, criteria.Limit, criteria.Offset, criteria.Kind, criteria.Client, criteria.Agent, criteria.SessionID, criteria.Workspace, criteria.FailuresOnly, criteria.From, criteria.To)
	if err != nil {
		return nil, xerrors.Errorf("failed to list events: %w", err)
	}
	return events, nil
}

func (a *eventUsecaseAdapter) Show(ctx context.Context, eventID types.EventID) (apptypes.EventDetails, error) {
	details, err := a.eventQuery.GetDetails(ctx, eventID)
	if err != nil {
		return apptypes.EventDetails{}, xerrors.Errorf("failed to get event details: %w", err)
	}
	return details, nil
}

func (a *eventUsecaseAdapter) Context(ctx context.Context, criteria EventContextCriteria) ([]*model.Event, error) {
	events, err := a.eventQuery.GetContext(ctx, criteria.Workspace, criteria.SessionID, criteria.Limit)
	if err != nil {
		return nil, xerrors.Errorf("failed to get context events: %w", err)
	}
	return events, nil
}

func (a *eventUsecaseAdapter) Timeline(ctx context.Context, criteria TimelineCriteria) ([]apptypes.TimelineBlock, error) {
	blocks, err := a.eventQuery.ListTimelineBlocks(ctx, criteria.Workspace, criteria.From, criteria.To, criteria.GapSeconds, criteria.Limit)
	if err != nil {
		return nil, xerrors.Errorf("failed to list timeline blocks: %w", err)
	}
	return blocks, nil
}
