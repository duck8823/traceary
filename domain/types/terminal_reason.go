package types

import (
	"slices"
	"strings"

	"golang.org/x/xerrors"
)

// TerminalReason describes why a session reached its single effective end
// state. Its zero value means no terminal state and is not itself valid.
type TerminalReason string

const (
	// TerminalReasonSuccess records normal completion.
	TerminalReasonSuccess TerminalReason = "success"
	// TerminalReasonFailure records a completed run with a failure result.
	TerminalReasonFailure TerminalReason = "failure"
	// TerminalReasonTimeout records expiration of the runtime deadline.
	TerminalReasonTimeout TerminalReason = "timeout"
	// TerminalReasonSignal records termination by an operating-system signal.
	TerminalReasonSignal TerminalReason = "signal"
	// TerminalReasonAbortedStream records a stream that ended without completion.
	TerminalReasonAbortedStream TerminalReason = "aborted_stream"
	// TerminalReasonLegacyUnknown records an end marker without typed evidence.
	TerminalReasonLegacyUnknown TerminalReason = "legacy_unknown"
)

var knownTerminalReasons = []TerminalReason{
	TerminalReasonSuccess,
	TerminalReasonFailure,
	TerminalReasonTimeout,
	TerminalReasonSignal,
	TerminalReasonAbortedStream,
	TerminalReasonLegacyUnknown,
}

// TerminalReasonFrom restores a validated TerminalReason from its persisted
// value. Empty is reserved for active sessions and is rejected here.
func TerminalReasonFrom(value string) (TerminalReason, error) {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return TerminalReason(""), xerrors.New("terminal reason must not be empty")
	}
	reason := TerminalReason(trimmed)
	if !slices.Contains(knownTerminalReasons, reason) {
		return TerminalReason(""), xerrors.Errorf(
			"unknown terminal reason: %s (allowed values: %s)",
			trimmed,
			strings.Join(KnownTerminalReasonStrings(), ", "),
		)
	}
	return reason, nil
}

// String returns the persisted terminal-reason value.
func (r TerminalReason) String() string { return string(r) }

// KnownTerminalReasonStrings returns all supported persisted values.
func KnownTerminalReasonStrings() []string {
	values := make([]string, 0, len(knownTerminalReasons))
	for _, reason := range knownTerminalReasons {
		values = append(values, reason.String())
	}
	return values
}
