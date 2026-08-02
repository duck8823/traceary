//nolint:wrapcheck // This orchestrator preserves typed adapter failures at the CLI boundary.
package usecase

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"time"

	"github.com/duck8823/traceary/application"
	"github.com/duck8823/traceary/domain"
)

type storeCompactionUsecase struct {
	journal application.StoreCompactionJournal
	builder application.StoreCompactionBuilder
	files   application.StoreReplacementCoordinator
	lease   application.StoreCompactionLease
	now     func() time.Time
}

// NewStoreCompactionUsecase composes the dedicated compaction protocol.
func NewStoreCompactionUsecase(j application.StoreCompactionJournal, b application.StoreCompactionBuilder, f application.StoreReplacementCoordinator, l application.StoreCompactionLease) application.StoreCompactionUsecase {
	return &storeCompactionUsecase{journal: j, builder: b, files: f, lease: l, now: time.Now}
}

func (u *storeCompactionUsecase) Plan(ctx context.Context, source string) (domain.CompactionRun, error) {
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
	run, err := u.journal.Load(ctx, id)
	if err != nil {
		return run, err
	}
	release, err := u.lease.AcquireExclusive(ctx, run.SourcePath)
	if err != nil {
		return run, err
	}
	defer release()
	for run.Phase != domain.CompactionCommitted {
		switch run.Phase {
		case domain.CompactionPlanned:
			run, err = u.advance(ctx, run, domain.CompactionCopyIntent)
		case domain.CompactionCopyIntent:
			err = u.builder.Build(ctx, run.SourcePath, run.CandidatePath)
			if err == nil {
				run, err = u.advance(ctx, run, domain.CompactionCopyComplete)
			}
		case domain.CompactionCopyComplete:
			run, err = u.advance(ctx, run, domain.CompactionCandidateSyncIntent)
		case domain.CompactionCandidateSyncIntent:
			err = u.builder.Sync(ctx, run.CandidatePath)
			if err == nil {
				run, err = u.advance(ctx, run, domain.CompactionCandidateSynced)
			}
		case domain.CompactionCandidateSynced:
			run, err = u.advance(ctx, run, domain.CompactionScrubInProgress)
		case domain.CompactionScrubInProgress:
			err = u.builder.VerifyPair(ctx, run.SourcePath, run.CandidatePath)
			if err == nil {
				run, err = u.files.FenceCandidate(ctx, run)
			}
			if err == nil {
				run, err = u.advance(ctx, run, domain.CompactionCandidateVerified)
			}
		case domain.CompactionCandidateVerified:
			run, err = u.advance(ctx, run, domain.CompactionSwapIntent)
		case domain.CompactionSwapIntent:
			err = u.files.Exchange(ctx, run)
			if err == nil {
				run, err = u.advance(ctx, run, domain.CompactionSwapped)
			}
		case domain.CompactionSwapped:
			run, err = u.advance(ctx, run, domain.CompactionRollbackPublishIntent)
		case domain.CompactionRollbackPublishIntent:
			err = u.files.PublishRollback(ctx, run)
			if err == nil {
				run, err = u.advance(ctx, run, domain.CompactionRollbackReady)
			}
		case domain.CompactionRollbackReady:
			run, err = u.advance(ctx, run, domain.CompactionCommitted)
		default:
			return run, fmt.Errorf("cannot apply compaction in phase %q", run.Phase)
		}
		if err != nil {
			return run, err
		}
	}
	return run, nil
}

func (u *storeCompactionUsecase) Resume(ctx context.Context, id string) (domain.CompactionRun, error) {
	run, err := u.journal.Load(ctx, id)
	if err != nil {
		return run, err
	}
	orientation, err := u.files.Orientation(ctx, run)
	if err != nil {
		return run, err
	}
	if orientation != run.Phase {
		transitions := recoveryTransitions(run.Phase, orientation)
		if len(transitions) == 0 {
			return run, fmt.Errorf("unsafe compaction recovery %q -> %q", run.Phase, orientation)
		}
		for _, phase := range transitions {
			run, err = u.advance(ctx, run, phase)
			if err != nil {
				return run, err
			}
		}
	}
	if run.Phase == domain.CompactionRollbackSwapped {
		return u.advance(ctx, run, domain.CompactionRolledBack)
	}
	if run.Phase == domain.CompactionRolledBack {
		return run, nil
	}
	return u.Apply(ctx, id)
}

func recoveryTransitions(from, to domain.CompactionPhase) []domain.CompactionPhase {
	if from == domain.CompactionCandidateVerified && to == domain.CompactionSwapped {
		return []domain.CompactionPhase{domain.CompactionSwapIntent, domain.CompactionSwapped}
	}
	if from == domain.CompactionSwapIntent && to == domain.CompactionSwapped {
		return []domain.CompactionPhase{domain.CompactionSwapped}
	}
	if from == domain.CompactionSwapped && to == domain.CompactionRollbackReady {
		return []domain.CompactionPhase{domain.CompactionRollbackPublishIntent, domain.CompactionRollbackReady}
	}
	if from == domain.CompactionRollbackPublishIntent && to == domain.CompactionRollbackReady {
		return []domain.CompactionPhase{domain.CompactionRollbackReady}
	}
	if from == domain.CompactionRollbackSwapIntent && to == domain.CompactionRollbackSwapped {
		return []domain.CompactionPhase{domain.CompactionRollbackSwapped}
	}
	return nil
}

func (u *storeCompactionUsecase) Status(ctx context.Context, id string) (domain.CompactionRun, error) {
	return u.journal.Load(ctx, id)
}

func (u *storeCompactionUsecase) Rollback(ctx context.Context, id string) (domain.CompactionRun, error) {
	run, err := u.journal.Load(ctx, id)
	if err != nil {
		return run, err
	}
	if run.Phase != domain.CompactionSwapped && run.Phase != domain.CompactionRollbackReady && run.Phase != domain.CompactionCommitted {
		return run, fmt.Errorf("cannot rollback compaction in phase %q", run.Phase)
	}
	release, err := u.lease.AcquireExclusive(ctx, run.SourcePath)
	if err != nil {
		return run, err
	}
	defer release()
	run, err = u.advance(ctx, run, domain.CompactionRollbackSwapIntent)
	if err != nil {
		return run, err
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
