package types

import (
	"slices"
	"strings"

	"golang.org/x/xerrors"
)

// SessionStatus represents the lifecycle status a session snapshot reports.
type SessionStatus string

const (
	// SessionStatusActive indicates the session has no end marker and is within the stale window.
	SessionStatusActive SessionStatus = "active"
	// SessionStatusStale indicates the session has no end marker but started before the stale window.
	SessionStatusStale SessionStatus = "stale"
	// SessionStatusEnded indicates the session has an end marker and no events after it.
	SessionStatusEnded SessionStatus = "ended"
	// SessionStatusEndedWithLateEvents indicates the session has an end marker but later events arrived.
	// It keeps such sessions visible in snapshots instead of silently dropping them.
	SessionStatusEndedWithLateEvents SessionStatus = "ended_with_late_events"
)

var knownSessionStatuses = []SessionStatus{
	SessionStatusActive,
	SessionStatusStale,
	SessionStatusEnded,
	SessionStatusEndedWithLateEvents,
}

// SessionStatusFrom creates a SessionStatus from a string.
func SessionStatusFrom(value string) (SessionStatus, error) {
	trimmedValue := strings.TrimSpace(value)
	if trimmedValue == "" {
		return SessionStatus(""), xerrors.Errorf("session status must not be empty")
	}
	if slices.Contains(knownSessionStatuses, SessionStatus(trimmedValue)) {
		return SessionStatus(trimmedValue), nil
	}
	return SessionStatus(""), xerrors.Errorf("unknown session status: %s", trimmedValue)
}

// String returns the string representation.
func (s SessionStatus) String() string { return string(s) }
