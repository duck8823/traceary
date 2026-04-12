package types

import (
	"slices"
	"strings"

	"golang.org/x/xerrors"
)

// MemorySource describes how a durable memory was produced.
type MemorySource string

const (
	// MemorySourceManual indicates a manually entered memory.
	MemorySourceManual MemorySource = "manual"
	// MemorySourceExtracted indicates a memory extracted from existing signals.
	MemorySourceExtracted MemorySource = "extracted"
	// MemorySourceImported indicates a memory imported from another source.
	MemorySourceImported MemorySource = "imported"
)

var knownMemorySources = []MemorySource{
	MemorySourceManual,
	MemorySourceExtracted,
	MemorySourceImported,
}

// MemorySourceOf creates a MemorySource from a string.
func MemorySourceOf(value string) (MemorySource, error) {
	trimmedValue := strings.TrimSpace(value)
	if trimmedValue == "" {
		return MemorySource(""), xerrors.Errorf("memory source must not be empty")
	}
	if slices.Contains(knownMemorySources, MemorySource(trimmedValue)) {
		return MemorySource(trimmedValue), nil
	}
	return MemorySource(""), xerrors.Errorf("unknown memory source: %s", trimmedValue)
}

// String returns the string representation.
func (m MemorySource) String() string { return string(m) }
