//nolint:wrapcheck // This orchestrator preserves typed adapter failures at the CLI boundary.
package usecase

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"path/filepath"
	"time"

	"github.com/duck8823/traceary/application"
	"github.com/duck8823/traceary/domain"
)

type storeCompactionUsecase struct {
	journal       application.StoreCompactionJournal
	builder       application.StoreCompactionBuilder
	files         application.StoreCompactionFiles
	lease         application.StoreCompactionLease
	now           func() time.Time
	expectedStore string
}

// NewStoreCompactionUsecase composes the dedicated compaction protocol.
func NewStoreCompactionUsecase(expectedStore string, j application.StoreCompactionJournal, b application.StoreCompactionBuilder, f application.StoreCompactionFiles, l application.StoreCompactionLease) application.StoreCompactionUsecase {
	return &storeCompactionUsecase{journal: j, builder: b, files: f, lease: l, now: time.Now, expectedStore: filepath.Clean(expectedStore)}
}

func (u *storeCompactionUsecase) Plan(ctx context.Context, source string) (domain.CompactionRun, error) {
	if filepath.Clean(source) != u.expectedStore {
		return domain.CompactionRun{}, fmt.Errorf("compaction store binding mismatch")
	}
	idBytes := make([]byte, 16)
	if _, err := rand.Read(idBytes); err != nil {
		return domain.CompactionRun{}, fmt.Errorf("create compaction run id: %w", err)
	}
	id := hex.EncodeToString(idBytes)
	now := u.now().UTC()
	run := domain.CompactionRun{ID: id, SourcePath: source, CandidatePath: source + ".compact-" + id, RollbackPath: source + ".rollback-" + id, Phase: domain.CompactionPlanned, CreatedAt: now, UpdatedAt: now}
	planned, err := u.files.Plan(ctx, run)
	if err != nil {
		return domain.CompactionRun{}, err
	}
	if err := u.journal.Create(ctx, planned); err != nil {
		return domain.CompactionRun{}, err
	}
	return planned, nil
}

func (u *storeCompactionUsecase) Apply(ctx context.Context, id string) (domain.CompactionRun, error) {
	release, err := u.lease.AcquireExclusive(ctx, u.expectedStore)
	if err != nil {
		return domain.CompactionRun{}, err
	}
	defer release()
	run, err := u.journal.Load(ctx, id)
	if err != nil {
		return run, err
	}
	if err := u.validateBinding(run); err != nil {
		return run, err
	}
	return u.applyLeased(ctx, run)
}

// guardPublication refuses to publish a candidate that still carries the
// retired migration-032 search index family.
//
// It sits immediately before the forward exchange rather than at the entry to
// a phase loop. Recovery reaches ActionExchange from resumeLeased without
// passing through applyLeased at all, so a run journaled at swap_intent by an
// older binary would otherwise be published by the very path that exists to
// finish interrupted runs — and neither Plan nor Build is revisited there.
// Anchoring on the action makes every route to the exchange carry the check.
//
// The rollback exchange is deliberately not guarded. It restores the original
// source, so refusing would strand the store on the candidate it is trying to
// abandon. So is anything past the exchange: the candidate is live by then,
// and the only safe direction is forward.
func (u *storeCompactionUsecase) guardPublication(ctx context.Context, run domain.CompactionRun) error {
	return u.files.RejectRetiredSearchIndex(ctx, run)
}

