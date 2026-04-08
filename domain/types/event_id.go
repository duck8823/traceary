package types

import (
	"strings"

	"golang.org/x/xerrors"
)

// EventID is a value object representing an event identifier.
type EventID string

// EventIDOf creates an EventID from a string.
func EventIDOf(value string) (EventID, error) {
	trimmedValue := strings.TrimSpace(value)
	if trimmedValue == "" {
		return EventID(""), xerrors.Errorf("event ID must not be empty")
	}
	return EventID(trimmedValue), nil
}

// String returns the string representation.
func (e EventID) String() string { return string(e) }
