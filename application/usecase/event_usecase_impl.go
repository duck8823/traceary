package usecase

import (
	"context"
	"regexp"
	"strings"

	"golang.org/x/xerrors"

	"github.com/duck8823/traceary/application/queryservice"
	apptypes "github.com/duck8823/traceary/application/types"
	"github.com/duck8823/traceary/domain/model"
	"github.com/duck8823/traceary/domain/types"
)

const (
	maxAuditInputLength  = 64 * 1024
	maxAuditOutputLength = 64 * 1024
)

type eventUsecase struct {
	eventRepo  model.EventRepository
	eventQuery queryservice.EventQueryService
}

// NewEventUsecase creates an EventUsecase.
func NewEventUsecase(
	eventRepo model.EventRepository,
	eventQuery queryservice.EventQueryService,
) EventUsecase {
	return &eventUsecase{
		eventRepo:  eventRepo,
		eventQuery: eventQuery,
	}
}

func (u *eventUsecase) Log(ctx context.Context, message string, kind types.EventKind, client types.Client, agent types.Agent, sessionID types.SessionID, workspace types.Workspace) (*model.Event, error) {
	if u.eventRepo == nil {
		return nil, xerrors.Errorf("event repository is not configured")
	}

	if _, err := types.AgentOf(agent.String()); err != nil {
		return nil, xerrors.Errorf("failed to resolve agent: %w", err)
	}
	if _, err := types.SessionIDOf(sessionID.String()); err != nil {
		return nil, xerrors.Errorf("failed to resolve session ID: %w", err)
	}
	resolvedKind := types.EventKindNote
	if strings.TrimSpace(kind.String()) != "" {
		resolved, err := types.EventKindOf(kind.String())
		if err != nil {
			return nil, xerrors.Errorf("failed to resolve event kind: %w", err)
		}
		resolvedKind = resolved
	}

	eventID, err := newEventID()
	if err != nil {
		return nil, xerrors.Errorf("failed to generate event ID: %w", err)
	}

	event, err := model.NewEvent(
		eventID,
		resolvedKind,
		client,
		agent,
		sessionID,
		workspace,
		message,
	)
	if err != nil {
		return nil, xerrors.Errorf("failed to build log event: %w", err)
	}
	if err := u.eventRepo.Save(ctx, event); err != nil {
		return nil, xerrors.Errorf("failed to save log event: %w", err)
	}

	return event, nil
}

func (u *eventUsecase) Audit(ctx context.Context, command string, input string, output string, client types.Client, agent types.Agent, sessionID types.SessionID, workspace types.Workspace, exitCode types.Optional[int], redaction apptypes.AuditRedaction) (*model.Event, *model.CommandAudit, error) {
	if u.eventRepo == nil {
		return nil, nil, xerrors.Errorf("event repository is not configured")
	}

	if _, err := types.AgentOf(agent.String()); err != nil {
		return nil, nil, xerrors.Errorf("failed to resolve agent: %w", err)
	}
	if _, err := types.SessionIDOf(sessionID.String()); err != nil {
		return nil, nil, xerrors.Errorf("failed to resolve session ID: %w", err)
	}
	eventID, err := newEventID()
	if err != nil {
		return nil, nil, xerrors.Errorf("failed to generate event ID: %w", err)
	}

	maxInputBytes, err := resolveAuditPayloadLimit(redaction.MaxInputBytes(), maxAuditInputLength)
	if err != nil {
		return nil, nil, xerrors.Errorf("failed to resolve input limit: %w", err)
	}
	maxOutputBytes, err := resolveAuditPayloadLimit(redaction.MaxOutputBytes(), maxAuditOutputLength)
	if err != nil {
		return nil, nil, xerrors.Errorf("failed to resolve output limit: %w", err)
	}

	extraRedactors, err := compileExtraRedactPatterns(redaction.ExtraRedactPatterns())
	if err != nil {
		return nil, nil, xerrors.Errorf("failed to compile extra redaction patterns: %w", err)
	}

	normalizedInput := input
	normalizedOutput := output
	var inputRedacted bool
	var outputRedacted bool
	if !redaction.AllowSecrets() {
		normalizedInput, inputRedacted = redactAuditPayload(normalizedInput, extraRedactors)
		normalizedOutput, outputRedacted = redactAuditPayload(normalizedOutput, extraRedactors)
	}

	normalizedInput, inputTruncated := truncateAuditPayload(normalizedInput, maxInputBytes)
	normalizedOutput, outputTruncated := truncateAuditPayload(normalizedOutput, maxOutputBytes)
	commandAudit, err := model.NewCommandAudit(
		eventID,
		command,
		normalizedInput,
		normalizedOutput,
		inputTruncated,
		outputTruncated,
	)
	if err != nil {
		return nil, nil, xerrors.Errorf("failed to build command audit: %w", err)
	}
	commandAudit.SetRedaction(inputRedacted, outputRedacted)
	commandAudit.SetExitCode(exitCode)

	event, err := model.NewEvent(
		eventID,
		types.EventKindCommandExecuted,
		client,
		agent,
		sessionID,
		workspace,
		commandAudit.Command(),
	)
	if err != nil {
		return nil, nil, xerrors.Errorf("failed to build audit event: %w", err)
	}

	if err := u.eventRepo.SaveWithAudit(ctx, event, commandAudit); err != nil {
		return nil, nil, xerrors.Errorf("failed to save audit event: %w", err)
	}

	return event, commandAudit, nil
}

