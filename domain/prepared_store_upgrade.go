//nolint:revive // Phase constants form one documented state-machine block.
package domain

import (
	"fmt"
	"time"
)

// PreparedStoreUpgradePhase is a durable point in the replacement protocol.
type PreparedStoreUpgradePhase string

const (
	// PreparedStoreUpgradePlanned starts the durable protocol.
	PreparedStoreUpgradePlanned               PreparedStoreUpgradePhase = "planned"
	PreparedStoreUpgradeCopyIntent            PreparedStoreUpgradePhase = "copy_intent"
	PreparedStoreUpgradeCopyRetryIntent       PreparedStoreUpgradePhase = "copy_retry_intent"
	PreparedStoreUpgradeCandidatePrepared     PreparedStoreUpgradePhase = "candidate_prepared"
	PreparedStoreUpgradeCopyComplete          PreparedStoreUpgradePhase = "copy_complete"
	PreparedStoreUpgradeCandidateSyncIntent   PreparedStoreUpgradePhase = "candidate_sync_intent"
	PreparedStoreUpgradeCandidateSynced       PreparedStoreUpgradePhase = "candidate_synced"
	PreparedStoreUpgradeScrubInProgress       PreparedStoreUpgradePhase = "scrub_in_progress"
	PreparedStoreUpgradeCandidateVerified     PreparedStoreUpgradePhase = "candidate_verified"
	PreparedStoreUpgradeSwapIntent            PreparedStoreUpgradePhase = "swap_intent"
	PreparedStoreUpgradeSwapped               PreparedStoreUpgradePhase = "swapped"
	PreparedStoreUpgradeRollbackPublishIntent PreparedStoreUpgradePhase = "rollback_publish_intent"
	PreparedStoreUpgradeRollbackReady         PreparedStoreUpgradePhase = "rollback_ready"
	PreparedStoreUpgradeCommitted             PreparedStoreUpgradePhase = "committed"
	PreparedStoreUpgradeRollbackSwapIntent    PreparedStoreUpgradePhase = "rollback_swap_intent"
	PreparedStoreUpgradeRollbackSwapped       PreparedStoreUpgradePhase = "rollback_swapped"
	PreparedStoreUpgradeRolledBack            PreparedStoreUpgradePhase = "rolled_back"
	// PreparedStoreUpgradeAbandoned is a terminal pre-publication exit.
	// The source inode was never swapped; the candidate may be removed.
	PreparedStoreUpgradeAbandoned PreparedStoreUpgradePhase = "abandoned"
)

// StoreFileIdentity fences every destructive filesystem action.
type StoreFileIdentity struct {
	Device  uint64 `json:"device"`
	Inode   uint64 `json:"inode"`
	Size    int64  `json:"size"`
	ModUnix int64  `json:"mod_unix_nano"`
	Mode    uint32 `json:"mode,omitempty"`
	Links   uint64 `json:"link_count,omitempty"`
}

// SameInode compares the stable filesystem object identity, excluding mutable size/mtime.
func (i StoreFileIdentity) SameInode(other StoreFileIdentity) bool {
	return i.Device == other.Device && i.Inode == other.Inode && i.Device != 0 && i.Inode != 0
}

// PreparedStoreUpgradeResourcePlan records the resources and capabilities authorized by plan.
type PreparedStoreUpgradeResourcePlan struct {
	RequiredBytes      uint64 `json:"required_bytes"`
	DestinationBytes   uint64 `json:"destination_bytes"`
	TemporaryBytes     uint64 `json:"temporary_bytes"`
	SafetyMarginBytes  uint64 `json:"safety_margin_bytes"`
	AvailableBytes     uint64 `json:"available_bytes"`
	FilesystemDevice   uint64 `json:"filesystem_device"`
	LeaseCapability    bool   `json:"lease_capability"`
	ExchangeCapability bool   `json:"exchange_capability"`
}

// PreparedStoreUpgradeOperation selects one statically composed candidate recipe.
// The empty value is accepted only while decoding legacy compaction journals.
type PreparedStoreUpgradeOperation string

const (
	PreparedStoreUpgradeOperationCompaction              PreparedStoreUpgradeOperation = "compaction"
	PreparedStoreUpgradeOperationOfflineMigrationUpgrade PreparedStoreUpgradeOperation = "offline_migration_upgrade"
)

