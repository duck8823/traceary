package domain

import (
	"errors"
	"strings"
)

// ArchiveSequence is the positive, store-local order assigned to a canonical
// event. It is deliberately distinct from every search projection sequence.
type ArchiveSequence int64

// NewArchiveSequence validates a persisted archive sequence.
func NewArchiveSequence(value int64) (ArchiveSequence, error) {
	if value <= 0 {
		return 0, errors.New("archive sequence must be positive")
	}
	return ArchiveSequence(value), nil
}

// Int64 returns the SQLite-compatible representation.
func (s ArchiveSequence) Int64() int64 { return int64(s) }

// ArchiveInventoryPhase is the durable phase of one sequence inventory.
type ArchiveInventoryPhase string

const (
	// ArchiveInventoryIdle has no active inventory generation.
	ArchiveInventoryIdle ArchiveInventoryPhase = "idle"
	// ArchiveInventoryScanning assigns stable sequences to historical events.
	ArchiveInventoryScanning ArchiveInventoryPhase = "inventory"
	// ArchiveInventoryVerifying checks the frozen contiguous prefix.
	ArchiveInventoryVerifying ArchiveInventoryPhase = "verifying"
	// ArchiveInventoryComplete is the only activation-eligible terminal phase.
	ArchiveInventoryComplete ArchiveInventoryPhase = "complete"
	// ArchiveInventoryFailed is a fail-closed terminal phase.
	ArchiveInventoryFailed ArchiveInventoryPhase = "failed"
)

// ArchiveInventoryGeneration identifies a resumable inventory and its frozen
// verification boundary. HighWater is zero until scanning finishes.
type ArchiveInventoryGeneration struct {
	ID        string
	Phase     ArchiveInventoryPhase
	HighWater int64
	Revision  int64
}

// Validate rejects states that could accidentally authorize an unverified
// archive prefix.
func (g ArchiveInventoryGeneration) Validate() error {
	if strings.TrimSpace(g.ID) == "" || g.Revision < 0 || g.HighWater < 0 {
		return errors.New("invalid archive inventory generation")
	}
	switch g.Phase {
	case ArchiveInventoryScanning, ArchiveInventoryVerifying, ArchiveInventoryComplete, ArchiveInventoryFailed:
		return nil
	default:
		return errors.New("unknown archive inventory phase")
	}
}
