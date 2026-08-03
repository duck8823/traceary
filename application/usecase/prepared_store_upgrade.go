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
	run, err := u.load(ctx, id)
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
	run, err := u.load(ctx, id)
	if err != nil {
		return application.PreparedStoreUpgradeReceipt{}, err
	}
	if run.Phase != domain.PreparedStoreUpgradeCandidateVerified {
		return application.PreparedStoreUpgradeReceipt{}, fmt.Errorf("prepared candidate is not verified")
	}
	return u.recoverAndPublish(ctx, run)
}

func (u *preparedStoreUpgradeUsecase) publishLeased(ctx context.Context, run domain.PreparedStoreUpgradeRun) (application.PreparedStoreUpgradeReceipt, error) {
	started := u.now()
	var err error
	for run.Phase != domain.PreparedStoreUpgradeCommitted {
		action, nextErr := run.NextAction()
		if nextErr != nil {
			return application.PreparedStoreUpgradeReceipt{}, nextErr
		}
		switch action {
		case domain.ActionExchange:
			err = u.files.Exchange(ctx, run)
			if err == nil {
				run, err = u.advance(ctx, run, domain.PreparedStoreUpgradeSwapped)
			}
		case domain.ActionRecordPublishIntent:
			run, err = u.advance(ctx, run, domain.PreparedStoreUpgradeRollbackPublishIntent)
		case domain.ActionPublishRollback:
			err = u.files.PublishRollback(ctx, run)
			if err == nil {
				run, err = u.advance(ctx, run, domain.PreparedStoreUpgradeRollbackReady)
			}
		case domain.ActionCommit:
			run, err = u.advance(ctx, run, domain.PreparedStoreUpgradeCommitted)
		default:
			return application.PreparedStoreUpgradeReceipt{}, fmt.Errorf("publication encountered preparation action %q", action)
		}
		if err != nil {
			return application.PreparedStoreUpgradeReceipt{}, err
		}
	}
	observation, err := u.files.Observe(ctx, run)
	if err != nil {
		return application.PreparedStoreUpgradeReceipt{}, err
	}
	return application.PreparedStoreUpgradeReceipt{RunID: run.ID, Operation: run.Operation, ConsumerBinding: run.ConsumerBinding, TargetIdentity: observation.Source, RollbackIdentity: observation.Rollback, Evidence: run.Evidence, PublishMilliseconds: u.now().Sub(started).Milliseconds()}, nil
}

func (u *preparedStoreUpgradeUsecase) Resume(ctx context.Context, id string) (application.PreparedStoreUpgradeReceipt, error) {
	run, err := u.load(ctx, id)
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
	// The intent is durable before waiting for the publication lease. A crash
	// here is safely resumed from candidate-ready/swap-intent orientation.
	if run.Phase == domain.PreparedStoreUpgradeCandidateVerified {
		var err error
		run, err = u.advance(ctx, run, domain.PreparedStoreUpgradeSwapIntent)
		if err != nil {
			return application.PreparedStoreUpgradeReceipt{}, err
		}
	}
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
	if run.Phase == domain.PreparedStoreUpgradeSwapIntent {
		if err = u.files.RecheckForPublish(lockCtx, run); err != nil {
			return application.PreparedStoreUpgradeReceipt{}, err
		}
	}
	obs, err := u.files.Observe(lockCtx, run)
	if err != nil {
		return application.PreparedStoreUpgradeReceipt{}, err
	}
	actions, err := run.RecoveryActions(obs)
	if err != nil {
		return application.PreparedStoreUpgradeReceipt{}, err
	}
	for _, a := range actions {
		run, err = u.executeRecoveryAction(lockCtx, run, obs, a)
		if err != nil {
			return application.PreparedStoreUpgradeReceipt{}, err
		}
	}
	return u.publishLeased(lockCtx, run)
}

