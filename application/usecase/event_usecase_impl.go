package usecase

import (
	"context"
	"strings"
	"unicode/utf8"

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
	eventRepo    model.EventRepository
	eventQuery   queryservice.EventReadQueryService
	auditPayload queryservice.CommandAuditPayloadQueryService
}

// NewEventUsecase creates an EventUsecase.
func NewEventUsecase(
	eventRepo model.EventRepository,
	eventQuery queryservice.EventReadQueryService,
) EventUsecase {
	var auditPayload queryservice.CommandAuditPayloadQueryService
	if payload, ok := any(eventQuery).(queryservice.CommandAuditPayloadQueryService); ok {
		auditPayload = payload
	}
	// The write repository is often the same SQLite adapter and may carry the
	// payload hydration capability when the read query does not.
	if auditPayload == nil {
		if payload, ok := any(eventRepo).(queryservice.CommandAuditPayloadQueryService); ok {
			auditPayload = payload
		}
	}
	return &eventUsecase{
		eventRepo:    eventRepo,
		eventQuery:   eventQuery,
		auditPayload: auditPayload,
	}
}

func (u *eventUsecase) Log(ctx context.Context, message string, kind types.EventKind, client types.Client, agent types.Agent, sessionID types.SessionID, workspace types.Workspace, logCfg apptypes.LogRedaction) (apptypes.EventWriteResult, error) {
	if u.eventRepo == nil {
		return apptypes.EventWriteResult{}, xerrors.Errorf("event repository is not configured")
	}

	if _, err := types.AgentFrom(agent.String()); err != nil {
		return apptypes.EventWriteResult{}, xerrors.Errorf("failed to resolve agent: %w", err)
	}
	if _, err := types.SessionIDFrom(sessionID.String()); err != nil {
		return apptypes.EventWriteResult{}, xerrors.Errorf("failed to resolve session ID: %w", err)
	}
	resolvedKind := types.EventKindNote
	if strings.TrimSpace(kind.String()) != "" {
		resolved, err := types.EventKindFrom(kind.String())
		if err != nil {
			return apptypes.EventWriteResult{}, xerrors.Errorf("failed to resolve event kind: %w", err)
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
			return apptypes.EventWriteResult{}, xerrors.Errorf("failed to compile redaction rules for transcript: %w", err)
		}
		if redactedBody, ok := redactStructuredBodyBlocks(message, rules); ok {
			message = redactedBody
		} else {
			message, _ = redaction.ApplyWithRules(message, rules, "log.message")
		}
	}

	eventID, err := newEventID()
	if err != nil {
		return apptypes.EventWriteResult{}, xerrors.Errorf("failed to generate event ID: %w", err)
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
		return apptypes.EventWriteResult{}, xerrors.Errorf("failed to build log event: %w", err)
	}
	event.SetSourceHook(apptypes.SourceHookFromContext(ctx))
	if err := attachHookDelivery(ctx, event); err != nil {
		return apptypes.EventWriteResult{}, xerrors.Errorf("failed to attach log delivery evidence: %w", err)
	}
	if err := u.eventRepo.Save(ctx, event); err != nil {
		return apptypes.EventWriteResult{}, xerrors.Errorf("failed to save log event: %w", err)
	}

	return apptypes.EventWriteResultOf(event, event.PersistInserted()), nil
}

func (u *eventUsecase) DeleteTranscript(ctx context.Context, eventID types.EventID) error {
	if u.eventRepo == nil {
		return xerrors.Errorf("event repository is not configured")
	}
	parsed, err := types.EventIDFrom(eventID.String())
	if err != nil {
		return xerrors.Errorf("failed to resolve event ID: %w", err)
	}
	if err := u.eventRepo.DeleteTranscript(ctx, parsed); err != nil {
		return xerrors.Errorf("failed to delete transcript event: %w", err)
	}
	return nil
}