// Known reports whether the journal operation can be dispatched safely.
func (o PreparedStoreUpgradeOperation) Known() bool {
	return o == PreparedStoreUpgradeOperationCompaction || o == PreparedStoreUpgradeOperationOfflineMigrationUpgrade
}

// PreparedStoreUpgradeBudget bounds preparation and the publication lease.
type PreparedStoreUpgradeBudget struct {
	WallTimeLimit      time.Duration `json:"wall_time_limit"`
	PublishLockLimit   time.Duration `json:"publish_lock_limit"`
	OwnedDiskByteLimit uint64        `json:"owned_disk_byte_limit"`
	WALByteLimit       uint64        `json:"wal_byte_limit"`
	TemporaryByteLimit uint64        `json:"temporary_byte_limit,omitempty"`
	SafetyMarginBytes  uint64        `json:"safety_margin_bytes"`
}

// CanonicalEventAuditEvidence is body-free proof of canonical row membership.
type CanonicalEventAuditEvidence struct {
	EventCount uint64 `json:"event_count"`
	AuditCount uint64 `json:"audit_count"`
	Digest     string `json:"digest"`
}

// PreparedCandidateEvidence authorizes publication after immutable verification.
type PreparedCandidateEvidence struct {
	SourceDigest       string                      `json:"source_digest,omitempty"`
	CandidateDigest    string                      `json:"candidate_digest,omitempty"`
	MigrationSetDigest string                      `json:"migration_set_digest"`
	SchemaDigest       string                      `json:"schema_digest"`
	Canonical          CanonicalEventAuditEvidence `json:"canonical"`
	PeakOwnedBytes     uint64                      `json:"peak_owned_bytes"`
	PeakWALBytes       uint64                      `json:"peak_wal_bytes"`
	BuildMilliseconds  int64                       `json:"build_milliseconds"`
}

// PreparedStoreUpgradeOrientation describes the uniquely observed placement of fenced inodes.
type PreparedStoreUpgradeOrientation string

const (
	OrientationSourceOriginal PreparedStoreUpgradeOrientation = "source_original"
	OrientationCandidateReady PreparedStoreUpgradeOrientation = "candidate_ready"
	OrientationSwapped        PreparedStoreUpgradeOrientation = "swapped"
	OrientationRollbackReady  PreparedStoreUpgradeOrientation = "rollback_ready"
	OrientationRolledBack     PreparedStoreUpgradeOrientation = "rolled_back"
)

// PreparedStoreUpgradeObservation is a complete filesystem snapshot used by aggregate decisions.
type PreparedStoreUpgradeObservation struct {
	Orientation        PreparedStoreUpgradeOrientation
	Source             StoreFileIdentity
	Candidate          StoreFileIdentity
	Rollback           StoreFileIdentity
	CandidateExists    bool
	RollbackExists     bool
	CandidateCondition CandidateCondition
}

// CandidateCondition classifies an observed run-owned candidate.
type CandidateCondition string

const (
	CandidateConditionUnknown         CandidateCondition = "unknown"
	CandidateConditionComplete        CandidateCondition = "complete"
	CandidateConditionOwnedIncomplete CandidateCondition = "owned_incomplete"
)

// PreparedStoreUpgradeAction is an aggregate-issued recovery action.
type PreparedStoreUpgradeAction string

