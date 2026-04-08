package types

import (
	"slices"
	"strings"

	"golang.org/x/xerrors"
)

const (
	// EventKindNote represents note events.
	EventKindNote EventKind = "note"
	// EventKindCommandExecuted represents command execution events.
	EventKindCommandExecuted EventKind = "command_executed"
	// EventKindReviewed represents review events.
	EventKindReviewed EventKind = "reviewed"
	// EventKindSessionStarted represents session start events.
	EventKindSessionStarted EventKind = "session_started"
	// EventKindSessionEnded represents session end events.
	EventKindSessionEnded EventKind = "session_ended"
)

// EventKind is a value object that represents an event kind.
type EventKind string

var knownEventKinds = []EventKind{
	EventKindNote,
	EventKindCommandExecuted,
	EventKindReviewed,
	EventKindSessionStarted,
	EventKindSessionEnded,
}

// EventKindOf builds EventKind from a string value.
func EventKindOf(value string) (EventKind, error) {
	trimmedValue := strings.TrimSpace(value)
	if trimmedValue == "" {
		return EventKind(""), xerrors.Errorf("event kind must not be empty")
	}

	if slices.Contains(knownEventKinds, EventKind(trimmedValue)) {
		return EventKind(trimmedValue), nil
	}

	return EventKind(""), xerrors.Errorf(
		"unknown event kind: %s (allowed values: %s)",
		trimmedValue,
		strings.Join(KnownEventKindStrings(), ", "),
	)
}

// String returns the string representation.
func (e EventKind) String() string { return string(e) }

// KnownEventKindStrings returns the list of known event kind values.
func KnownEventKindStrings() []string {
	values := make([]string, 0, len(knownEventKinds))
	for _, kind := range knownEventKinds {
		values = append(values, kind.String())
	}

	return values
}