func (u *eventUsecase) Audit(ctx context.Context, in apptypes.AuditInput, auditCfg apptypes.AuditRedaction) (apptypes.EventWriteResult, *model.CommandAudit, error) {
	if u.eventRepo == nil {
		return apptypes.EventWriteResult{}, nil, xerrors.Errorf("event repository is not configured")
	}

	if _, err := types.AgentFrom(in.Agent.String()); err != nil {
		return apptypes.EventWriteResult{}, nil, xerrors.Errorf("failed to resolve agent: %w", err)
	}
	if _, err := types.SessionIDFrom(in.SessionID.String()); err != nil {
		return apptypes.EventWriteResult{}, nil, xerrors.Errorf("failed to resolve session ID: %w", err)
	}
	eventID, err := newEventID()
	if err != nil {
		return apptypes.EventWriteResult{}, nil, xerrors.Errorf("failed to generate event ID: %w", err)
	}

	maxInputBytes, err := resolveAuditPayloadLimit(auditCfg.MaxInputBytes(), maxAuditInputLength)
	if err != nil {
		return apptypes.EventWriteResult{}, nil, xerrors.Errorf("failed to resolve input limit: %w", err)
	}
	maxOutputBytes, err := resolveAuditPayloadLimit(auditCfg.MaxOutputBytes(), maxAuditOutputLength)
	if err != nil {
		return apptypes.EventWriteResult{}, nil, xerrors.Errorf("failed to resolve output limit: %w", err)
	}

	rules, err := redaction.CompileRules(auditCfg.ExtraRedactPatterns(), auditCfg.StructuredRules())
	if err != nil {
		return apptypes.EventWriteResult{}, nil, xerrors.Errorf("failed to compile redaction rules: %w", err)
	}

	normalizedCommand := in.Command
	normalizedInput := in.Input
	normalizedOutput := in.Output
	var inputRedacted bool
	var outputRedacted bool
	if !auditCfg.AllowSecrets() {
		normalizedCommand, _ = redaction.ApplyWithRules(normalizedCommand, rules, "audit.command")
		normalizedInput, inputRedacted = redaction.ApplyWithRules(normalizedInput, rules, "audit.input")
		normalizedOutput, outputRedacted = redaction.ApplyWithRules(normalizedOutput, rules, "audit.output")
	}

	inputPayload := truncateAuditPayload(normalizedInput, maxInputBytes)
	outputPayload := truncateAuditPayload(normalizedOutput, maxOutputBytes)
	commandAudit, err := model.NewCommandAudit(
		eventID,
		normalizedCommand,
		inputPayload.Body,
		outputPayload.Body,
		inputPayload.Truncated,
		outputPayload.Truncated,
	)
	if err != nil {
		return apptypes.EventWriteResult{}, nil, xerrors.Errorf("failed to build command audit: %w", err)
	}
	commandAudit.SetOriginalPayloadBytes(inputPayload.OriginalBytes, outputPayload.OriginalBytes)
	commandAudit.SetRedaction(inputRedacted, outputRedacted)
	if err := commandAudit.ClassifyOutcome(in.ExitCode, in.FailureReason, in.Failed); err != nil {
		return apptypes.EventWriteResult{}, nil, xerrors.Errorf("failed to classify command audit outcome: %w", err)
	}

	// command_executed no longer persists a composed body; command_audits is
	// the retained execution record (#1675). Search indexes audit columns
	// independently of events.body.
	event, err := model.NewEvent(
		eventID,
		types.EventKindCommandExecuted,
		in.Client,
		in.Agent,
		in.SessionID,
		in.Workspace,
		"",
	)
	if err != nil {
		return apptypes.EventWriteResult{}, nil, xerrors.Errorf("failed to build audit event: %w", err)
	}
	event.SetSourceHook(apptypes.SourceHookFromContext(ctx))
	if err := attachHookDelivery(ctx, event, commandAuditDeliveryFields(commandAudit)...); err != nil {
		return apptypes.EventWriteResult{}, nil, xerrors.Errorf("failed to attach audit delivery evidence: %w", err)
	}

	if err := u.eventRepo.SaveWithAudit(ctx, event, commandAudit); err != nil {
		return apptypes.EventWriteResult{}, nil, xerrors.Errorf("failed to save audit event: %w", err)
	}
	if !event.PersistInserted() {
		commandAudit.RebindEventID(event.EventID())
	}

	return apptypes.EventWriteResultOf(event, event.PersistInserted()), commandAudit, nil
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
	if !criteria.PageAnchor().IsZero() && criteria.Offset() != 0 {
		return nil, xerrors.Errorf("event page anchor cannot be combined with offset")
	}
	if !criteria.From().IsZero() && !criteria.To().IsZero() && criteria.From().After(criteria.To()) {
		return nil, xerrors.Errorf("from must be earlier than to")
	}
	resolvedKind, err := resolveOptionalSearchKind(criteria.Kind().String())
	if err != nil {
		return nil, err
	}

	resolvedCriteria := apptypes.NewEventSearchCriteriaBuilder(criteria.Limit()).
		Query(criteria.Query()).
		Workspace(criteria.Workspace()).
		SessionID(criteria.SessionID()).
		Client(criteria.Client()).
		Agent(criteria.Agent()).
		Kind(resolvedKind).
		From(criteria.From()).
		To(criteria.To()).
		Offset(criteria.Offset()).
		FailuresOnly(criteria.FailuresOnly()).
		PageAnchor(criteria.PageAnchor()).
		Build()
	events, err := u.eventQuery.SearchPage(ctx, resolvedCriteria)
	if err != nil {
		return nil, xerrors.Errorf("failed to search event page: %w", err)
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
	if !criteria.PageAnchor().IsZero() && criteria.Offset() != 0 {
		return nil, xerrors.Errorf("event page anchor cannot be combined with offset")
	}

	events, err := u.eventQuery.ListRecentPage(ctx, criteria)
	if err != nil {
		return nil, xerrors.Errorf("failed to list event page: %w", err)
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
	if criteria.Offset() < 0 {
		return nil, xerrors.Errorf("offset must be greater than or equal to 0")
	}
	if !criteria.PageAnchor().IsZero() && criteria.Offset() != 0 {
		return nil, xerrors.Errorf("event page anchor cannot be combined with offset")
	}

	events, err := u.eventQuery.GetContextPage(ctx, criteria)
	if err != nil {
		return nil, xerrors.Errorf("failed to get context event page: %w", err)
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

func (u *eventUsecase) HydrateCommandAudits(
	ctx context.Context,
	events []*model.Event,
	fields queryservice.CommandAuditPayloadFields,
) error {
	if u.auditPayload == nil {
		return xerrors.Errorf("command audit payload query is not configured")
	}
	if err := u.auditPayload.HydrateCommandAudits(ctx, events, fields); err != nil {
		return xerrors.Errorf("failed to hydrate command audit payloads: %w", err)
	}
	return nil
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

type auditPayloadTruncation struct {
	Body          string
	Truncated     bool
	OriginalBytes int
}

func truncateAuditPayload(value string, limit int) auditPayloadTruncation {
	originalBytes := len(value)
	if limit <= 0 {
		return auditPayloadTruncation{Body: value, OriginalBytes: originalBytes}
	}
	if originalBytes <= limit {
		return auditPayloadTruncation{Body: value, OriginalBytes: originalBytes}
	}

	marker := "\n" + model.AuditPayloadTruncationMarker(originalBytes) + "\n"
	if limit <= len(marker) {
		return auditPayloadTruncation{
			Body:          takeUTF8PrefixBytes(marker, limit),
			Truncated:     true,
			OriginalBytes: originalBytes,
		}
	}

	payloadBudget := limit - len(marker)
	headBudget := payloadBudget / 2
	tailBudget := payloadBudget - headBudget
	head := takeUTF8PrefixBytes(value, headBudget)
	tail := takeUTF8SuffixBytes(value, tailBudget)

	return auditPayloadTruncation{
		Body:          head + marker + tail,
		Truncated:     true,
		OriginalBytes: originalBytes,
	}
}

func takeUTF8PrefixBytes(value string, limit int) string {
	if limit <= 0 {
		return ""
	}
	if len(value) <= limit {
		return value
	}
	end := 0
	for end < len(value) {
		r, size := utf8.DecodeRuneInString(value[end:])
		if r == utf8.RuneError && size == 0 {
			break
		}
		if end+size > limit {
			break
		}
		end += size
	}
	return value[:end]
}

func takeUTF8SuffixBytes(value string, limit int) string {
	if limit <= 0 {
		return ""
	}
	if len(value) <= limit {
		return value
	}
	start := len(value)
	for start > 0 {
		r, size := utf8.DecodeLastRuneInString(value[:start])
		if r == utf8.RuneError && size == 0 {
			break
		}
		if len(value)-start+size > limit {
			break
		}
		start -= size
	}
	return value[start:]
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
