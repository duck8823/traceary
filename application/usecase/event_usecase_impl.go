package usecase

import (
	"context"
	"strconv"
	"strings"

	"golang.org/x/xerrors"

	"github.com/duck8823/traceary/application/queryservice"
	"github.com/duck8823/traceary/application/redaction"
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

func (u *eventUsecase) Log(ctx context.Context, message string, kind types.EventKind, client types.Client, agent types.Agent, sessionID types.SessionID, workspace types.Workspace, logCfg apptypes.LogRedaction) (*model.Event, error) {
	if u.eventRepo == nil {
		return nil, xerrors.Errorf("event repository is not configured")
	}

	if _, err := types.AgentFrom(agent.String()); err != nil {
		return nil, xerrors.Errorf("failed to resolve agent: %w", err)
	}
	if _, err := types.SessionIDFrom(sessionID.String()); err != nil {
		return nil, xerrors.Errorf("failed to resolve session ID: %w", err)
	}
	resolvedKind := types.EventKindNote
	if strings.TrimSpace(kind.String()) != "" {
		resolved, err := types.EventKindFrom(kind.String())
		if err != nil {
			return nil, xerrors.Errorf("failed to resolve event kind: %w", err)
		}
		resolvedKind = resolved
	}

	// Transcript events routinely re-state secrets the agent saw
	// earlier in the turn (API keys read from .env, Bearer tokens
	// echoed from header dumps, private keys pasted into chat). Apply
	// the shared redaction policy once inside the usecase so every
	// log-ingest surface (CLI log, transcript hook, MCP add_log)
	// gets the same coverage without re-implementing the 5-line
	// CompileExtraPatterns+Apply block in the presentation layer.
	//
	// If the body is a structured JSON block envelope (introduced in
	// #662), parse it, redact each block's text field independently,
	// and re-serialize — running redaction on the raw JSON string
	// would risk inserting "[REDACTED]" inside JSON delimiters and
	// breaking the envelope for downstream block-aware readers.
	if resolvedKind == types.EventKindTranscript {
		rules, err := redaction.CompileRules(logCfg.ExtraRedactPatterns(), logCfg.StructuredRules())
		if err != nil {
			return nil, xerrors.Errorf("failed to compile redaction rules for transcript: %w", err)
		}
		if redactedBody, ok := redactStructuredBodyBlocks(message, rules); ok {
			message = redactedBody
		} else {
			message, _ = redaction.ApplyWithRules(message, rules, "log.message")
		}
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
	event.SetSourceHook(apptypes.SourceHookFromContext(ctx))
	if err := u.eventRepo.Save(ctx, event); err != nil {
		return nil, xerrors.Errorf("failed to save log event: %w", err)
	}

	return event, nil
}

func (u *eventUsecase) Audit(ctx context.Context, command string, input string, output string, client types.Client, agent types.Agent, sessionID types.SessionID, workspace types.Workspace, exitCode types.Optional[int], auditCfg apptypes.AuditRedaction) (*model.Event, *model.CommandAudit, error) {
	if u.eventRepo == nil {
		return nil, nil, xerrors.Errorf("event repository is not configured")
	}

	if _, err := types.AgentFrom(agent.String()); err != nil {
		return nil, nil, xerrors.Errorf("failed to resolve agent: %w", err)
	}
	if _, err := types.SessionIDFrom(sessionID.String()); err != nil {
		return nil, nil, xerrors.Errorf("failed to resolve session ID: %w", err)
	}
	eventID, err := newEventID()
	if err != nil {
		return nil, nil, xerrors.Errorf("failed to generate event ID: %w", err)
	}

	maxInputBytes, err := resolveAuditPayloadLimit(auditCfg.MaxInputBytes(), maxAuditInputLength)
	if err != nil {
		return nil, nil, xerrors.Errorf("failed to resolve input limit: %w", err)
	}
	maxOutputBytes, err := resolveAuditPayloadLimit(auditCfg.MaxOutputBytes(), maxAuditOutputLength)
	if err != nil {
		return nil, nil, xerrors.Errorf("failed to resolve output limit: %w", err)
	}

	rules, err := redaction.CompileRules(auditCfg.ExtraRedactPatterns(), auditCfg.StructuredRules())
	if err != nil {
		return nil, nil, xerrors.Errorf("failed to compile redaction rules: %w", err)
	}

	normalizedInput := input
	normalizedOutput := output
	var inputRedacted bool
	var outputRedacted bool
	if !auditCfg.AllowSecrets() {
		normalizedInput, inputRedacted = redaction.ApplyWithRules(normalizedInput, rules, "audit.input")
		normalizedOutput, outputRedacted = redaction.ApplyWithRules(normalizedOutput, rules, "audit.output")
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
		commandAuditEventBody(commandAudit),
	)
	if err != nil {
		return nil, nil, xerrors.Errorf("failed to build audit event: %w", err)
	}
	event.SetSourceHook(apptypes.SourceHookFromContext(ctx))

	if err := u.eventRepo.SaveWithAudit(ctx, event, commandAudit); err != nil {
		return nil, nil, xerrors.Errorf("failed to save audit event: %w", err)
	}

	return event, commandAudit, nil
}

func commandAuditEventBody(commandAudit *model.CommandAudit) string {
	if commandAudit == nil {
		return ""
	}

	var builder strings.Builder
	builder.WriteString(commandAudit.Command())

	exitCode, hasExitCode := commandAudit.ExitCode().Value()
	if hasExitCode || commandAudit.Input() != "" || commandAudit.Output() != "" || commandAudit.InputTruncated() || commandAudit.OutputTruncated() {
		builder.WriteString("\n")
	}
	if hasExitCode {
		builder.WriteString("\nEXIT_CODE: ")
		builder.WriteString(strconv.Itoa(exitCode))
		builder.WriteString("\n")
	}
	if commandAudit.Input() != "" || commandAudit.InputTruncated() {
		builder.WriteString("\nINPUT")
		if commandAudit.InputTruncated() {
			builder.WriteString(" (truncated)")
		}
		builder.WriteString(":\n")
		builder.WriteString(commandAudit.Input())
		builder.WriteString("\n")
	}
	if commandAudit.Output() != "" || commandAudit.OutputTruncated() {
		builder.WriteString("\nOUTPUT")
		if commandAudit.OutputTruncated() {
			builder.WriteString(" (truncated)")
		}
		builder.WriteString(":\n")
		builder.WriteString(commandAudit.Output())
		builder.WriteString("\n")
	}

	return strings.TrimRight(builder.String(), "\n")
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

	events, err := u.eventQuery.ListRecent(ctx, criteria.Limit(), criteria.Offset(), criteria.Kind(), criteria.Client(), criteria.Agent(), criteria.SessionID(), criteria.Workspace(), criteria.FailuresOnly(), criteria.From(), criteria.To(), criteria.SourceHook())
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

	kind, err := types.EventKindFrom(trimmedValue)
	if err != nil {
		return types.EventKind(""), xerrors.Errorf("failed to resolve kind: %w", err)
	}

	return kind, nil
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

// redactStructuredBodyBlocks parses body as the canonical JSON block
// envelope (see application/types.EventBodyBlocks), runs the built-in
// + extra redactors on each block's text field, and returns the
// re-serialized JSON. Returns ok=false when the body isn't JSON-
// shaped so the caller can fall back to the legacy whole-string
// redaction path.
func redactStructuredBodyBlocks(body string, rules redaction.Rules) (string, bool) {
	blocks := apptypes.ParseEventBodyBlocks(body)
	if len(blocks) == 0 {
		return "", false
	}
	// ParseEventBodyBlocks returns a single-text fallback for
	// non-JSON bodies. Detect that case by checking whether the
	// block round-trips as the exact same raw string.
	if len(blocks) == 1 && blocks[0].Type == apptypes.EventBodyBlockTypeText && blocks[0].Text == body {
		return "", false
	}
	for i := range blocks {
		blocks[i].Text, _ = redaction.ApplyWithRules(blocks[i].Text, rules, "log.message")
	}
	encoded, err := apptypes.MarshalEventBodyBlocks(blocks)
	if err != nil {
		return "", false
	}
	return encoded, true
}
