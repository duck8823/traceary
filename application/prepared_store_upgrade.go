package application

import (
	"context"

	"github.com/duck8823/traceary/domain"
)

// PreparedStoreUpgradeCommand starts one operation selected from the fixed
// composition-time recipe registry.
type PreparedStoreUpgradeCommand struct {
	Operation                    domain.PreparedStoreUpgradeOperation
	TargetPath                   string
	ConsumerBinding              string
	Budget                       domain.PreparedStoreUpgradeBudget
	BoundDropApproval            *domain.BoundDropApproval
	UnavailableRetentionApproval *domain.UnavailableRetentionApproval
}

// PreparedCandidateRequest contains only durable, recipe-safe values.
type PreparedCandidateRequest struct {
	Run domain.PreparedStoreUpgradeRun
}

// PreparedCandidateInspection identifies an owned candidate without exposing
// an open SQLite handle through the application boundary.
type PreparedCandidateInspection struct {
	Run         domain.PreparedStoreUpgradeRun
	Observation domain.PreparedStoreUpgradeObservation
}

// PreparedCandidateRecipe owns candidate contents and logical verification.
type PreparedCandidateRecipe interface {
	Plan(context.Context, PreparedCandidateRequest) (string, error)
	Build(context.Context, PreparedCandidateRequest) error
	ClassifyOwnedCandidate(context.Context, PreparedCandidateInspection) (domain.CandidateCondition, error)
	Sync(context.Context, PreparedCandidateRequest) error
	Verify(context.Context, PreparedCandidateRequest) (domain.PreparedCandidateEvidence, error)
}

// BoundDropVerifier re-checks a bound drop approval against the live source
// immediately before Build. Recipes that do not drop rows omit it.
type BoundDropVerifier interface {
	VerifyBoundDrop(context.Context, PreparedCandidateRequest) error
}

// UnavailableRetentionVerifier re-checks a bound unavailable_retention
// approval against the live source immediately before Build.
type UnavailableRetentionVerifier interface {
	VerifyUnavailableRetention(context.Context, PreparedCandidateRequest) error
}

// PreparedStoreUpgradeJournal persists bounded protocol records outside every
// SQLite inode participating in exchange.
type PreparedStoreUpgradeJournal interface {
	Create(context.Context, domain.PreparedStoreUpgradeRun) error
	Load(context.Context, string) (domain.PreparedStoreUpgradeRun, error)
	FindActive(context.Context, domain.PreparedStoreUpgradeOperation, string, string) (domain.PreparedStoreUpgradeRun, error)
	Append(context.Context, domain.PreparedStoreUpgradeRun) error
}

// PreparedStoreUpgradeFiles is the single physical orientation implementation
// shared with compaction.
type PreparedStoreUpgradeFiles interface {
	Plan(context.Context, domain.PreparedStoreUpgradeRun) (domain.PreparedStoreUpgradeRun, error)
	PrepareCandidate(context.Context, domain.PreparedStoreUpgradeRun) (domain.StoreFileIdentity, error)
	RemoveOwnedPartialCandidate(context.Context, domain.PreparedStoreUpgradeRun, domain.PreparedStoreUpgradeObservation) error
	Recheck(context.Context, domain.PreparedStoreUpgradeRun) error
	RecheckForPublish(context.Context, domain.PreparedStoreUpgradeRun) error
	// RejectRetiredSearchIndex refuses to publish a store that still carries
	// the retired migration-032 search index family. Compaction declares the
	// same method; both protocols reach the same exchange, so both must ask.
	// An offline-migration upgrade is exempt — that publication is how a
	// store reaches the schema where the family can be retired at all.
	RejectRetiredSearchIndex(context.Context, domain.PreparedStoreUpgradeRun) error
	FenceCandidate(context.Context, domain.PreparedStoreUpgradeRun) (domain.PreparedStoreUpgradeRun, error)
	Exchange(context.Context, domain.PreparedStoreUpgradeRun) error
	PublishRollback(context.Context, domain.PreparedStoreUpgradeRun) error
	Observe(context.Context, domain.PreparedStoreUpgradeRun) (domain.PreparedStoreUpgradeObservation, error)
	SyncRecoveredOrientation(context.Context, domain.PreparedStoreUpgradeRun, domain.PreparedStoreUpgradeObservation) error
}

// PreparedStoreUpgradeLease excludes cooperating SQLite connections only for
// publication or physical recovery.
type PreparedStoreUpgradeLease interface {
	AcquireExclusive(context.Context, string) (func(), error)
	HoldsExclusive(storePath string) bool
}

// PreparedStoreUpgradeReceipt binds a published inode to body-free evidence.
type PreparedStoreUpgradeReceipt struct {
	RunID               string
	Operation           domain.PreparedStoreUpgradeOperation
	ConsumerBinding     string
	TargetIdentity      domain.StoreFileIdentity
	RollbackIdentity    domain.StoreFileIdentity
	RollbackPath        string
	Evidence            domain.PreparedCandidateEvidence
	PublishMilliseconds int64
}

// PreparedStoreUpgradeUsecase provides the staged maintenance protocol.
type PreparedStoreUpgradeUsecase interface {
	Plan(context.Context, PreparedStoreUpgradeCommand) (domain.PreparedStoreUpgradeRun, error)
	Prepare(context.Context, string) (domain.PreparedStoreUpgradeRun, error)
	Publish(context.Context, string) (PreparedStoreUpgradeReceipt, error)
	Resume(context.Context, string) (PreparedStoreUpgradeReceipt, error)
	Rollback(context.Context, string) (domain.PreparedStoreUpgradeRun, error)
	RunUpgrade(context.Context, PreparedStoreUpgradeCommand) (PreparedStoreUpgradeReceipt, error)
}
