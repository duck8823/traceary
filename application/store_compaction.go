package application

import (
	"context"
	"time"

	"github.com/duck8823/traceary/domain"
)

// StoreCompactionJournal persists bounded protocol records outside SQLite.
type StoreCompactionJournal interface {
	Create(context.Context, domain.CompactionRun) error
	Load(context.Context, string) (domain.CompactionRun, error)
	Append(context.Context, domain.CompactionRun) error
}

// StoreCompactionBuilder creates and verifies a compact SQLite candidate.
type StoreCompactionBuilder interface {
	Build(context.Context, string, string) error
	ClassifyCandidate(context.Context, string, string) (domain.CandidateCondition, error)
	Sync(context.Context, string) error
	VerifyPair(context.Context, string, string) error
}

// CompactBodyGateInspector classifies discardable-age bodies on the source.
type CompactBodyGateInspector interface {
	InspectBodyGate(ctx context.Context, source string, cutoff time.Time) (BodyGate, error)
}

// CandidateWorkspace owns prepared candidate inode creation and removal.
type CandidateWorkspace interface {
	PrepareCandidate(context.Context, domain.CompactionRun) (domain.StoreFileIdentity, error)
	RemoveOwnedPartialCandidate(context.Context, domain.CompactionRun, domain.CompactionObservation) error
}

// StoreReplacementCoordinator owns file identity and atomic exchange.
type StoreReplacementCoordinator interface {
	Plan(context.Context, domain.CompactionRun) (domain.CompactionRun, error)
	Recheck(context.Context, domain.CompactionRun) error
	// RejectRetiredSearchIndex refuses to publish a store that still carries
	// the retired migration-032 search index family. Plan already checks, but
	// a run journaled by an older binary carries no such verdict, and resuming
	// one past candidate verification reaches the exchange without revisiting
	// Plan or Build. This runs behind the exclusive lease on every path.
	RejectRetiredSearchIndex(context.Context, domain.CompactionRun) error
	FenceCandidate(context.Context, domain.CompactionRun) (domain.CompactionRun, error)
	Exchange(context.Context, domain.CompactionRun) error
	PublishRollback(context.Context, domain.CompactionRun) error
	Observe(context.Context, domain.CompactionRun) (domain.CompactionObservation, error)
	SyncRecoveredOrientation(context.Context, domain.CompactionRun, domain.CompactionObservation) error
}

// StoreCompactionFiles is the complete consumer-facing file protocol required
// by compaction. Implementations cannot omit prepared-candidate ownership.
type StoreCompactionFiles interface {
	StoreReplacementCoordinator
	CandidateWorkspace
}

// StoreCompactionLease excludes every cooperating normal connection for apply.
type StoreCompactionLease interface {
	AcquireExclusive(context.Context, string) (func(), error)
}

// StoreCompactionUsecase provides the explicit maintenance workflow.
type StoreCompactionUsecase interface {
	Compact(context.Context, CompactInput) (CompactResult, error)
	Plan(context.Context, string) (domain.CompactionRun, error)
	Apply(context.Context, string) (domain.CompactionRun, error)
	Resume(context.Context, string) (domain.CompactionRun, error)
	Status(context.Context, string) (domain.CompactionRun, error)
	Rollback(context.Context, string) (domain.CompactionRun, error)
}

// CompactionInFlightFinder locates a non-terminal journal for a store.
type CompactionInFlightFinder interface {
	FindInFlight(context.Context, string) (domain.CompactionRun, error)
}
