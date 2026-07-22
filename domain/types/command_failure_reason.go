package types

import (
	"slices"
	"strings"

	"golang.org/x/xerrors"
)

// CommandFailureReason is the structured outcome classification captured for
// a command audit. Unknown is explicit legacy or unavailable evidence; it is
// never inferred from command output text.
type CommandFailureReason string

const (
	// CommandFailureReasonUnknown means no structured outcome evidence exists.
	CommandFailureReasonUnknown CommandFailureReason = "unknown"
	// CommandFailureReasonNone means structured evidence confirms success.
	CommandFailureReasonNone CommandFailureReason = "none"
	// CommandFailureReasonExitCode means a non-zero exit code caused failure.
	CommandFailureReasonExitCode CommandFailureReason = "exit_code"
	// CommandFailureReasonSignal means execution was interrupted by a signal.
	CommandFailureReasonSignal CommandFailureReason = "signal"
	// CommandFailureReasonTimeout means execution exceeded its time limit.
	CommandFailureReasonTimeout CommandFailureReason = "timeout"
	// CommandFailureReasonHookDenied means a host hook denied execution.
	CommandFailureReasonHookDenied CommandFailureReason = "hook_denied"
	// CommandFailureReasonHostError means the host reported a structured error.
	CommandFailureReasonHostError CommandFailureReason = "host_error"
)

var knownCommandFailureReasons = []CommandFailureReason{
	CommandFailureReasonUnknown,
	CommandFailureReasonNone,
	CommandFailureReasonExitCode,
	CommandFailureReasonSignal,
	CommandFailureReasonTimeout,
	CommandFailureReasonHookDenied,
	CommandFailureReasonHostError,
}

// CommandFailureReasonFrom validates a persisted failure reason.
func CommandFailureReasonFrom(value string) (CommandFailureReason, error) {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return "", xerrors.New("command failure reason must not be empty")
	}
	reason := CommandFailureReason(trimmed)
	if !slices.Contains(knownCommandFailureReasons, reason) {
		return "", xerrors.Errorf(
			"unknown command failure reason: %s (allowed values: %s)",
			trimmed,
			strings.Join(KnownCommandFailureReasonStrings(), ", "),
		)
	}
	return reason, nil
}

// IsFailure reports whether the reason is affirmative structured failure
// evidence. Unknown is deliberately not treated as success or failure.
func (r CommandFailureReason) IsFailure() bool {
	switch r {
	case CommandFailureReasonExitCode,
		CommandFailureReasonSignal,
		CommandFailureReasonTimeout,
		CommandFailureReasonHookDenied,
		CommandFailureReasonHostError:
		return true
	default:
		return false
	}
}

func (r CommandFailureReason) String() string { return string(r) }

// KnownCommandFailureReasonStrings returns every accepted persisted value.
func KnownCommandFailureReasonStrings() []string {
	values := make([]string, 0, len(knownCommandFailureReasons))
	for _, reason := range knownCommandFailureReasons {
		values = append(values, reason.String())
	}
	return values
}
