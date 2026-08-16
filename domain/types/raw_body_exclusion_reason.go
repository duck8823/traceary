package types

import "golang.org/x/xerrors"

// RawBodyExclusionReason is the first allowlist condition that an excluded event fails.
// Evaluation order matches select_discardable_event_bodies.sql:
// kind, availability, session identity, session ended, ts_valid, coverage.
type RawBodyExclusionReason string

// Exclusion reasons in allowlist-evaluation order (first failing condition wins).
const (
	RawBodyExclusionReasonNotTranscript   RawBodyExclusionReason = "not_transcript"
	RawBodyExclusionReasonAlreadyDiscarded RawBodyExclusionReason = "already_discarded"
	RawBodyExclusionReasonSessionMissing  RawBodyExclusionReason = "session_missing"
	RawBodyExclusionReasonSessionActive   RawBodyExclusionReason = "session_active"
	RawBodyExclusionReasonWithinRetention RawBodyExclusionReason = "within_retention"
	RawBodyExclusionReasonUncovered       RawBodyExclusionReason = "uncovered"
)

// RawBodyExclusionReasonFrom validates a persisted exclusion-reason value.
func RawBodyExclusionReasonFrom(value string) (RawBodyExclusionReason, error) {
	reason := RawBodyExclusionReason(value)
	switch reason {
	case RawBodyExclusionReasonNotTranscript,
		RawBodyExclusionReasonAlreadyDiscarded,
		RawBodyExclusionReasonSessionMissing,
		RawBodyExclusionReasonSessionActive,
		RawBodyExclusionReasonWithinRetention,
		RawBodyExclusionReasonUncovered:
		return reason, nil
	default:
		return "", xerrors.Errorf("unsupported raw body exclusion reason: %q", value)
	}
}

// String returns the persisted representation.
func (r RawBodyExclusionReason) String() string { return string(r) }