const (
	ActionRecheckPlan                 PreparedStoreUpgradeAction = "recheck_plan"
	ActionPrepareCandidate            PreparedStoreUpgradeAction = "prepare_candidate"
	ActionBuildCandidate              PreparedStoreUpgradeAction = "build_candidate"
	ActionRemoveOwnedPartialCandidate PreparedStoreUpgradeAction = "remove_owned_partial_candidate"
	ActionRecordCopyRetryIntent       PreparedStoreUpgradeAction = "record_copy_retry_intent"
	ActionRecordCopyComplete          PreparedStoreUpgradeAction = "record_copy_complete"
	ActionRecordSyncIntent            PreparedStoreUpgradeAction = "record_sync_intent"
	ActionSyncCandidate               PreparedStoreUpgradeAction = "sync_candidate"
	ActionRecordScrubInProgress       PreparedStoreUpgradeAction = "record_scrub_in_progress"
	ActionVerifyCandidate             PreparedStoreUpgradeAction = "verify_candidate"
	ActionRecordSwapIntent            PreparedStoreUpgradeAction = "record_swap_intent"
	ActionExchange                    PreparedStoreUpgradeAction = "exchange"
	ActionRecordSwapped               PreparedStoreUpgradeAction = "record_swapped"
	ActionRecordPublishIntent         PreparedStoreUpgradeAction = "record_publish_intent"
	ActionPublishRollback             PreparedStoreUpgradeAction = "publish_rollback"
	ActionRecordRollbackReady         PreparedStoreUpgradeAction = "record_rollback_ready"
	ActionCommit                      PreparedStoreUpgradeAction = "commit"
	ActionRollbackExchange            PreparedStoreUpgradeAction = "rollback_exchange"
	ActionSyncRecoveredOrientation    PreparedStoreUpgradeAction = "sync_recovered_orientation"
	ActionRecordRollbackSwapped       PreparedStoreUpgradeAction = "record_rollback_swapped"
	ActionRecordRolledBack            PreparedStoreUpgradeAction = "record_rolled_back"
)

// NextAction is the aggregate decision for the next normal apply step.
func (r PreparedStoreUpgradeRun) NextAction() (PreparedStoreUpgradeAction, error) {
	switch r.Phase {
	case PreparedStoreUpgradePlanned:
		return ActionRecheckPlan, nil
	case PreparedStoreUpgradeCopyIntent, PreparedStoreUpgradeCopyRetryIntent:
		return ActionPrepareCandidate, nil
	case PreparedStoreUpgradeCandidatePrepared:
		return ActionBuildCandidate, nil
	case PreparedStoreUpgradeCopyComplete:
		return ActionRecordSyncIntent, nil
	case PreparedStoreUpgradeCandidateSyncIntent:
		return ActionSyncCandidate, nil
	case PreparedStoreUpgradeCandidateSynced:
		return ActionRecordScrubInProgress, nil
	case PreparedStoreUpgradeScrubInProgress:
		return ActionVerifyCandidate, nil
	case PreparedStoreUpgradeCandidateVerified:
		return ActionRecordSwapIntent, nil
	case PreparedStoreUpgradeSwapIntent:
		return ActionExchange, nil
	case PreparedStoreUpgradeSwapped:
		return ActionRecordPublishIntent, nil
	case PreparedStoreUpgradeRollbackPublishIntent:
		return ActionPublishRollback, nil
	case PreparedStoreUpgradeRollbackReady:
		return ActionCommit, nil
	case PreparedStoreUpgradeCommitted:
		return "", nil
	default:
		return "", fmt.Errorf("no normal apply action in phase %q", r.Phase)
	}
}

