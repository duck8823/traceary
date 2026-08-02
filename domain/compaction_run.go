//nolint:revive // Phase constants form one documented state-machine block.
package domain

import (
	"fmt"
	"time"
)

// CompactionPhase is a durable point in the replacement protocol.
type CompactionPhase string

const (
	// CompactionPlanned starts the durable protocol.
	CompactionPlanned               CompactionPhase = "planned"
	CompactionCopyIntent            CompactionPhase = "copy_intent"
	CompactionCopyRetryIntent       CompactionPhase = "copy_retry_intent"
	CompactionCandidatePrepared     CompactionPhase = "candidate_prepared"
	CompactionCopyComplete          CompactionPhase = "copy_complete"
	CompactionCandidateSyncIntent   CompactionPhase = "candidate_sync_intent"
	CompactionCandidateSynced       CompactionPhase = "candidate_synced"
	CompactionScrubInProgress       CompactionPhase = "scrub_in_progress"
	CompactionCandidateVerified     CompactionPhase = "candidate_verified"
	CompactionSwapIntent            CompactionPhase = "swap_intent"
	CompactionSwapped               CompactionPhase = "swapped"
	CompactionRollbackPublishIntent CompactionPhase = "rollback_publish_intent"
	CompactionRollbackReady         CompactionPhase = "rollback_ready"
	CompactionCommitted             CompactionPhase = "committed"
	CompactionRollbackSwapIntent    CompactionPhase = "rollback_swap_intent"
	CompactionRollbackSwapped       CompactionPhase = "rollback_swapped"
	CompactionRolledBack            CompactionPhase = "rolled_back"
)

// StoreFileIdentity fences every destructive filesystem action.
type StoreFileIdentity struct {
	Device  uint64 `json:"device"`
	Inode   uint64 `json:"inode"`
	Size    int64  `json:"size"`
	ModUnix int64  `json:"mod_unix_nano"`
}

// SameInode compares the stable filesystem object identity, excluding mutable size/mtime.
func (i StoreFileIdentity) SameInode(other StoreFileIdentity) bool {
	return i.Device == other.Device && i.Inode == other.Inode && i.Device != 0 && i.Inode != 0
}

// CompactionResourcePlan records the resources and capabilities authorized by plan.
type CompactionResourcePlan struct {
	RequiredBytes      uint64 `json:"required_bytes"`
	DestinationBytes   uint64 `json:"destination_bytes"`
	TemporaryBytes     uint64 `json:"temporary_bytes"`
	SafetyMarginBytes  uint64 `json:"safety_margin_bytes"`
	AvailableBytes     uint64 `json:"available_bytes"`
	FilesystemDevice   uint64 `json:"filesystem_device"`
	LeaseCapability    bool   `json:"lease_capability"`
	ExchangeCapability bool   `json:"exchange_capability"`
}

// CompactionOrientation describes the uniquely observed placement of fenced inodes.
type CompactionOrientation string

const (
	OrientationSourceOriginal CompactionOrientation = "source_original"
	OrientationCandidateReady CompactionOrientation = "candidate_ready"
	OrientationSwapped        CompactionOrientation = "swapped"
	OrientationRollbackReady  CompactionOrientation = "rollback_ready"
	OrientationRolledBack     CompactionOrientation = "rolled_back"
)

