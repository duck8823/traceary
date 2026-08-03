//nolint:wrapcheck // Protocol adapters return typed safety failures to their caller.
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

type preparedStoreUpgradeUsecase struct {
	expectedStore string
	journal       application.PreparedStoreUpgradeJournal
	files         application.PreparedStoreUpgradeFiles
	lease         application.PreparedStoreUpgradeLease
	recipes       map[domain.PreparedStoreUpgradeOperation]application.PreparedCandidateRecipe
	now           func() time.Time
}

// NewPreparedStoreUpgradeUsecase composes the shared, fixed-recipe publication protocol.
func NewPreparedStoreUpgradeUsecase(expectedStore string, journal application.PreparedStoreUpgradeJournal, files application.PreparedStoreUpgradeFiles, lease application.PreparedStoreUpgradeLease, recipes map[domain.PreparedStoreUpgradeOperation]application.PreparedCandidateRecipe) application.PreparedStoreUpgradeUsecase {
	owned := make(map[domain.PreparedStoreUpgradeOperation]application.PreparedCandidateRecipe, len(recipes))
	for operation, recipe := range recipes {
		owned[operation] = recipe
	}
	return &preparedStoreUpgradeUsecase{expectedStore: filepath.Clean(expectedStore), journal: journal, files: files, lease: lease, recipes: owned, now: time.Now}
}

func (u *preparedStoreUpgradeUsecase) Plan(ctx context.Context, command application.PreparedStoreUpgradeCommand) (domain.PreparedStoreUpgradeRun, error) {
	if filepath.Clean(command.TargetPath) != u.expectedStore || command.ConsumerBinding == "" || !command.Operation.Known() || u.recipes[command.Operation] == nil {
		return domain.PreparedStoreUpgradeRun{}, fmt.Errorf("invalid prepared upgrade binding")
	}
	idBytes := make([]byte, 16)
	if _, err := rand.Read(idBytes); err != nil {
		return domain.PreparedStoreUpgradeRun{}, fmt.Errorf("create prepared upgrade id: %w", err)
	}
	id := hex.EncodeToString(idBytes)
	now := u.now().UTC()
	candidateSuffix := ".compact-"
	if command.Operation == domain.PreparedStoreUpgradeOperationPayloadRehearsalMigration {
		candidateSuffix = ".prepare-"
	}
	run := domain.PreparedStoreUpgradeRun{ID: id, SourcePath: command.TargetPath, CandidatePath: command.TargetPath + candidateSuffix + id, RollbackPath: command.TargetPath + ".rollback-" + id, Phase: domain.PreparedStoreUpgradePlanned, Operation: command.Operation, ConsumerBinding: command.ConsumerBinding, Budget: command.Budget, CreatedAt: now, UpdatedAt: now}
	planDigest, err := u.recipes[command.Operation].Plan(ctx, application.PreparedCandidateRequest{Run: run})
	if err != nil {
		return run, err
	}
	run.PlanDigest = planDigest
	planned, err := u.files.Plan(ctx, run)
	if err != nil {
		return run, err
	}
	if err = u.journal.Create(ctx, planned); err != nil {
		return run, err
	}
	return planned, nil
}

func (u *preparedStoreUpgradeUsecase) Prepare(ctx context.Context, id string) (domain.PreparedStoreUpgradeRun, error) {
	run, err := u.load(id, ctx)
	if err != nil {
		return run, err
	}
	return u.prepare(ctx, run)
}

