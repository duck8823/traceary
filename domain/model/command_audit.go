package model

import (
	"strconv"
	"strings"

	"golang.org/x/xerrors"

	"github.com/duck8823/traceary/domain/types"
)

const (
	auditTruncationMarkerPrefix = "...[truncated original_bytes="
	auditTruncationMarkerSuffix = "]..."
)

// CommandAudit holds detailed audit information for a command execution.
type CommandAudit struct {
	eventID             types.EventID
	command             string
	commandIdentity     types.CommandIdentity
	input               string
	output              string
	inputTruncated      bool
	outputTruncated     bool
	inputOriginalBytes  int
	outputOriginalBytes int
	inputRedacted       bool
	outputRedacted      bool
	exitCode            types.Optional[int]
	failed              bool
	failureReason       types.CommandFailureReason
}

// NewCommandAudit creates a new CommandAudit.
func NewCommandAudit(
	eventID types.EventID,
	command string,
	input string,
	output string,
	inputTruncated bool,
	outputTruncated bool,
) (*CommandAudit, error) {
	trimmedCommand := strings.TrimSpace(command)
	if trimmedCommand == "" {
		return nil, xerrors.Errorf("command must not be empty")
	}

	return &CommandAudit{
		eventID:             eventID,
		command:             trimmedCommand,
		commandIdentity:     types.CommandIdentityFrom(trimmedCommand),
		input:               input,
		output:              output,
		inputTruncated:      inputTruncated,
		outputTruncated:     outputTruncated,
		inputOriginalBytes:  inferAuditPayloadOriginalBytes(input, inputTruncated),
		outputOriginalBytes: inferAuditPayloadOriginalBytes(output, outputTruncated),
		failureReason:       types.CommandFailureReasonUnknown,
	}, nil
}

// CommandAuditOf restores a CommandAudit from persisted values.
func CommandAuditOf(
	eventID types.EventID,
	command string,
	input string,
	output string,
	inputTruncated bool,
	outputTruncated bool,
	exitCode types.Optional[int],
	failed bool,
) *CommandAudit {
	return &CommandAudit{
		eventID:             eventID,
		command:             command,
		commandIdentity:     types.CommandIdentityOf(types.None[types.CommandName](), types.CommandNameUnknown),
		input:               input,
		output:              output,
		inputTruncated:      inputTruncated,
		outputTruncated:     outputTruncated,
		inputOriginalBytes:  inferAuditPayloadOriginalBytes(input, inputTruncated),
		outputOriginalBytes: inferAuditPayloadOriginalBytes(output, outputTruncated),
		exitCode:            exitCode,
		failed:              failed,
		failureReason:       types.CommandFailureReasonUnknown,
	}
}

// EventID returns the linked event ID.
func (a *CommandAudit) EventID() types.EventID { return a.eventID }

// Command returns the executed command.
func (a *CommandAudit) Command() string { return a.command }

// CommandIdentity returns the separately normalized wrapper and executable.
func (a *CommandAudit) CommandIdentity() types.CommandIdentity { return a.commandIdentity }

// Input returns the command input payload.
func (a *CommandAudit) Input() string { return a.input }

// Output returns the command output payload.
func (a *CommandAudit) Output() string { return a.output }

// InputTruncated reports whether input was truncated.
func (a *CommandAudit) InputTruncated() bool { return a.inputTruncated }

// OutputTruncated reports whether output was truncated.
func (a *CommandAudit) OutputTruncated() bool { return a.outputTruncated }

// SetOriginalPayloadBytes records the pre-truncation payload sizes when known.
func (a *CommandAudit) SetOriginalPayloadBytes(inputBytes int, outputBytes int) {
	if a == nil {
		return
	}
	if a.inputTruncated && inputBytes > 0 {
		a.inputOriginalBytes = inputBytes
	}
	if a.outputTruncated && outputBytes > 0 {
		a.outputOriginalBytes = outputBytes
	}
}

// InputOriginalBytes returns the original input byte count when the input was
// truncated and the count is known. It returns 0 for untruncated or legacy
// truncated rows without size metadata.
func (a *CommandAudit) InputOriginalBytes() int { return a.inputOriginalBytes }

// OutputOriginalBytes returns the original output byte count when the output
// was truncated and the count is known. It returns 0 for untruncated or legacy
// truncated rows without size metadata.
func (a *CommandAudit) OutputOriginalBytes() int { return a.outputOriginalBytes }

// SetRedaction sets whether redaction was applied during capture.
func (a *CommandAudit) SetRedaction(inputRedacted bool, outputRedacted bool) {
	if a == nil {
		return
	}

	a.inputRedacted = inputRedacted
	a.outputRedacted = outputRedacted
}

