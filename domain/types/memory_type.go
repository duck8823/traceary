package types

import (
	"slices"
	"strings"

	"golang.org/x/xerrors"
)

// MemoryType classifies durable memory.
type MemoryType string

const (
	// MemoryTypePreference stores stable preferences.
	MemoryTypePreference MemoryType = "preference"
	// MemoryTypeDecision stores important decisions.
	MemoryTypeDecision MemoryType = "decision"
	// MemoryTypeConstraint stores constraints.
	MemoryTypeConstraint MemoryType = "constraint"
	// MemoryTypeLesson stores lessons learned.
	MemoryTypeLesson MemoryType = "lesson"
	// MemoryTypeArtifact stores durable artifact pointers.
	MemoryTypeArtifact MemoryType = "artifact"
)

var knownMemoryTypes = []MemoryType{
	MemoryTypePreference,
	MemoryTypeDecision,
	MemoryTypeConstraint,
	MemoryTypeLesson,
	MemoryTypeArtifact,
}

// MemoryTypeFrom creates a MemoryType from a string.
func MemoryTypeFrom(value string) (MemoryType, error) {
	trimmedValue := strings.TrimSpace(value)
	if trimmedValue == "" {
		return MemoryType(""), xerrors.Errorf("memory type must not be empty")
	}
	if slices.Contains(knownMemoryTypes, MemoryType(trimmedValue)) {
		return MemoryType(trimmedValue), nil
	}
	return MemoryType(""), xerrors.Errorf("unknown memory type: %s", trimmedValue)
}

// String returns the string representation.
func (m MemoryType) String() string { return string(m) }
