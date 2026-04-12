package types

import (
	"slices"
	"strings"

	"golang.org/x/xerrors"
)

// MemoryStatus represents the lifecycle status of a memory.
type MemoryStatus string

const (
	// MemoryStatusCandidate indicates review is still required.
	MemoryStatusCandidate MemoryStatus = "candidate"
	// MemoryStatusAccepted indicates the memory is active.
	MemoryStatusAccepted MemoryStatus = "accepted"
	// MemoryStatusRejected indicates the memory was rejected.
	MemoryStatusRejected MemoryStatus = "rejected"
	// MemoryStatusSuperseded indicates the memory was replaced.
	MemoryStatusSuperseded MemoryStatus = "superseded"
	// MemoryStatusExpired indicates the memory is no longer active due to expiry.
	MemoryStatusExpired MemoryStatus = "expired"
)

var knownMemoryStatuses = []MemoryStatus{
	MemoryStatusCandidate,
	MemoryStatusAccepted,
	MemoryStatusRejected,
	MemoryStatusSuperseded,
	MemoryStatusExpired,
}

// MemoryStatusOf creates a MemoryStatus from a string.
func MemoryStatusOf(value string) (MemoryStatus, error) {
	trimmedValue := strings.TrimSpace(value)
	if trimmedValue == "" {
		return MemoryStatus(""), xerrors.Errorf("memory status must not be empty")
	}
	if slices.Contains(knownMemoryStatuses, MemoryStatus(trimmedValue)) {
		return MemoryStatus(trimmedValue), nil
	}
	return MemoryStatus(""), xerrors.Errorf("unknown memory status: %s", trimmedValue)
}

// String returns the string representation.
func (m MemoryStatus) String() string { return string(m) }
