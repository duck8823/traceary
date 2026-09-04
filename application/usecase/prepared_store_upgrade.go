//nolint:wrapcheck // Protocol adapters return typed safety failures to their caller.
package usecase

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
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
	switch command.Operation {
	case domain.PreparedStoreUpgradeOperationPayloadRehearsalMigration:
		candidateSuffix = ".prepare-"
	case domain.PreparedStoreUpgradeOperationOfflineMigrationUpgrade:
		candidateSuffix = ".upgrade-"
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

// guardPublication refuses to publish a candidate that still carries the
// retired migration-032 search index family, on every forward exchange this
// protocol performs.
//
// Only the payload-rehearsal migration recipe is registered today, and that
// operation is exempt, so this changes nothing that currently runs. It is here
// because the operation set is a public contract: domain.Operation.Known()
// admits compaction, and a future recipe registration would otherwise inherit
// an unguarded publication path with nothing to catch it. The compaction
// usecase enforces the same rule at its own exchange.
func (u *preparedStoreUpgradeUsecase) guardPublication(ctx context.Context, run domain.PreparedStoreUpgradeRun) error {
	return u.files.RejectRetiredSearchIndex(ctx, run)
}

func (u *preparedStoreUpgradeUsecase) publishLeased(ctx context.Context, run domain.PreparedStoreUpgradeRun, leaseStarted time.Time) (application.PreparedStoreUpgradeReceipt, error) {
	var err error
	for run.Phase != domain.PreparedStoreUpgradeCommitted {
		action, nextErr := run.NextAction()
		if nextErr != nil {
			return application.PreparedStoreUpgradeReceipt{}, nextErr
		}
		switch action {
		case domain.ActionExchange:
			err = u.guardPublication(ctx, run)
			if err == nil {
				err = u.files.Exchange(ctx, run)
			}
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
	return application.PreparedStoreUpgradeReceipt{RunID: run.ID, Operation: run.Operation, ConsumerBinding: run.ConsumerBinding, TargetIdentity: observation.Source, RollbackIdentity: observation.Rollback, RollbackPath: run.RollbackPath, Evidence: run.Evidence, PublishMilliseconds: u.now().Sub(leaseStarted).Milliseconds()}, nil
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
	release, lockCtx, cancel, err := u.acquireExclusiveSkippingIfHeld(ctx, run)
	if err != nil {
		return application.PreparedStoreUpgradeReceipt{}, err
	}
	defer cancel()
	defer release()
	leaseStarted := u.now()
	obs, err := u.files.Observe(lockCtx, run)
	if err != nil {
		return application.PreparedStoreUpgradeReceipt{}, err
	}
	// The pre-swap identity fence is valid only while the prepared candidate is
	// still at its original path. After exchange, recovery must reconcile the
	// observed orientation instead of applying a stale pre-swap predicate.
	if run.Phase == domain.PreparedStoreUpgradeSwapIntent && obs.Orientation == domain.OrientationCandidateReady {
		if err = u.files.RecheckForPublish(lockCtx, run); err != nil {
			return application.PreparedStoreUpgradeReceipt{}, err
		}
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
	return u.publishLeased(lockCtx, run, leaseStarted)
}

func (u *preparedStoreUpgradeUsecase) Rollback(ctx context.Context, id string) (domain.PreparedStoreUpgradeRun, error) {
	run, err := u.load(ctx, id)
	if err != nil {
		return run, err
	}
	if run.Phase == domain.PreparedStoreUpgradeRollbackReady || run.Phase == domain.PreparedStoreUpgradeCommitted {
		if run.Operation == domain.PreparedStoreUpgradeOperationOfflineMigrationUpgrade && run.Phase == domain.PreparedStoreUpgradeCommitted {
			return run, fmt.Errorf("upgrade run refuses post-commit rollback: retained file is a forensic backup, not an interchangeable rollback target")
		}
		run, err = u.advance(ctx, run, domain.PreparedStoreUpgradeRollbackSwapIntent)
		if err != nil {
			return run, err
		}
	}
	release, lockCtx, cancel, err := u.acquireExclusiveSkippingIfHeld(ctx, run)
	if err != nil {
		return run, err
	}
	defer cancel()
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
	case domain.ActionExchange:
		// Split from the rollback exchange below. They call the same operation
		// but in opposite directions: this one publishes the candidate, so it
		// must clear the retired-index refusal first.
		err = u.guardPublication(ctx, run)
		if err == nil {
			err = u.files.Exchange(ctx, run)
		}
	case domain.ActionRollbackExchange:
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

func (u *preparedStoreUpgradeUsecase) acquireExclusiveSkippingIfHeld(ctx context.Context, run domain.PreparedStoreUpgradeRun) (func(), context.Context, context.CancelFunc, error) {
	lockCtx := ctx
	cancel := func() {}
	if run.Budget.PublishLockLimit > 0 {
		lockCtx, cancel = context.WithTimeout(ctx, run.Budget.PublishLockLimit)
	}
	if u.lease != nil && u.lease.HoldsExclusive(u.expectedStore) {
		return func() {}, lockCtx, cancel, nil
	}
	release, err := u.lease.AcquireExclusive(lockCtx, u.expectedStore)
	if err != nil {
		cancel()
		return nil, ctx, func() {}, err
	}
	return release, lockCtx, cancel, nil
}

// RunUpgrade holds the exclusive lease (and compact-pending marker) across
// plan, prepare, and publish so hook writers defer instead of racing the clone.
func (u *preparedStoreUpgradeUsecase) RunUpgrade(ctx context.Context, command application.PreparedStoreUpgradeCommand) (application.PreparedStoreUpgradeReceipt, error) {
	if filepath.Clean(command.TargetPath) != u.expectedStore || command.ConsumerBinding == "" || command.Operation != domain.PreparedStoreUpgradeOperationOfflineMigrationUpgrade || u.recipes[command.Operation] == nil {
		return application.PreparedStoreUpgradeReceipt{}, fmt.Errorf("invalid prepared upgrade binding")
	}
	lockCtx := ctx
	cancel := func() {}
	if command.Budget.PublishLockLimit > 0 {
		lockCtx, cancel = context.WithTimeout(ctx, command.Budget.PublishLockLimit)
	}
	defer cancel()
	release, err := u.lease.AcquireExclusive(lockCtx, u.expectedStore)
	if err != nil {
		return application.PreparedStoreUpgradeReceipt{}, err
	}
	defer release()

	run, findErr := u.journal.FindActive(lockCtx, command.Operation, command.TargetPath, command.ConsumerBinding)
	if findErr != nil && !osErrNotExist(findErr) {
		return application.PreparedStoreUpgradeReceipt{}, findErr
	}
	if findErr == nil && !run.Phase.IsTerminal() {
		if isPreparationPhase(run.Phase) {
			run, err = u.recoverPreparation(lockCtx, run)
			if err != nil {
				return application.PreparedStoreUpgradeReceipt{}, err
			}
			run, err = u.prepare(lockCtx, run)
			if err != nil {
				return application.PreparedStoreUpgradeReceipt{}, err
			}
		}
		return u.recoverAndPublish(lockCtx, run)
	}
	run, err = u.Plan(lockCtx, command)
	if err != nil {
		return application.PreparedStoreUpgradeReceipt{}, err
	}
	run, err = u.prepare(lockCtx, run)
	if err != nil {
		return application.PreparedStoreUpgradeReceipt{}, err
	}
	return u.recoverAndPublish(lockCtx, run)
}

func osErrNotExist(err error) bool {
	return err != nil && (errors.Is(err, os.ErrNotExist) || os.IsNotExist(err))
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
