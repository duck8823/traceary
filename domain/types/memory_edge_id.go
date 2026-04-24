package types

import (
	"strings"

	"golang.org/x/xerrors"
)

// MemoryEdgeID identifies a memory-graph edge. Edges are additive
// overlay on top of the memory table (see #573); an ID is unique
// within a single SQLite store and has no cross-store meaning.
type MemoryEdgeID string

// MemoryEdgeIDOf creates a MemoryEdgeID from a string. Empty /
// whitespace-only values are rejected — callers should pass a fresh
// UUID-like token the edge repository can write.
func MemoryEdgeIDOf(value string) (MemoryEdgeID, error) {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return MemoryEdgeID(""), xerrors.Errorf("memory edge ID must not be empty")
	}
	return MemoryEdgeID(trimmed), nil
}

// String returns the string representation.
func (m MemoryEdgeID) String() string { return string(m) }
