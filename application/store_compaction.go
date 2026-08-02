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
	Sync(context.Context, string) error
	VerifyPair(context.Context, string, string) error
}

// StoreReplacementCoordinator owns file identity and atomic exchange.
type StoreReplacementCoordinator interface {
	Plan(context.Context, domain.CompactionRun) (domain.CompactionRun, error)
	FenceCandidate(context.Context, domain.CompactionRun) (domain.CompactionRun, error)
	Exchange(context.Context, domain.CompactionRun) error
	PublishRollback(context.Context, domain.CompactionRun) error
	Orientation(context.Context, domain.CompactionRun) (domain.CompactionPhase, error)
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