func (u *eventUsecase) Search(ctx context.Context, criteria apptypes.EventSearchCriteria) ([]*model.Event, error) {
	if !hasSearchConstraint(criteria) {
		return nil, xerrors.Errorf("at least one search filter is required")
	}
	if criteria.Limit() <= 0 {
		return nil, xerrors.Errorf("limit must be greater than or equal to 1")
	}
	if criteria.Offset() < 0 {
		return nil, xerrors.Errorf("offset must be greater than or equal to 0")
	}
	if !criteria.From().IsZero() && !criteria.To().IsZero() && criteria.From().After(criteria.To()) {
		return nil, xerrors.Errorf("from must be earlier than to")
	}
	resolvedKind, err := resolveOptionalSearchKind(criteria.Kind().String())
	if err != nil {
		return nil, err
	}

	events, err := u.eventQuery.Search(ctx, criteria.Query(), criteria.Workspace(), criteria.SessionID(), criteria.Client(), criteria.Agent(), resolvedKind, criteria.From(), criteria.To(), criteria.Limit(), criteria.Offset(), criteria.FailuresOnly())
	if err != nil {
		return nil, xerrors.Errorf("failed to search events: %w", err)
	}
	return events, nil
}

func (u *eventUsecase) List(ctx context.Context, criteria apptypes.EventListCriteria) ([]*model.Event, error) {
	if criteria.Limit() <= 0 {
		return nil, xerrors.Errorf("limit must be greater than or equal to 1")
	}
	if criteria.Offset() < 0 {
		return nil, xerrors.Errorf("offset must be greater than or equal to 0")
	}

	events, err := u.eventQuery.ListRecent(ctx, criteria.Limit(), criteria.Offset(), criteria.Kind(), criteria.Client(), criteria.Agent(), criteria.SessionID(), criteria.Workspace(), criteria.FailuresOnly(), criteria.From(), criteria.To())
	if err != nil {
		return nil, xerrors.Errorf("failed to list events: %w", err)
	}
	return events, nil
}

func (u *eventUsecase) ListWindow(ctx context.Context, criteria apptypes.EventListCriteria) ([]*model.Event, error) {
	if criteria.Limit() <= 0 {
		return nil, xerrors.Errorf("limit must be greater than or equal to 1")
	}
	if criteria.Offset() != 0 {
		return nil, xerrors.Errorf("offset must be zero for ListWindow (paging is handled internally)")
	}
	if !criteria.From().IsZero() && !criteria.To().IsZero() && criteria.From().After(criteria.To()) {
		return nil, xerrors.Errorf("from must be earlier than to")
	}

	events, err := u.eventQuery.ListWindow(ctx, criteria)
	if err != nil {
		return nil, xerrors.Errorf("failed to list event window: %w", err)
	}
	return events, nil
}