// CompactionObservation is a complete filesystem snapshot used by aggregate decisions.
type CompactionObservation struct {
	Orientation        CompactionOrientation
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

// CompactionAction is an aggregate-issued recovery action.
type CompactionAction string

const (
	ActionRecheckPlan                 CompactionAction = "recheck_plan"
	ActionPrepareCandidate            CompactionAction = "prepare_candidate"
	ActionBuildCandidate              CompactionAction = "build_candidate"
	ActionRemoveOwnedPartialCandidate CompactionAction = "remove_owned_partial_candidate"
	ActionRecordCopyRetryIntent       CompactionAction = "record_copy_retry_intent"
	ActionRecordCopyComplete          CompactionAction = "record_copy_complete"
	ActionRecordSyncIntent            CompactionAction = "record_sync_intent"
	ActionSyncCandidate               CompactionAction = "sync_candidate"
	ActionRecordScrubInProgress       CompactionAction = "record_scrub_in_progress"
	ActionVerifyCandidate             CompactionAction = "verify_candidate"
	ActionRecordSwapIntent            CompactionAction = "record_swap_intent"
	ActionExchange                    CompactionAction = "exchange"
	ActionRecordSwapped               CompactionAction = "record_swapped"
	ActionRecordPublishIntent         CompactionAction = "record_publish_intent"
	ActionPublishRollback             CompactionAction = "publish_rollback"
	ActionRecordRollbackReady         CompactionAction = "record_rollback_ready"
	ActionCommit                      CompactionAction = "commit"
	ActionRollbackExchange            CompactionAction = "rollback_exchange"
	ActionRecordRollbackSwapped       CompactionAction = "record_rollback_swapped"
	ActionRecordRolledBack            CompactionAction = "record_rolled_back"
)

// NextAction is the aggregate decision for the next normal apply step.
func (r CompactionRun) NextAction() (CompactionAction, error) {
	switch r.Phase {
	case CompactionPlanned:
		return ActionRecheckPlan, nil
	case CompactionCopyIntent, CompactionCopyRetryIntent:
		return ActionPrepareCandidate, nil
	case CompactionCandidatePrepared:
		return ActionBuildCandidate, nil
	case CompactionCopyComplete:
		return ActionRecordSyncIntent, nil
	case CompactionCandidateSyncIntent:
		return ActionSyncCandidate, nil
	case CompactionCandidateSynced:
		return ActionRecordScrubInProgress, nil
	case CompactionScrubInProgress:
		return ActionVerifyCandidate, nil
	case CompactionCandidateVerified:
		return ActionRecordSwapIntent, nil
	case CompactionSwapIntent:
		return ActionExchange, nil
	case CompactionSwapped:
		return ActionRecordPublishIntent, nil
	case CompactionRollbackPublishIntent:
		return ActionPublishRollback, nil
	case CompactionRollbackReady:
		return ActionCommit, nil
	case CompactionCommitted:
		return "", nil
	default:
		return "", fmt.Errorf("no normal apply action in phase %q", r.Phase)
	}
}

// RecoveryActions rejects unknown placement/phase combinations and returns ordered decisions.
func (r CompactionRun) RecoveryActions(observation CompactionObservation) ([]CompactionAction, error) {
	switch observation.Orientation {
	case OrientationSwapped:
		switch r.Phase {
		case CompactionCandidateVerified:
			return []CompactionAction{ActionRecordSwapIntent, ActionRecordSwapped}, nil
		case CompactionSwapIntent:
			return []CompactionAction{ActionRecordSwapped}, nil
		case CompactionSwapped:
			return nil, nil
		case CompactionRollbackPublishIntent:
			return nil, nil
		}
	case OrientationRollbackReady:
		switch r.Phase {
		case CompactionSwapped:
			return []CompactionAction{ActionRecordPublishIntent, ActionRecordRollbackReady}, nil
		case CompactionRollbackPublishIntent:
			return []CompactionAction{ActionRecordRollbackReady}, nil
		case CompactionRollbackReady, CompactionCommitted:
			return nil, nil
		case CompactionRollbackSwapIntent:
			return []CompactionAction{ActionRollbackExchange, ActionRecordRollbackSwapped, ActionRecordRolledBack}, nil
		}
	case OrientationRolledBack:
		switch r.Phase {
		case CompactionRollbackSwapIntent:
			return []CompactionAction{ActionRecordRollbackSwapped, ActionRecordRolledBack}, nil
		case CompactionRollbackSwapped:
			return []CompactionAction{ActionRecordRolledBack}, nil
		case CompactionRolledBack:
			return nil, nil
		}
	case OrientationSourceOriginal:
		switch r.Phase {
		case CompactionPlanned, CompactionCopyIntent, CompactionCopyRetryIntent:
			return nil, nil
		}
	case OrientationCandidateReady:
		switch r.Phase {
		case CompactionCopyIntent, CompactionCopyRetryIntent:
			return nil, fmt.Errorf("candidate appeared before prepared identity was durable")
		case CompactionCandidatePrepared:
			if !observation.Candidate.SameInode(r.PreparedCandidateIdentity) {
				return nil, fmt.Errorf("prepared candidate identity changed")
			}
			switch observation.CandidateCondition {
			case CandidateConditionComplete:
				return []CompactionAction{ActionRecordCopyComplete}, nil
			case CandidateConditionOwnedIncomplete:
				return []CompactionAction{ActionRemoveOwnedPartialCandidate, ActionRecordCopyRetryIntent}, nil
			default:
				return nil, fmt.Errorf("candidate ownership/completeness is unknown")
			}
		case CompactionCopyComplete, CompactionCandidateSyncIntent, CompactionCandidateSynced, CompactionScrubInProgress, CompactionCandidateVerified:
			return nil, nil
		case CompactionSwapIntent:
			return []CompactionAction{ActionExchange, ActionRecordSwapped}, nil
		}
	}
	return nil, fmt.Errorf("unsafe compaction observation %q in phase %q", observation.Orientation, r.Phase)
}

// CompactionRun is the domain record persisted outside the source database.
type CompactionRun struct {
	ID                        string                 `json:"id"`
	SourcePath                string                 `json:"source_path"`
	CandidatePath             string                 `json:"candidate_path"`
	RollbackPath              string                 `json:"rollback_path"`
	Phase                     CompactionPhase        `json:"phase"`
	SourceIdentity            StoreFileIdentity      `json:"source_identity"`
	Candidate                 StoreFileIdentity      `json:"candidate_identity,omitempty"`
	PreparedCandidateIdentity StoreFileIdentity      `json:"prepared_candidate_identity,omitempty"`
	PreparedAttempt           uint64                 `json:"prepared_attempt"`
	Resources                 CompactionResourcePlan `json:"resources"`
	CreatedAt                 time.Time              `json:"created_at"`
	UpdatedAt                 time.Time              `json:"updated_at"`
}

var compactionTransitions = map[CompactionPhase][]CompactionPhase{
	CompactionPlanned:               {CompactionCopyIntent},
	CompactionCopyIntent:            {CompactionCandidatePrepared},
	CompactionCopyRetryIntent:       {CompactionCandidatePrepared},
	CompactionCandidatePrepared:     {CompactionCopyComplete, CompactionCopyRetryIntent},
	CompactionCopyComplete:          {CompactionCandidateSyncIntent},
	CompactionCandidateSyncIntent:   {CompactionCandidateSynced},
	CompactionCandidateSynced:       {CompactionScrubInProgress},
	CompactionScrubInProgress:       {CompactionCandidateVerified},
	CompactionCandidateVerified:     {CompactionSwapIntent},
	CompactionSwapIntent:            {CompactionSwapped},
	CompactionSwapped:               {CompactionRollbackPublishIntent, CompactionRollbackSwapIntent},
	CompactionRollbackPublishIntent: {CompactionRollbackReady},
	CompactionRollbackReady:         {CompactionCommitted, CompactionRollbackSwapIntent},
	CompactionCommitted:             {CompactionRollbackSwapIntent},
	CompactionRollbackSwapIntent:    {CompactionRollbackSwapped},
	CompactionRollbackSwapped:       {CompactionRolledBack},
}

// Advance rejects skipped or backward protocol transitions.
func (r CompactionRun) Advance(next CompactionPhase, now time.Time) (CompactionRun, error) {
	for _, allowed := range compactionTransitions[r.Phase] {
		if next == allowed {
			r.Phase, r.UpdatedAt = next, now.UTC()
			return r, nil
		}
	}
	return r, fmt.Errorf("invalid compaction transition %q -> %q", r.Phase, next)
}
