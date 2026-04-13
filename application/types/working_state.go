package types

import "strings"

// WorkingState captures structured working-memory signals for handoff and
// context resumption without persisting a separate aggregate.
type WorkingState struct {
	sessionSummary string
	compactSummary string
}

// WorkingStateOf creates a WorkingState from session and compact summary
// signals.
func WorkingStateOf(sessionSummary string, compactSummary string) WorkingState {
	return WorkingState{
		sessionSummary: strings.TrimSpace(sessionSummary),
		compactSummary: strings.TrimSpace(compactSummary),
	}
}

// SessionSummary returns the session-end or operator-authored summary signal.
func (w WorkingState) SessionSummary() string { return w.sessionSummary }

// CompactSummary returns the latest compact-summary signal extracted from event
// history.
func (w WorkingState) CompactSummary() string { return w.compactSummary }

// CombinedSummary returns a single-line summary suitable for compatibility
// handoff surfaces.
func (w WorkingState) CombinedSummary() string {
	switch {
	case w.sessionSummary != "" && w.compactSummary != "":
		return w.sessionSummary + " | " + w.compactSummary
	case w.sessionSummary != "":
		return w.sessionSummary
	default:
		return w.compactSummary
	}
}