func (u *storeCompactionUsecase) applyLeased(ctx context.Context, run domain.CompactionRun) (domain.CompactionRun, error) {
	var err error
	for run.Phase != domain.CompactionCommitted {
		action, decisionErr := run.NextAction()
		if decisionErr != nil {
			return run, decisionErr
		}
		switch action {
		case domain.ActionRecheckPlan:
			err = u.files.Recheck(ctx, run)
			if err == nil {
				run, err = u.advance(ctx, run, domain.CompactionCopyIntent)
			}
		case domain.ActionPrepareCandidate:
			identity, prepareErr := u.files.PrepareCandidate(ctx, run)
			if prepareErr != nil {
				return run, prepareErr
			}
			run.PreparedCandidateIdentity = identity
			run.PreparedAttempt++
			run, err = u.advance(ctx, run, domain.CompactionCandidatePrepared)
		case domain.ActionBuildCandidate:
			err = u.builder.Build(ctx, run.SourcePath, run.CandidatePath)
			if err == nil {
				run, err = u.advance(ctx, run, domain.CompactionCopyComplete)
			}
		case domain.ActionRecordSyncIntent:
			run, err = u.advance(ctx, run, domain.CompactionCandidateSyncIntent)
		case domain.ActionSyncCandidate:
			err = u.builder.Sync(ctx, run.CandidatePath)
			if err == nil {
				run, err = u.advance(ctx, run, domain.CompactionCandidateSynced)
			}
		case domain.ActionRecordScrubInProgress:
			run, err = u.advance(ctx, run, domain.CompactionScrubInProgress)
		case domain.ActionVerifyCandidate:
			err = u.builder.VerifyPair(ctx, run.SourcePath, run.CandidatePath)
			if err == nil {
				run, err = u.files.FenceCandidate(ctx, run)
			}
			if err == nil {
				run, err = u.advance(ctx, run, domain.CompactionCandidateVerified)
			}
		case domain.ActionRecordSwapIntent:
			run, err = u.advance(ctx, run, domain.CompactionSwapIntent)
		case domain.ActionExchange:
			err = u.guardPublication(ctx, run)
			if err == nil {
				err = u.files.Exchange(ctx, run)
			}
			if err == nil {
				run, err = u.advance(ctx, run, domain.CompactionSwapped)
			}
		case domain.ActionRecordPublishIntent:
			run, err = u.advance(ctx, run, domain.CompactionRollbackPublishIntent)
		case domain.ActionPublishRollback:
			err = u.files.PublishRollback(ctx, run)
			if err == nil {
				run, err = u.advance(ctx, run, domain.CompactionRollbackReady)
			}
		case domain.ActionCommit:
			run, err = u.advance(ctx, run, domain.CompactionCommitted)
		default:
			return run, fmt.Errorf("cannot execute compaction action %q", action)
		}
		if err != nil {
			return run, err
		}
	}
	return run, nil
}

func (u *storeCompactionUsecase) Resume(ctx context.Context, id string) (domain.CompactionRun, error) {
	// Acquire is deliberately performed before journal/orientation inspection;
	// resume then calls the shared leased runner to avoid double acquisition.
	release, err := u.lease.AcquireExclusive(ctx, u.expectedStore)
	if err != nil {
		return domain.CompactionRun{}, err
	}
	defer release()
	initial, err := u.journal.Load(ctx, id)
	if err != nil {
		return initial, err
	}
	if err := u.validateBinding(initial); err != nil {
		return initial, err
	}
	return u.resumeLeased(ctx, initial)
}

func (u *storeCompactionUsecase) resumeLeased(ctx context.Context, run domain.CompactionRun) (domain.CompactionRun, error) {
	observation, err := u.files.Observe(ctx, run)
	if err != nil {
		return run, err
	}
	if run.Phase == domain.CompactionCandidatePrepared && observation.Orientation == domain.OrientationCandidateReady {
		observation.CandidateCondition, err = u.builder.ClassifyCandidate(ctx, run.SourcePath, run.CandidatePath)
		if err != nil {
			return run, err
		}
	}
	actions, err := run.RecoveryActions(observation)
	if err != nil {
		return run, err
	}
	for _, action := range actions {
		if action == domain.ActionSyncRecoveredOrientation {
			if err := u.files.SyncRecoveredOrientation(ctx, run, observation); err != nil {
				return run, err
			}
			continue
		}
		if action == domain.ActionRemoveOwnedPartialCandidate {
			if err := u.files.Recheck(ctx, run); err != nil {
				return run, err
			}
			if err := u.files.RemoveOwnedPartialCandidate(ctx, run, observation); err != nil {
				return run, err
			}
			continue
		}
		if action == domain.ActionRecordCopyRetryIntent {
			run, err = u.advance(ctx, run, domain.CompactionCopyRetryIntent)
			if err != nil {
				return run, err
			}
			continue
		}
		if action == domain.ActionRecordCopyComplete {
			run, err = u.advance(ctx, run, domain.CompactionCopyComplete)
			if err != nil {
				return run, err
			}
			continue
		}
		if action == domain.ActionExchange {
			if err := u.guardPublication(ctx, run); err != nil {
				return run, err
			}
			if err := u.files.Exchange(ctx, run); err != nil {
				return run, err
			}
			continue
		}
		if action == domain.ActionRollbackExchange {
			if err := u.files.Exchange(ctx, run); err != nil {
				return run, err
			}
			continue
		}
		phase, ok := phaseForRecoveryAction(action)
		if !ok {
			return run, fmt.Errorf("unsupported recovery action %q", action)
		}
		run, err = u.advance(ctx, run, phase)
		if err != nil {
			return run, err
		}
	}
	if run.Phase == domain.CompactionRollbackSwapped {
		return u.advance(ctx, run, domain.CompactionRolledBack)
	}
	if run.Phase == domain.CompactionRolledBack {
		return run, nil
	}
	return u.applyLeased(ctx, run)
}

