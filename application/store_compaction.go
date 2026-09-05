package application

import (
	"context"

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

// CommandBodyReclaimInspector measures duplicated command_executed bodies
// on the source. Compact reports that stored-byte sum after a successful rewrite.
type CommandBodyReclaimInspector interface {
	InspectCommandBodyReclaim(ctx context.Context, source string) (CommandBodyReclaim, error)
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

// CompactionProgress reports journal phase changes and replica-build bytes
// to the operator. Implementations write diagnostics (typically stderr).
type CompactionProgress interface {
	OnPhase(phase domain.CompactionPhase, destinationBytes uint64)
	WatchBuild(ctx context.Context, candidatePath string, destinationBytes uint64) (stop func())
}

// StoreCompactionUsecase provides the explicit maintenance workflow.
type StoreCompactionUsecase interface {
	Compact(context.Context, CompactInput) (CompactResult, error)
	Plan(context.Context, string) (domain.CompactionRun, error)
	Apply(context.Context, string) (domain.CompactionRun, error)
	Resume(context.Context, string) (domain.CompactionRun, error)
	Status(context.Context, string) (domain.CompactionRun, error)
	Rollback(context.Context, string) (domain.CompactionRun, error)
	// AbandonStalePrePublication journals abandoned and removes the
	// candidate when an in-flight run is strictly pre-publication and the
	// live source identity no longer matches the journal fence.
	AbandonStalePrePublication(context.Context, string) (domain.CompactionRun, error)
}

// CompactionInFlightFinder locates a non-terminal journal for a store.
type CompactionInFlightFinder interface {
	FindInFlight(context.Context, string) (domain.CompactionRun, error)
}

// CompactionPrePublicationCleanup inspects the live source and removes an
// abandoned candidate without requiring the journaled source identity to
// still match (the mismatch is why abandon is needed).
type CompactionPrePublicationCleanup interface {
	InspectStoreFile(context.Context, string) (domain.StoreFileIdentity, error)
	RemoveAbandonedCandidate(context.Context, domain.CompactionRun) error
}
