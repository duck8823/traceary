package types

import (
	"strings"

	"golang.org/x/xerrors"
)

// MemoryID identifies a durable memory.
type MemoryID string

// MemoryIDFrom creates a MemoryID from a string.
func MemoryIDFrom(value string) (MemoryID, error) {
	trimmedValue := strings.TrimSpace(value)
	if trimmedValue == "" {
		return MemoryID(""), xerrors.Errorf("memory ID must not be empty")
	}
	return MemoryID(trimmedValue), nil
}

// String returns the string representation.
func (m MemoryID) String() string { return string(m) }