func phaseForRecoveryAction(action domain.CompactionAction) (domain.CompactionPhase, bool) {
	switch action {
	case domain.ActionRecordSwapIntent:
		return domain.CompactionSwapIntent, true
	case domain.ActionRecordSwapped:
		return domain.CompactionSwapped, true
	case domain.ActionRecordPublishIntent:
		return domain.CompactionRollbackPublishIntent, true
	case domain.ActionRecordRollbackReady:
		return domain.CompactionRollbackReady, true
	case domain.ActionRecordRollbackSwapped:
		return domain.CompactionRollbackSwapped, true
	case domain.ActionRecordRolledBack:
		return domain.CompactionRolledBack, true
	default:
		return "", false
	}
}

func (u *storeCompactionUsecase) Status(ctx context.Context, id string) (domain.CompactionRun, error) {
	run, err := u.journal.Load(ctx, id)
	if err != nil {
		return run, err
	}
	if err := u.validateBinding(run); err != nil {
		return run, err
	}
	return run, nil
}

func (u *storeCompactionUsecase) Rollback(ctx context.Context, id string) (domain.CompactionRun, error) {
	release, err := u.lease.AcquireExclusive(ctx, u.expectedStore)
	if err != nil {
		return domain.CompactionRun{}, err
	}
	defer release()
	run, err := u.journal.Load(ctx, id)
	if err != nil {
		return run, err
	}
	if err := u.validateBinding(run); err != nil {
		return run, err
	}
	if run.Phase != domain.CompactionSwapped && run.Phase != domain.CompactionRollbackReady && run.Phase != domain.CompactionCommitted && run.Phase != domain.CompactionRollbackSwapIntent && run.Phase != domain.CompactionRollbackSwapped {
		return run, fmt.Errorf("cannot rollback compaction in phase %q", run.Phase)
	}
	observation, err := u.files.Observe(ctx, run)
	if err != nil {
		return run, err
	}
	if observation.Orientation == domain.OrientationRolledBack {
		if run.Phase == domain.CompactionRollbackSwapIntent {
			run, err = u.advance(ctx, run, domain.CompactionRollbackSwapped)
			if err != nil {
				return run, err
			}
		}
		if run.Phase == domain.CompactionRollbackSwapped {
			return u.advance(ctx, run, domain.CompactionRolledBack)
		}
		return run, fmt.Errorf("rollback observation conflicts with phase")
	}
	// An exchange may have committed immediately before the process stopped.
	// Publish the still-fenced original inode first; rollback is never allowed
	// directly from the transient swapped orientation.
	if observation.Orientation == domain.OrientationSwapped {
		if run.Phase == domain.CompactionSwapped {
			run, err = u.advance(ctx, run, domain.CompactionRollbackPublishIntent)
			if err != nil {
				return run, err
			}
		}
		if run.Phase != domain.CompactionRollbackPublishIntent {
			return run, fmt.Errorf("swapped rollback requires publish intent, got %q", run.Phase)
		}
		if err = u.files.PublishRollback(ctx, run); err != nil {
			return run, err
		}
		run, err = u.advance(ctx, run, domain.CompactionRollbackReady)
		if err != nil {
			return run, err
		}
		observation, err = u.files.Observe(ctx, run)
		if err != nil {
			return run, err
		}
	}
	if observation.Orientation != domain.OrientationRollbackReady {
		return run, fmt.Errorf("rollback requires rollback-ready orientation, got %q", observation.Orientation)
	}
	if run.Phase != domain.CompactionRollbackSwapIntent {
		run, err = u.advance(ctx, run, domain.CompactionRollbackSwapIntent)
		if err != nil {
			return run, err
		}
	}
	if err := u.files.Exchange(ctx, run); err != nil {
		return run, err
	}
	run, err = u.advance(ctx, run, domain.CompactionRollbackSwapped)
	if err != nil {
		return run, err
	}
	return u.advance(ctx, run, domain.CompactionRolledBack)
}

func (u *storeCompactionUsecase) validateBinding(run domain.CompactionRun) error {
	if filepath.Clean(run.SourcePath) != u.expectedStore {
		return fmt.Errorf("compaction run is bound to another store")
	}
	return nil
}

func (u *storeCompactionUsecase) advance(ctx context.Context, run domain.CompactionRun, phase domain.CompactionPhase) (domain.CompactionRun, error) {
	next, err := run.Advance(phase, u.now())
	if err != nil {
		return run, err
	}
	if err := u.journal.Append(ctx, next); err != nil {
		return run, err
	}
	return next, nil
}
