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

// CandidateWorkspace owns prepared candidate inode creation and removal.
type CandidateWorkspace interface {
	PrepareCandidate(context.Context, domain.CompactionRun) (domain.StoreFileIdentity, error)
	RemoveOwnedPartialCandidate(context.Context, domain.CompactionRun, domain.CompactionObservation) error
}

// StoreReplacementCoordinator owns file identity and atomic exchange.
type StoreReplacementCoordinator interface {
	Plan(context.Context, domain.CompactionRun) (domain.CompactionRun, error)
	Recheck(context.Context, domain.CompactionRun) error
	FenceCandidate(context.Context, domain.CompactionRun) (domain.CompactionRun, error)
	Exchange(context.Context, domain.CompactionRun) error
	PublishRollback(context.Context, domain.CompactionRun) error
	Observe(context.Context, domain.CompactionRun) (domain.CompactionObservation, error)
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
	Plan(context.Context, string) (domain.CompactionRun, error)
	Apply(context.Context, string) (domain.CompactionRun, error)
	Resume(context.Context, string) (domain.CompactionRun, error)
	Status(context.Context, string) (domain.CompactionRun, error)
	Rollback(context.Context, string) (domain.CompactionRun, error)
}