// InputRedacted reports whether input redaction was applied.
func (a *CommandAudit) InputRedacted() bool { return a.inputRedacted }

// OutputRedacted reports whether output redaction was applied.
func (a *CommandAudit) OutputRedacted() bool { return a.outputRedacted }

// ExitCode returns the exit code, or empty if not captured.
func (a *CommandAudit) ExitCode() types.Optional[int] { return a.exitCode }

// SetExitCode sets the exit code for the command.
func (a *CommandAudit) SetExitCode(code types.Optional[int]) {
	if a == nil {
		return
	}
	_ = a.ClassifyOutcome(code, a.failureReason, a.failed)
}

// Failed reports whether the tool/command execution failed. This is a
// structural failure signal captured independently of exitCode, because
// some hosts (e.g. Claude Code's PostToolUseFailure payload) report failure
// without a numeric exit code in the hook payload. See docs/hooks/contract.md.
func (a *CommandAudit) Failed() bool { return a.failed }

// FailureReason returns structured evidence for the audit outcome. Unknown is
// explicit for legacy rows and captures without outcome evidence.
func (a *CommandAudit) FailureReason() types.CommandFailureReason {
	if a == nil || a.failureReason.String() == "" {
		return types.CommandFailureReasonUnknown
	}
	return a.failureReason
}

// ClassifyOutcome resolves structured outcome evidence. A zero exit code is
// authoritative success even when payload text or a host flag looks like a
// failure. Specific signal/timeout/denial evidence takes precedence over a
// non-zero code; otherwise the code is classified as exit_code.
func (a *CommandAudit) ClassifyOutcome(
	exitCode types.Optional[int],
	reportedReason types.CommandFailureReason,
	structuralFailure bool,
) error {
	if a == nil {
		return nil
	}
	reason := reportedReason
	if reason.String() == "" {
		reason = types.CommandFailureReasonUnknown
	}
	validated, err := types.CommandFailureReasonFrom(reason.String())
	if err != nil {
		return xerrors.Errorf("invalid command failure reason: %w", err)
	}

	if code, ok := exitCode.Value(); ok {
		a.exitCode = exitCode
		if code == 0 {
			a.failureReason = types.CommandFailureReasonNone
			a.failed = false
			return nil
		}
		switch validated {
		case types.CommandFailureReasonSignal,
			types.CommandFailureReasonTimeout,
			types.CommandFailureReasonHookDenied:
			a.failureReason = validated
		default:
			a.failureReason = types.CommandFailureReasonExitCode
		}
		a.failed = true
		return nil
	}

	a.exitCode = types.None[int]()
	if validated == types.CommandFailureReasonUnknown && structuralFailure {
		validated = types.CommandFailureReasonHostError
	}
	a.failureReason = validated
	a.failed = validated.IsFailure()
	return nil
}

// SetFailed marks whether the tool/command execution failed.
func (a *CommandAudit) SetFailed(failed bool) {
	if a == nil {
		return
	}
	_ = a.ClassifyOutcome(a.exitCode, a.failureReason, failed)
}

func inferAuditPayloadOriginalBytes(payload string, truncated bool) int {
	if !truncated {
		return 0
	}
	return AuditPayloadOriginalBytesFromTruncationMarker(payload)
}

// AuditPayloadTruncationMarker returns the canonical marker embedded between
// the preserved head and tail of an ingest-truncated command-audit payload.
func AuditPayloadTruncationMarker(originalBytes int) string {
	if originalBytes <= 0 {
		return auditTruncationMarkerPrefix + "0" + auditTruncationMarkerSuffix
	}
	return auditTruncationMarkerPrefix + strconv.Itoa(originalBytes) + auditTruncationMarkerSuffix
}

// AuditPayloadOriginalBytesFromTruncationMarker restores the original byte
// count from the canonical truncation marker. When multiple markers are
// present because user-controlled head text contains marker-looking content,
// the last marker wins because Traceary appends its canonical marker between
// the preserved head and tail.
func AuditPayloadOriginalBytesFromTruncationMarker(payload string) int {
	markerIndex := strings.LastIndex(payload, auditTruncationMarkerPrefix)
	if markerIndex < 0 {
		return 0
	}
	valueStart := markerIndex + len(auditTruncationMarkerPrefix)
	suffixIndex := strings.Index(payload[valueStart:], auditTruncationMarkerSuffix)
	if suffixIndex < 0 {
		return 0
	}
	valueText := payload[valueStart : valueStart+suffixIndex]
	value, err := strconv.Atoi(valueText)
	if err != nil {
		return 0
	}
	return value
}