// RecoveryActions rejects unknown placement/phase combinations and returns ordered decisions.
func (r PreparedStoreUpgradeRun) RecoveryActions(observation PreparedStoreUpgradeObservation) ([]PreparedStoreUpgradeAction, error) {
	switch observation.Orientation {
	case OrientationSwapped:
		switch r.Phase {
		case PreparedStoreUpgradeCandidateVerified:
			return []PreparedStoreUpgradeAction{ActionRecordSwapIntent, ActionSyncRecoveredOrientation, ActionRecordSwapped}, nil
		case PreparedStoreUpgradeSwapIntent:
			return []PreparedStoreUpgradeAction{ActionSyncRecoveredOrientation, ActionRecordSwapped}, nil
		case PreparedStoreUpgradeSwapped:
			return nil, nil
		case PreparedStoreUpgradeRollbackPublishIntent:
			return nil, nil
		}
	case OrientationRollbackReady:
		switch r.Phase {
		case PreparedStoreUpgradeSwapped:
			return []PreparedStoreUpgradeAction{ActionRecordPublishIntent, ActionSyncRecoveredOrientation, ActionRecordRollbackReady}, nil
		case PreparedStoreUpgradeRollbackPublishIntent:
			return []PreparedStoreUpgradeAction{ActionSyncRecoveredOrientation, ActionRecordRollbackReady}, nil
		case PreparedStoreUpgradeRollbackReady, PreparedStoreUpgradeCommitted:
			return nil, nil
		case PreparedStoreUpgradeRollbackSwapIntent:
			return []PreparedStoreUpgradeAction{ActionRollbackExchange, ActionRecordRollbackSwapped, ActionRecordRolledBack}, nil
		}
	case OrientationRolledBack:
		switch r.Phase {
		case PreparedStoreUpgradeRollbackSwapIntent:
			return []PreparedStoreUpgradeAction{ActionSyncRecoveredOrientation, ActionRecordRollbackSwapped, ActionRecordRolledBack}, nil
		case PreparedStoreUpgradeRollbackSwapped:
			return []PreparedStoreUpgradeAction{ActionRecordRolledBack}, nil
		case PreparedStoreUpgradeRolledBack:
			return nil, nil
		}
	case OrientationSourceOriginal:
		switch r.Phase {
		case PreparedStoreUpgradePlanned, PreparedStoreUpgradeCopyIntent, PreparedStoreUpgradeCopyRetryIntent:
			return nil, nil
		}
	case OrientationCandidateReady:
		switch r.Phase {
		case PreparedStoreUpgradeCopyIntent:
			return nil, fmt.Errorf("candidate appeared before prepared identity was durable")
		case PreparedStoreUpgradeCopyRetryIntent:
			if !observation.Candidate.SameInode(r.PreparedCandidateIdentity) {
				return nil, fmt.Errorf("retry candidate identity changed")
			}
			return []PreparedStoreUpgradeAction{ActionRemoveOwnedPartialCandidate}, nil
		case PreparedStoreUpgradeCandidatePrepared:
			if !observation.Candidate.SameInode(r.PreparedCandidateIdentity) {
				return nil, fmt.Errorf("prepared candidate identity changed")
			}
			switch observation.CandidateCondition {
			case CandidateConditionComplete:
				return []PreparedStoreUpgradeAction{ActionRecordCopyComplete}, nil
			case CandidateConditionOwnedIncomplete:
				return []PreparedStoreUpgradeAction{ActionRecordCopyRetryIntent, ActionRemoveOwnedPartialCandidate}, nil
			default:
				return nil, fmt.Errorf("candidate ownership/completeness is unknown")
			}
		case PreparedStoreUpgradeCopyComplete, PreparedStoreUpgradeCandidateSyncIntent, PreparedStoreUpgradeCandidateSynced, PreparedStoreUpgradeScrubInProgress, PreparedStoreUpgradeCandidateVerified:
			return nil, nil
		case PreparedStoreUpgradeSwapIntent:
			return []PreparedStoreUpgradeAction{ActionExchange, ActionRecordSwapped}, nil
		}
	}
	return nil, fmt.Errorf("unsafe prepared upgrade observation %q in phase %q", observation.Orientation, r.Phase)
}

// PreparedStoreUpgradeRun is the domain record persisted outside the source database.
type PreparedStoreUpgradeRun struct {
	ID                           string                           `json:"id"`
	SourcePath                   string                           `json:"source_path"`
	CandidatePath                string                           `json:"candidate_path"`
	RollbackPath                 string                           `json:"rollback_path"`
	Phase                        PreparedStoreUpgradePhase        `json:"phase"`
	SourceIdentity               StoreFileIdentity                `json:"source_identity"`
	Candidate                    StoreFileIdentity                `json:"candidate_identity,omitempty"`
	PreparedCandidateIdentity    StoreFileIdentity                `json:"prepared_candidate_identity,omitempty"`
	PreparedAttempt              uint64                           `json:"prepared_attempt"`
	Resources                    PreparedStoreUpgradeResourcePlan `json:"resources"`
	Operation                    PreparedStoreUpgradeOperation    `json:"operation,omitempty"`
	ConsumerBinding              string                           `json:"consumer_binding,omitempty"`
	PlanDigest                   string                           `json:"plan_digest,omitempty"`
	SourceDigest                 string                           `json:"source_digest,omitempty"`
	BoundDropApproval            *BoundDropApproval               `json:"bound_drop_approval,omitempty"`
	UnavailableRetentionApproval *UnavailableRetentionApproval    `json:"unavailable_retention_approval,omitempty"`
	Budget                       PreparedStoreUpgradeBudget       `json:"budget,omitempty"`
	Evidence                     PreparedCandidateEvidence        `json:"evidence,omitempty"`
	CreatedAt                    time.Time                        `json:"created_at"`
	UpdatedAt                    time.Time                        `json:"updated_at"`
}