func (u *eventUsecase) Show(ctx context.Context, eventID types.EventID) (apptypes.EventDetails, error) {
	details, err := u.eventQuery.GetDetails(ctx, eventID)
	if err != nil {
		return apptypes.EventDetails{}, xerrors.Errorf("failed to get event details: %w", err)
	}
	return details, nil
}

func (u *eventUsecase) Context(ctx context.Context, criteria apptypes.EventContextCriteria) ([]*model.Event, error) {
	if criteria.Limit() <= 0 {
		return nil, xerrors.Errorf("limit must be greater than or equal to 1")
	}

	events, err := u.eventQuery.GetContext(ctx, criteria.Workspace(), criteria.SessionID(), criteria.Limit())
	if err != nil {
		return nil, xerrors.Errorf("failed to get context events: %w", err)
	}
	return events, nil
}

func (u *eventUsecase) Timeline(ctx context.Context, criteria apptypes.TimelineCriteria) ([]apptypes.TimelineBlock, error) {
	if criteria.GapSeconds() <= 0 {
		return nil, xerrors.Errorf("gap must be greater than 0")
	}
	if criteria.Limit() <= 0 {
		return nil, xerrors.Errorf("limit must be greater than or equal to 1")
	}

	blocks, err := u.eventQuery.ListTimelineBlocks(ctx, criteria.Workspace(), criteria.From(), criteria.To(), criteria.GapSeconds(), criteria.Limit())
	if err != nil {
		return nil, xerrors.Errorf("failed to list timeline blocks: %w", err)
	}
	return blocks, nil
}

func hasSearchConstraint(criteria apptypes.EventSearchCriteria) bool {
	return strings.TrimSpace(criteria.Query()) != "" ||
		strings.TrimSpace(criteria.Workspace().String()) != "" ||
		strings.TrimSpace(criteria.SessionID().String()) != "" ||
		strings.TrimSpace(criteria.Client().String()) != "" ||
		strings.TrimSpace(criteria.Agent().String()) != "" ||
		strings.TrimSpace(criteria.Kind().String()) != "" ||
		!criteria.From().IsZero() ||
		!criteria.To().IsZero() ||
		criteria.FailuresOnly()
}

func resolveOptionalSearchKind(value string) (types.EventKind, error) {
	trimmedValue := strings.TrimSpace(value)
	if trimmedValue == "" {
		return types.EventKind(""), nil
	}
	if trimmedValue == "audit" {
		return types.EventKindCommandExecuted, nil
	}

	kind, err := types.EventKindOf(trimmedValue)
	if err != nil {
		return types.EventKind(""), xerrors.Errorf("failed to resolve kind: %w", err)
	}

	return kind, nil
}

func compileExtraRedactPatterns(patterns []string) ([]auditPayloadRedactor, error) {
	if len(patterns) == 0 {
		return nil, nil
	}

	redactors := make([]auditPayloadRedactor, 0, len(patterns))
	for _, raw := range patterns {
		trimmed := strings.TrimSpace(raw)
		if trimmed == "" {
			continue
		}
		compiled, err := regexp.Compile(trimmed)
		if err != nil {
			return nil, xerrors.Errorf("invalid redaction pattern %q: %w", trimmed, err)
		}
		redactors = append(redactors, auditPayloadRedactor{
			pattern:     compiled,
			replacement: redactedAuditValue,
		})
	}

	return redactors, nil
}

func truncateAuditPayload(value string, limit int) (string, bool) {
	if limit <= 0 {
		return value, false
	}
	if len(value) <= limit {
		return value, false
	}

	const suffix = "\n...[truncated]"
	if limit <= len(suffix) {
		return suffix[:limit], true
	}

	return value[:limit-len(suffix)] + suffix, true
}

func resolveAuditPayloadLimit(value int, defaultValue int) (int, error) {
	if value < 0 {
		return 0, xerrors.Errorf("value must be greater than or equal to 0")
	}
	if value == 0 {
		return defaultValue, nil
	}

	return value, nil
}
