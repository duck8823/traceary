package types

import (
	"strings"

	"golang.org/x/xerrors"
)

// SessionID is a value object representing a work session identifier.
type SessionID string

// SessionIDOf creates a SessionID from a string.
func SessionIDOf(value string) (SessionID, error) {
	trimmedValue := strings.TrimSpace(value)
	if trimmedValue == "" {
		return SessionID(""), xerrors.Errorf("session ID must not be empty")
	}
	return SessionID(trimmedValue), nil
}

// String returns the string representation.
func (s SessionID) String() string { return string(s) }