func (u *preparedStoreUpgradeUsecase) prepare(ctx context.Context, run domain.PreparedStoreUpgradeRun) (domain.PreparedStoreUpgradeRun, error) {
	recipe := u.recipes[run.Operation]
	for run.Phase != domain.PreparedStoreUpgradeCandidateVerified {
		action, err := run.NextAction()
		if err != nil {
			return run, err
		}
		switch action {
		case domain.ActionRecheckPlan:
			err = u.files.Recheck(ctx, run)
			if err == nil {
				run, err = u.advance(ctx, run, domain.PreparedStoreUpgradeCopyIntent)
			}
		case domain.ActionPrepareCandidate:
			var identity domain.StoreFileIdentity
			identity, err = u.files.PrepareCandidate(ctx, run)
			if err == nil {
				run.PreparedCandidateIdentity = identity
				run.PreparedAttempt++
				run, err = u.advance(ctx, run, domain.PreparedStoreUpgradeCandidatePrepared)
			}
		case domain.ActionBuildCandidate:
			err = recipe.Build(ctx, application.PreparedCandidateRequest{Run: run})
			if err == nil {
				run, err = u.advance(ctx, run, domain.PreparedStoreUpgradeCopyComplete)
			}
		case domain.ActionRecordSyncIntent:
			run, err = u.advance(ctx, run, domain.PreparedStoreUpgradeCandidateSyncIntent)
		case domain.ActionSyncCandidate:
			err = recipe.Sync(ctx, application.PreparedCandidateRequest{Run: run})
			if err == nil {
				run, err = u.advance(ctx, run, domain.PreparedStoreUpgradeCandidateSynced)
			}
		case domain.ActionRecordScrubInProgress:
			run, err = u.advance(ctx, run, domain.PreparedStoreUpgradeScrubInProgress)
		case domain.ActionVerifyCandidate:
			var evidence domain.PreparedCandidateEvidence
			evidence, err = recipe.Verify(ctx, application.PreparedCandidateRequest{Run: run})
			if err == nil {
				run.Evidence = evidence
				run, err = u.files.FenceCandidate(ctx, run)
			}
			if err == nil {
				run, err = u.advance(ctx, run, domain.PreparedStoreUpgradeCandidateVerified)
			}
		default:
			return run, fmt.Errorf("preparation reached publication action %q", action)
		}
		if err != nil {
			return run, err
		}
	}
	return run, nil
}

func (u *preparedStoreUpgradeUsecase) Publish(ctx context.Context, id string) (application.PreparedStoreUpgradeReceipt, error) {
	run, err := u.load(id, ctx)
	if err != nil {
		return application.PreparedStoreUpgradeReceipt{}, err
	}
	if run.Phase != domain.PreparedStoreUpgradeCandidateVerified {
		return application.PreparedStoreUpgradeReceipt{}, fmt.Errorf("prepared candidate is not verified")
	}
	return u.publish(ctx, run)
}

func (u *preparedStoreUpgradeUsecase) publish(ctx context.Context, run domain.PreparedStoreUpgradeRun) (application.PreparedStoreUpgradeReceipt, error) {
	started := u.now()
	lockCtx := ctx
	cancel := func() {}
	if run.Budget.PublishLockLimit > 0 {
		lockCtx, cancel = context.WithTimeout(ctx, run.Budget.PublishLockLimit)
	}
	defer cancel()
	release, err := u.lease.AcquireExclusive(lockCtx, u.expectedStore)
	if err != nil {
		return application.PreparedStoreUpgradeReceipt{}, err
	}
	defer release()
	if err = u.files.RecheckForPublish(lockCtx, run); err != nil {
		return application.PreparedStoreUpgradeReceipt{}, err
	}
	for run.Phase != domain.PreparedStoreUpgradeCommitted {
		action, nextErr := run.NextAction()
		if nextErr != nil {
			return application.PreparedStoreUpgradeReceipt{}, nextErr
		}
		switch action {
		case domain.ActionRecordSwapIntent:
			run, err = u.advance(lockCtx, run, domain.PreparedStoreUpgradeSwapIntent)
		case domain.ActionExchange:
			err = u.files.Exchange(lockCtx, run)
			if err == nil {
				run, err = u.advance(lockCtx, run, domain.PreparedStoreUpgradeSwapped)
			}
		case domain.ActionRecordPublishIntent:
			run, err = u.advance(lockCtx, run, domain.PreparedStoreUpgradeRollbackPublishIntent)
		case domain.ActionPublishRollback:
			err = u.files.PublishRollback(lockCtx, run)
			if err == nil {
				run, err = u.advance(lockCtx, run, domain.PreparedStoreUpgradeRollbackReady)
			}
		case domain.ActionCommit:
			run, err = u.advance(lockCtx, run, domain.PreparedStoreUpgradeCommitted)
		default:
			return application.PreparedStoreUpgradeReceipt{}, fmt.Errorf("publication encountered preparation action %q", action)
		}
		if err != nil {
			return application.PreparedStoreUpgradeReceipt{}, err
		}
	}
	observation, err := u.files.Observe(lockCtx, run)
	if err != nil {
		return application.PreparedStoreUpgradeReceipt{}, err
	}
	return application.PreparedStoreUpgradeReceipt{RunID: run.ID, Operation: run.Operation, ConsumerBinding: run.ConsumerBinding, TargetIdentity: observation.Source, RollbackIdentity: observation.Rollback, Evidence: run.Evidence, PublishMilliseconds: u.now().Sub(started).Milliseconds()}, nil
}

