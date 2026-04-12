package usecase

import (
	"context"
	"regexp"
	"strings"

	"golang.org/x/xerrors"

	"github.com/duck8823/traceary/domain/model"
	"github.com/duck8823/traceary/domain/types"
)

const (
	maxAuditInputLength  = 64 * 1024
	maxAuditOutputLength = 64 * 1024
)

// RecordCommandAuditInput is the input for traceary audit recording.
type RecordCommandAuditInput struct {
	Command             string
	Input               string
	Output              string
	Client              string
	Agent               string
	SessionID           string
	Workspace string
	AllowSecrets        bool
	MaxInputBytes       int
	MaxOutputBytes      int
	ExtraRedactPatterns []string
	ExitCode            types.Optional[int]
}

// RecordCommandAuditUsecase persists command-audit events.
type RecordCommandAuditUsecase interface {
	// Run persists a command-audit event.
	Run(ctx context.Context, input RecordCommandAuditInput) (*model.Event, *model.CommandAudit, error)
}

type recordCommandAuditUsecase struct {
	eventRepo model.EventRepository
}

// NewRecordCommandAuditUsecase creates a RecordCommandAuditUsecase.
func NewRecordCommandAuditUsecase(eventRepo model.EventRepository) RecordCommandAuditUsecase {
	return &recordCommandAuditUsecase{eventRepo: eventRepo}
}

// Run persists a command-audit event.
func (u *recordCommandAuditUsecase) Run(
	ctx context.Context,
	input RecordCommandAuditInput,
) (*model.Event, *model.CommandAudit, error) {
	if u.eventRepo == nil {
		return nil, nil, xerrors.Errorf("event repository is not configured")
	}


	agent, err := types.AgentOf(input.Agent)
	if err != nil {
		return nil, nil, xerrors.Errorf("failed to resolve agent: %w", err)
	}
	sessionID, err := types.SessionIDOf(input.SessionID)
	if err != nil {
		return nil, nil, xerrors.Errorf("failed to resolve session ID: %w", err)
	}
	eventID, err := newEventID()
	if err != nil {
		return nil, nil, xerrors.Errorf("failed to generate event ID: %w", err)
	}

	maxInputBytes, err := resolveAuditPayloadLimit(input.MaxInputBytes, maxAuditInputLength)
	if err != nil {
		return nil, nil, xerrors.Errorf("failed to resolve input limit: %w", err)
	}
	maxOutputBytes, err := resolveAuditPayloadLimit(input.MaxOutputBytes, maxAuditOutputLength)
	if err != nil {
		return nil, nil, xerrors.Errorf("failed to resolve output limit: %w", err)
	}

	extraRedactors, err := compileExtraRedactPatterns(input.ExtraRedactPatterns)
	if err != nil {
		return nil, nil, xerrors.Errorf("failed to compile extra redaction patterns: %w", err)
	}

	normalizedInput := input.Input
	normalizedOutput := input.Output
	var inputRedacted bool
	var outputRedacted bool
	if !input.AllowSecrets {
		normalizedInput, inputRedacted = redactAuditPayload(normalizedInput, extraRedactors)
		normalizedOutput, outputRedacted = redactAuditPayload(normalizedOutput, extraRedactors)
	}

	normalizedInput, inputTruncated := truncateAuditPayload(normalizedInput, maxInputBytes)
	normalizedOutput, outputTruncated := truncateAuditPayload(normalizedOutput, maxOutputBytes)
	commandAudit, err := model.NewCommandAudit(
		eventID,
		input.Command,
		normalizedInput,
		normalizedOutput,
		inputTruncated,
		outputTruncated,
	)
	if err != nil {
		return nil, nil, xerrors.Errorf("failed to build command audit: %w", err)
	}
	commandAudit.SetRedaction(inputRedacted, outputRedacted)
	commandAudit.SetExitCode(input.ExitCode)

	event, err := model.NewEvent(
		eventID,
		types.EventKindCommandExecuted,
		strings.TrimSpace(input.Client),
		agent,
		sessionID,
		strings.TrimSpace(input.Workspace),
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
