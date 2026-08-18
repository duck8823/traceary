//nolint:revive // Compatibility aliases preserve the public compaction surface.
package domain

// Compaction types are compatibility names for the shared prepared-store
// upgrade aggregate. Their JSON shape and durable state strings are unchanged.
type (
	CompactionPhase        = PreparedStoreUpgradePhase
	CompactionResourcePlan = PreparedStoreUpgradeResourcePlan
	CompactionOrientation  = PreparedStoreUpgradeOrientation
	CompactionObservation  = PreparedStoreUpgradeObservation
	CompactionAction       = PreparedStoreUpgradeAction
	CompactionRun          = PreparedStoreUpgradeRun
)

const (
	CompactionPlanned               = PreparedStoreUpgradePlanned
	CompactionCopyIntent            = PreparedStoreUpgradeCopyIntent
	CompactionCopyRetryIntent       = PreparedStoreUpgradeCopyRetryIntent
	CompactionCandidatePrepared     = PreparedStoreUpgradeCandidatePrepared
	CompactionCopyComplete          = PreparedStoreUpgradeCopyComplete
	CompactionCandidateSyncIntent   = PreparedStoreUpgradeCandidateSyncIntent
	CompactionCandidateSynced       = PreparedStoreUpgradeCandidateSynced
	CompactionScrubInProgress       = PreparedStoreUpgradeScrubInProgress
	CompactionCandidateVerified     = PreparedStoreUpgradeCandidateVerified
	CompactionSwapIntent            = PreparedStoreUpgradeSwapIntent
	CompactionSwapped               = PreparedStoreUpgradeSwapped
	CompactionRollbackPublishIntent = PreparedStoreUpgradeRollbackPublishIntent
	CompactionRollbackReady         = PreparedStoreUpgradeRollbackReady
	CompactionCommitted             = PreparedStoreUpgradeCommitted
	CompactionRollbackSwapIntent    = PreparedStoreUpgradeRollbackSwapIntent
	CompactionRollbackSwapped       = PreparedStoreUpgradeRollbackSwapped
	CompactionRolledBack            = PreparedStoreUpgradeRolledBack
	CompactionAbandoned             = PreparedStoreUpgradeAbandoned
)