func (u *preparedStoreUpgradeUsecase) Resume(ctx context.Context, id string) (application.PreparedStoreUpgradeReceipt, error) {
	run, err := u.load(id, ctx)
	if err != nil {
		return application.PreparedStoreUpgradeReceipt{}, err
	}
	if isPreparationPhase(run.Phase) {
		run, err = u.recoverPreparation(ctx, run)
		if err != nil {
			return application.PreparedStoreUpgradeReceipt{}, err
		}
		run, err = u.prepare(ctx, run)
		if err != nil {
			return application.PreparedStoreUpgradeReceipt{}, err
		}
	}
	return u.recoverAndPublish(ctx, run)
}

func isPreparationPhase(p domain.PreparedStoreUpgradePhase) bool {
	switch p {
	case domain.PreparedStoreUpgradePlanned, domain.PreparedStoreUpgradeCopyIntent, domain.PreparedStoreUpgradeCopyRetryIntent, domain.PreparedStoreUpgradeCandidatePrepared, domain.PreparedStoreUpgradeCopyComplete, domain.PreparedStoreUpgradeCandidateSyncIntent, domain.PreparedStoreUpgradeCandidateSynced, domain.PreparedStoreUpgradeScrubInProgress:
		return true
	}
	return false
}

func (u *preparedStoreUpgradeUsecase) recoverPreparation(ctx context.Context, run domain.PreparedStoreUpgradeRun) (domain.PreparedStoreUpgradeRun, error) {
	obs, err := u.files.Observe(ctx, run)
	if err != nil {
		return run, err
	}
	if run.Phase == domain.PreparedStoreUpgradeCandidatePrepared && obs.Orientation == domain.OrientationCandidateReady {
		obs.CandidateCondition, err = u.recipes[run.Operation].ClassifyOwnedCandidate(ctx, application.PreparedCandidateInspection{Run: run, Observation: obs})
		if err != nil {
			return run, err
		}
	}
	actions, err := run.RecoveryActions(obs)
	if err != nil {
		return run, err
	}
	for _, a := range actions {
		switch a {
		case domain.ActionRecordCopyRetryIntent:
			run, err = u.advance(ctx, run, domain.PreparedStoreUpgradeCopyRetryIntent)
		case domain.ActionRemoveOwnedPartialCandidate:
			err = u.files.Recheck(ctx, run)
			if err == nil {
				err = u.files.RemoveOwnedPartialCandidate(ctx, run, obs)
			}
		case domain.ActionRecordCopyComplete:
			run, err = u.advance(ctx, run, domain.PreparedStoreUpgradeCopyComplete)
		default:
			return run, fmt.Errorf("unexpected preparation recovery action %q", a)
		}
		if err != nil {
			return run, err
		}
	}
	return run, nil
}