var preparedStoreUpgradeTransitions = map[PreparedStoreUpgradePhase][]PreparedStoreUpgradePhase{
	PreparedStoreUpgradePlanned:               {PreparedStoreUpgradeCopyIntent, PreparedStoreUpgradeAbandoned},
	PreparedStoreUpgradeCopyIntent:            {PreparedStoreUpgradeCandidatePrepared, PreparedStoreUpgradeAbandoned},
	PreparedStoreUpgradeCopyRetryIntent:       {PreparedStoreUpgradeCandidatePrepared, PreparedStoreUpgradeAbandoned},
	PreparedStoreUpgradeCandidatePrepared:     {PreparedStoreUpgradeCopyComplete, PreparedStoreUpgradeCopyRetryIntent, PreparedStoreUpgradeAbandoned},
	PreparedStoreUpgradeCopyComplete:          {PreparedStoreUpgradeCandidateSyncIntent},
	PreparedStoreUpgradeCandidateSyncIntent:   {PreparedStoreUpgradeCandidateSynced},
	PreparedStoreUpgradeCandidateSynced:       {PreparedStoreUpgradeScrubInProgress},
	PreparedStoreUpgradeScrubInProgress:       {PreparedStoreUpgradeCandidateVerified},
	PreparedStoreUpgradeCandidateVerified:     {PreparedStoreUpgradeSwapIntent},
	PreparedStoreUpgradeSwapIntent:            {PreparedStoreUpgradeSwapped},
	PreparedStoreUpgradeSwapped:               {PreparedStoreUpgradeRollbackPublishIntent},
	PreparedStoreUpgradeRollbackPublishIntent: {PreparedStoreUpgradeRollbackReady},
	PreparedStoreUpgradeRollbackReady:         {PreparedStoreUpgradeCommitted, PreparedStoreUpgradeRollbackSwapIntent},
	PreparedStoreUpgradeCommitted:             {PreparedStoreUpgradeRollbackSwapIntent},
	PreparedStoreUpgradeRollbackSwapIntent:    {PreparedStoreUpgradeRollbackSwapped},
	PreparedStoreUpgradeRollbackSwapped:       {PreparedStoreUpgradeRolledBack},
}

// IsTerminal reports whether the journal no longer requires resume.
func (p PreparedStoreUpgradePhase) IsTerminal() bool {
	switch p {
	case PreparedStoreUpgradeCommitted, PreparedStoreUpgradeRolledBack, PreparedStoreUpgradeAbandoned:
		return true
	default:
		return false
	}
}

// IsPrePublication is the window where abandoning is unconditionally safe:
// the rollback inode was never created and the source inode was never swapped.
func (p PreparedStoreUpgradePhase) IsPrePublication() bool {
	switch p {
	case PreparedStoreUpgradePlanned, PreparedStoreUpgradeCopyIntent, PreparedStoreUpgradeCopyRetryIntent, PreparedStoreUpgradeCandidatePrepared:
		return true
	default:
		return false
	}
}

// ShouldAbandonForStaleSource is true when a pre-publication journal is fenced
// to a source identity that no longer matches the live file (typically mtime
// after a later writer). Publication phases must not take this exit.
func (r PreparedStoreUpgradeRun) ShouldAbandonForStaleSource(current StoreFileIdentity) bool {
	return r.Phase.IsPrePublication() && current != (StoreFileIdentity{}) && current != r.SourceIdentity
}

// Advance rejects skipped or backward protocol transitions.
func (r PreparedStoreUpgradeRun) Advance(next PreparedStoreUpgradePhase, now time.Time) (PreparedStoreUpgradeRun, error) {
	if r.Phase == PreparedStoreUpgradeCommitted && next == PreparedStoreUpgradeRollbackSwapIntent && r.Operation == PreparedStoreUpgradeOperationOfflineMigrationUpgrade {
		return r, fmt.Errorf("upgrade run refuses post-commit rollback: retained file is a forensic backup, not an interchangeable rollback target")
	}
	for _, allowed := range preparedStoreUpgradeTransitions[r.Phase] {
		if next == allowed {
			r.Phase, r.UpdatedAt = next, now.UTC()
			return r, nil
		}
	}
	return r, fmt.Errorf("invalid prepared upgrade transition %q -> %q", r.Phase, next)
}