func (u *preparedStoreUpgradeUsecase) Rollback(ctx context.Context, id string) (domain.PreparedStoreUpgradeRun, error) {
	run, err := u.load(ctx, id)
	if err != nil {
		return run, err
	}
	if run.Phase == domain.PreparedStoreUpgradeRollbackReady || run.Phase == domain.PreparedStoreUpgradeCommitted {
		run, err = u.advance(ctx, run, domain.PreparedStoreUpgradeRollbackSwapIntent)
		if err != nil {
			return run, err
		}
	}
	lockCtx := ctx
	cancel := func() {}
	if run.Budget.PublishLockLimit > 0 {
		lockCtx, cancel = context.WithTimeout(ctx, run.Budget.PublishLockLimit)
	}
	defer cancel()
	release, err := u.lease.AcquireExclusive(lockCtx, u.expectedStore)
	if err != nil {
		return run, err
	}
	defer release()
	obs, err := u.files.Observe(lockCtx, run)
	if err != nil {
		return run, err
	}
	actions, err := run.RecoveryActions(obs)
	if err != nil {
		return run, err
	}
	for _, action := range actions {
		run, err = u.executeRecoveryAction(lockCtx, run, obs, action)
		if err != nil {
			return run, err
		}
	}
	// A rollback requested while publication was only partially journaled must
	// first converge to rollback-ready, never exchange an unpublished inode.
	for run.Phase != domain.PreparedStoreUpgradeRolledBack {
		switch run.Phase {
		case domain.PreparedStoreUpgradeSwapped:
			run, err = u.advance(lockCtx, run, domain.PreparedStoreUpgradeRollbackPublishIntent)
		case domain.PreparedStoreUpgradeRollbackPublishIntent:
			err = u.files.PublishRollback(lockCtx, run)
			if err == nil {
				run, err = u.advance(lockCtx, run, domain.PreparedStoreUpgradeRollbackReady)
			}
		case domain.PreparedStoreUpgradeRollbackReady, domain.PreparedStoreUpgradeCommitted:
			run, err = u.advance(lockCtx, run, domain.PreparedStoreUpgradeRollbackSwapIntent)
		case domain.PreparedStoreUpgradeRollbackSwapIntent:
			err = u.files.Exchange(lockCtx, run)
			if err == nil {
				run, err = u.advance(lockCtx, run, domain.PreparedStoreUpgradeRollbackSwapped)
			}
		case domain.PreparedStoreUpgradeRollbackSwapped:
			run, err = u.advance(lockCtx, run, domain.PreparedStoreUpgradeRolledBack)
		default:
			return run, fmt.Errorf("rollback cannot continue from phase %q", run.Phase)
		}
		if err != nil {
			return run, err
		}
	}
	return run, nil
}

func (u *preparedStoreUpgradeUsecase) executeRecoveryAction(ctx context.Context, run domain.PreparedStoreUpgradeRun, observation domain.PreparedStoreUpgradeObservation, action domain.PreparedStoreUpgradeAction) (domain.PreparedStoreUpgradeRun, error) {
	var err error
	switch action {
	case domain.ActionSyncRecoveredOrientation:
		err = u.files.SyncRecoveredOrientation(ctx, run, observation)
	case domain.ActionExchange, domain.ActionRollbackExchange:
		err = u.files.Exchange(ctx, run)
	case domain.ActionPublishRollback:
		err = u.files.PublishRollback(ctx, run)
	default:
		phase, ok := phaseForPreparedRecoveryAction(action)
		if !ok {
			return run, fmt.Errorf("unsupported prepared recovery action %q", action)
		}
		run, err = u.advance(ctx, run, phase)
	}
	return run, err
}

func phaseForPreparedRecoveryAction(action domain.PreparedStoreUpgradeAction) (domain.PreparedStoreUpgradePhase, bool) {
	switch action {
	case domain.ActionRecordSwapIntent:
		return domain.PreparedStoreUpgradeSwapIntent, true
	case domain.ActionRecordSwapped:
		return domain.PreparedStoreUpgradeSwapped, true
	case domain.ActionRecordPublishIntent:
		return domain.PreparedStoreUpgradeRollbackPublishIntent, true
	case domain.ActionRecordRollbackReady:
		return domain.PreparedStoreUpgradeRollbackReady, true
	case domain.ActionRecordRollbackSwapped:
		return domain.PreparedStoreUpgradeRollbackSwapped, true
	case domain.ActionRecordRolledBack:
		return domain.PreparedStoreUpgradeRolledBack, true
	default:
		return "", false
	}
}

func (u *preparedStoreUpgradeUsecase) load(ctx context.Context, id string) (domain.PreparedStoreUpgradeRun, error) {
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