func (u *preparedStoreUpgradeUsecase) recoverAndPublish(ctx context.Context, run domain.PreparedStoreUpgradeRun) (application.PreparedStoreUpgradeReceipt, error) {
	if run.Phase == domain.PreparedStoreUpgradeCandidateVerified {
		return u.publish(ctx, run)
	}
	release, err := u.lease.AcquireExclusive(ctx, u.expectedStore)
	if err != nil {
		return application.PreparedStoreUpgradeReceipt{}, err
	}
	released := false
	defer func() {
		if !released {
			release()
		}
	}()
	obs, err := u.files.Observe(ctx, run)
	if err != nil {
		return application.PreparedStoreUpgradeReceipt{}, err
	}
	actions, err := run.RecoveryActions(obs)
	if err != nil {
		return application.PreparedStoreUpgradeReceipt{}, err
	}
	for _, a := range actions {
		switch a {
		case domain.ActionSyncRecoveredOrientation:
			err = u.files.SyncRecoveredOrientation(ctx, run, obs)
		case domain.ActionExchange:
			err = u.files.Exchange(ctx, run)
		case domain.ActionRecordSwapIntent:
			run, err = u.advance(ctx, run, domain.PreparedStoreUpgradeSwapIntent)
		case domain.ActionRecordSwapped:
			run, err = u.advance(ctx, run, domain.PreparedStoreUpgradeSwapped)
		case domain.ActionRecordPublishIntent:
			run, err = u.advance(ctx, run, domain.PreparedStoreUpgradeRollbackPublishIntent)
		case domain.ActionRecordRollbackReady:
			run, err = u.advance(ctx, run, domain.PreparedStoreUpgradeRollbackReady)
		default:
			release()
			return application.PreparedStoreUpgradeReceipt{}, fmt.Errorf("unexpected publication recovery action %q", a)
		}
		if err != nil {
			release()
			return application.PreparedStoreUpgradeReceipt{}, err
		}
	}
	// Release and reacquire through publish so the budget covers the complete normal tail.
	release()
	released = true
	return u.publish(ctx, run)
}

func (u *preparedStoreUpgradeUsecase) Rollback(ctx context.Context, id string) (domain.PreparedStoreUpgradeRun, error) {
	release, err := u.lease.AcquireExclusive(ctx, u.expectedStore)
	if err != nil {
		return domain.PreparedStoreUpgradeRun{}, err
	}
	defer release()
	run, err := u.load(id, ctx)
	if err != nil {
		return run, err
	}
	obs, err := u.files.Observe(ctx, run)
	if err != nil {
		return run, err
	}
	// A crash after exchange must first publish the original inode at rollback path.
	if obs.Orientation == domain.OrientationSwapped {
		if run.Phase == domain.PreparedStoreUpgradeSwapped {
			run, err = u.advance(ctx, run, domain.PreparedStoreUpgradeRollbackPublishIntent)
		}
		if err == nil {
			err = u.files.PublishRollback(ctx, run)
		}
		if err == nil {
			run, err = u.advance(ctx, run, domain.PreparedStoreUpgradeRollbackReady)
		}
		if err != nil {
			return run, err
		}
		obs, err = u.files.Observe(ctx, run)
		if err != nil {
			return run, err
		}
	}
	if obs.Orientation == domain.OrientationRolledBack {
		return run, nil
	}
	if obs.Orientation != domain.OrientationRollbackReady {
		return run, fmt.Errorf("rollback requires rollback-ready orientation")
	}
	if run.Phase != domain.PreparedStoreUpgradeRollbackSwapIntent {
		run, err = u.advance(ctx, run, domain.PreparedStoreUpgradeRollbackSwapIntent)
		if err != nil {
			return run, err
		}
	}
	if err = u.files.Exchange(ctx, run); err != nil {
		return run, err
	}
	run, err = u.advance(ctx, run, domain.PreparedStoreUpgradeRollbackSwapped)
	if err != nil {
		return run, err
	}
	return u.advance(ctx, run, domain.PreparedStoreUpgradeRolledBack)
}

func (u *preparedStoreUpgradeUsecase) load(id string, ctx context.Context) (domain.PreparedStoreUpgradeRun, error) {
	run, err := u.journal.Load(ctx, id)
	if err == nil && (filepath.Clean(run.SourcePath) != u.expectedStore || !run.Operation.Known() || u.recipes[run.Operation] == nil) {
		err = fmt.Errorf("prepared upgrade run binding mismatch")
	}
	return run, err
}
func (u *preparedStoreUpgradeUsecase) advance(ctx context.Context, run domain.PreparedStoreUpgradeRun, p domain.PreparedStoreUpgradePhase) (domain.PreparedStoreUpgradeRun, error) {
	next, err := run.Advance(p, u.now())
	if err != nil {
		return run, err
	}
	if err = u.journal.Append(ctx, next); err != nil {
		return run, err
	}
	return next, nil
}
