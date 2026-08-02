package domain

import (
	"fmt"
	"time"
)

// CompactionPhase is a durable point in the replacement protocol.
type CompactionPhase string

const (
	CompactionPlanned               CompactionPhase = "planned"
	CompactionCopyIntent            CompactionPhase = "copy_intent"
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

// CompactionRun is the domain record persisted outside the source database.
type CompactionRun struct {
	ID             string            `json:"id"`
	SourcePath     string            `json:"source_path"`
	CandidatePath  string            `json:"candidate_path"`
	RollbackPath   string            `json:"rollback_path"`
	Phase          CompactionPhase   `json:"phase"`
	SourceIdentity StoreFileIdentity `json:"source_identity"`
	Candidate      StoreFileIdentity `json:"candidate_identity,omitempty"`
	CreatedAt      time.Time         `json:"created_at"`
	UpdatedAt      time.Time         `json:"updated_at"`
}

var compactionTransitions = map[CompactionPhase][]CompactionPhase{
	CompactionPlanned:               {CompactionCopyIntent},
	CompactionCopyIntent:            {CompactionCopyComplete},
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
