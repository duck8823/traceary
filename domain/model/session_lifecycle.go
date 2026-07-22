package model

import (
	"fmt"
	"time"

	"github.com/duck8823/traceary/domain/types"
)

// SessionTerminalTransition describes the observable outcome of applying a
// proposed terminal state to a Session aggregate.
type SessionTerminalTransition string

const (
	// SessionTerminalTransitionApplied means this call established the first
	// effective terminal state.
	SessionTerminalTransitionApplied SessionTerminalTransition = "applied"
	// SessionTerminalTransitionAlreadyApplied means the same reason was already
	// effective and no timestamp, summary, or reason was changed.
	SessionTerminalTransitionAlreadyApplied SessionTerminalTransition = "already_applied"
)

// SessionTerminalConflictError preserves both the effective and proposed
// reasons so callers can diagnose a fail-closed transition.
type SessionTerminalConflictError struct {
	current  types.TerminalReason
	proposed types.TerminalReason
}

// Error describes the conflicting reasons without session content.
func (e *SessionTerminalConflictError) Error() string {
	return fmt.Sprintf("terminal reason %q conflicts with effective reason %q", e.proposed, e.current)
}

// Unwrap supports errors.Is(err, ErrConflictingTerminalState).
func (e *SessionTerminalConflictError) Unwrap() error { return ErrConflictingTerminalState }

// CurrentReason returns the effective reason that remains unchanged.
func (e *SessionTerminalConflictError) CurrentReason() types.TerminalReason { return e.current }

// ProposedReason returns the rejected reason.
func (e *SessionTerminalConflictError) ProposedReason() types.TerminalReason { return e.proposed }

// Terminate applies the session's single effective terminal state. Repeating
// the same reason is idempotent and preserves the first timestamp and summary;
// a different reason fails closed with diagnostic reason values.
func (s *Session) Terminate(
	endedAt time.Time,
	reason types.TerminalReason,
	summary string,
) (SessionTerminalTransition, error) {
	if s == nil {
		return SessionTerminalTransition(""), ErrInvalidSessionState
	}
	validatedReason, err := types.TerminalReasonFrom(reason.String())
	if err != nil {
		return SessionTerminalTransition(""), err
	}
	if endedAt.IsZero() || endedAt.Before(s.startedAt) {
		return SessionTerminalTransition(""), ErrInvalidSessionState
	}
	if current, terminal := s.terminalReason.Value(); terminal {
		if current == validatedReason {
			return SessionTerminalTransitionAlreadyApplied, nil
		}
		return SessionTerminalTransition(""), &SessionTerminalConflictError{
			current:  current,
			proposed: validatedReason,
		}
	}
	if _, ended := s.endedAt.Value(); ended {
		// Rehydration guarantees an ended session has a reason. Keep this guard so
		// any in-memory invariant breach fails closed rather than overwriting it.
		return SessionTerminalTransition(""), ErrInvalidSessionState
	}
	s.endedAt = types.Some(endedAt)
	s.terminalReason = types.Some(validatedReason)
	s.summary = summary
	return SessionTerminalTransitionApplied, nil
}

// FinalizeOneShot applies a terminal transition only to an explicitly
// supervised one-shot session. Other runtime modes require their own
// authoritative lifecycle signal and must never be synthesized here.
func (s *Session) FinalizeOneShot(endedAt time.Time, reason types.TerminalReason, summary string) (SessionTerminalTransition, error) {
	if s == nil || s.runtimeMode != types.RuntimeModeOneShot {
		return SessionTerminalTransition(""), ErrInvalidSessionState
	}
	return s.Terminate(endedAt, reason, summary)
}
