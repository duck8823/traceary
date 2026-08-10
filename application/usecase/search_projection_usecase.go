//nolint:revive // Public projection API names are intentionally explicit.
package usecase

import (
	"context"
	"errors"
	"log/slog"
	"math/bits"
	"strings"
	"time"

	"golang.org/x/xerrors"

	apptypes "github.com/duck8823/traceary/application/types"
)

// SearchProjectionStore is the narrow persistence boundary for the explicit
// projection state machine. Policy and generation lifecycle stay here.
type SearchProjectionStore interface {
	Start(context.Context, apptypes.SearchProjectionBudget, time.Time) (apptypes.SearchProjectionGeneration, error)
	SelectSnapshot(context.Context, apptypes.SearchProjectionBudget, time.Time) (apptypes.ProjectionSnapshot, error)
	ApplyBatch(context.Context, apptypes.ProjectionBatchPlan, time.Duration, time.Time) (apptypes.SearchProjectionProgress, error)
	CleanupBatch(context.Context, apptypes.ProjectionBatchPlan, time.Duration, time.Time) (apptypes.SearchProjectionProgress, error)
	MarkFailed(context.Context, string, int64, string, time.Time) error
	SearchProjectionStatus(context.Context) (apptypes.SearchProjectionStatus, error)
	SearchProjectionControlStatus(context.Context) (apptypes.SearchProjectionControlStatus, error)
}

type SearchProjectionInventoryStore interface {
	SelectInventory(context.Context, apptypes.SearchProjectionBudget) (apptypes.SearchProjectionInventorySnapshot, error)
	ApplyInventoryBatch(context.Context, apptypes.SearchProjectionInventoryPlan, time.Duration, time.Time) (apptypes.SearchProjectionProgress, error)
}

type SearchProjectionUsecase struct{ store SearchProjectionStore }

type SearchProjectionAbandonStore interface {
	AbandonSearchProjection(context.Context, time.Time) (apptypes.SearchProjectionAbandonResult, error)
}

// SearchProjectionVerifyStore gates old-generation reclaim on a real
// session-tier query against the generation under construction.
type SearchProjectionVerifyStore interface {
	VerifySearchProjectionSessionTier(context.Context, string) error
}

// NewSearchProjectionUsecase constructs the projection workflow.
func NewSearchProjectionUsecase(store SearchProjectionStore) *SearchProjectionUsecase {
	return &SearchProjectionUsecase{store: store}
}

//nolint:wrapcheck // The application boundary preserves typed store errors.
func (u *SearchProjectionUsecase) StartGeneration(ctx context.Context, b apptypes.SearchProjectionBudget, now time.Time) (apptypes.SearchProjectionGeneration, error) {
	if !b.Valid() {
		return apptypes.SearchProjectionGeneration{}, &apptypes.SearchProjectionNoProgressError{Reason: "invalid generation budget"}
	}
	status, err := u.store.SearchProjectionControlStatus(ctx)
	if err != nil {
		return apptypes.SearchProjectionGeneration{}, xerrors.Errorf("inspect projection before start: %w", err)
	}
	if status.State == "rebuilding" {
		return apptypes.SearchProjectionGeneration{}, &apptypes.SearchProjectionNoProgressError{Reason: "a generation is already rebuilding"}
	}
	return u.store.Start(ctx, b, now.UTC())
}

//nolint:wrapcheck // The application boundary preserves typed store errors.
func (u *SearchProjectionUsecase) Resume(ctx context.Context, b apptypes.SearchProjectionBudget, now time.Time) (apptypes.SearchProjectionProgress, error) {
	wallCtx, cancel := context.WithTimeout(ctx, b.WallTime)
	defer cancel()
	status, err := u.store.SearchProjectionControlStatus(wallCtx)
	if err != nil {
		return apptypes.SearchProjectionProgress{}, xerrors.Errorf("inspect projection before resume: %w", err)
	}
	if status.State != "rebuilding" && (status.State != "drifted" || status.Phase != "cleanup") {
		return apptypes.SearchProjectionProgress{}, &apptypes.SearchProjectionNoProgressError{Reason: "no generation is rebuilding"}
	}
	if status.ConfigHash != b.ConfigHash() {
		return apptypes.SearchProjectionProgress{}, &apptypes.SearchProjectionNoProgressError{Reason: "budget does not match generation configuration"}
	}
	if status.Phase == "inventory" {
		inventoryStore, ok := u.store.(SearchProjectionInventoryStore)
		if !ok {
			return apptypes.SearchProjectionProgress{}, &apptypes.SearchProjectionNoProgressError{Reason: "projection store does not support historical inventory"}
		}
		inventory, inventoryErr := inventoryStore.SelectInventory(wallCtx, b)
		if inventoryErr != nil {
			return apptypes.SearchProjectionProgress{}, inventoryErr
		}
		plan := apptypes.SearchProjectionInventoryPlan{
			GenerationID: inventory.Generation.GenerationID, ExpectedRevision: inventory.Generation.SourceRevision,
			ExpectedCursor: inventory.Cursor, ExpectedCursorStarted: inventory.CursorStarted,
			Items: inventory.Items, Done: inventory.Done,
		}
		for _, item := range inventory.Items {
			plan.Ledger.Rows++
			plan.Ledger.StoredBytes += int64(len(item.EventID))
			if item.Missing {
				plan.Ledger.LogicalWriteBytes += item.LogicalBytes
			}
			plan.NextCursor = item.EventID
			plan.NextCursorStarted = true
		}
		if err = wallCtx.Err(); err != nil {
			return apptypes.SearchProjectionProgress{}, err
		}
		return inventoryStore.ApplyInventoryBatch(wallCtx, plan, b.LockTime, now.UTC())
	}
	snapshot, err := u.store.SelectSnapshot(wallCtx, b, now.UTC())
	if err != nil {
		var oversized *apptypes.SearchProjectionOversizeError
		if errors.As(err, &oversized) && snapshot.Generation.GenerationID != "" {
			if markErr := u.markFailed(ctx, b.LockTime, snapshot.Generation, oversized.Class, now.UTC()); markErr != nil {
				return apptypes.SearchProjectionProgress{}, markErr
			}
		}
		return apptypes.SearchProjectionProgress{}, err
	}
	// Old-generation reclaim (cleanup_scope=old) must not run until the new
	// session tier answers a real query. Drifted cleanup_scope=all wipes every
	// generation and is not a cutover, so verification is skipped there.
	if snapshot.Phase == "cleanup" && !snapshot.CleanupAll {
		if err = u.verifySessionTierBeforeReclaim(ctx, b.LockTime, snapshot.Generation, now.UTC()); err != nil {
			return apptypes.SearchProjectionProgress{}, err
		}
	}
	plan, err := PlanProjectionBatch(snapshot, b)
	if err != nil {
		var oversized *apptypes.SearchProjectionOversizeError
		if errors.As(err, &oversized) {
			if markErr := u.markFailed(ctx, b.LockTime, snapshot.Generation, oversized.Class, now.UTC()); markErr != nil {
				return apptypes.SearchProjectionProgress{}, markErr
			}
		}
		return apptypes.SearchProjectionProgress{}, err
	}
	if err = wallCtx.Err(); err != nil {
		return apptypes.SearchProjectionProgress{}, err
	}
	if snapshot.Phase == "source" {
		return u.store.ApplyBatch(wallCtx, plan, b.LockTime, now.UTC())
	}
	return u.store.CleanupBatch(wallCtx, plan, b.LockTime, now.UTC())
}

func (u *SearchProjectionUsecase) ResumeUntil(ctx context.Context, b apptypes.SearchProjectionBudget, opts apptypes.SearchProjectionRunOptions, now time.Time) (apptypes.SearchProjectionRunResult, error) {
	started := time.Now()
	result := apptypes.SearchProjectionRunResult{}
	if opts.MaxBatches <= 0 || opts.TotalWallTime <= 0 {
		return result, &apptypes.SearchProjectionNoProgressError{Reason: "multi-batch bounds must be positive"}
	}
	runCtx, cancel := context.WithTimeout(ctx, opts.TotalWallTime)
	defer cancel()
	for result.Batches < opts.MaxBatches {
		if err := ctx.Err(); err != nil {
			return result, xerrors.Errorf("resume projection batches: %w", err)
		}
		if err := runCtx.Err(); err != nil {
			result.StopReason = "total_wall_time"
			result.ElapsedMilliseconds = time.Since(started).Milliseconds()
			return result, nil
		}
		batchBudget := b
		for {
			progress, err := u.Resume(runCtx, batchBudget, now.UTC())
			if err == nil {
				result.Batches++
				result.Progress.Selected += progress.Selected
				result.Progress.Written += progress.Written
				result.Progress.Evicted += progress.Evicted
				result.Progress.Cleaned += progress.Cleaned
				result.Progress.StoredBytes += progress.StoredBytes
				result.Progress.DecodedBytes += progress.DecodedBytes
				result.Progress.WrittenBytes += progress.WrittenBytes
				result.Progress.CleanupBytes += progress.CleanupBytes
				result.Progress.Completed = progress.Completed
				result.Progress.GenerationID = progress.GenerationID
				if progress.Completed {
					result.StopReason = "complete"
				}
				break
			}
			if ctx.Err() != nil {
				return result, xerrors.Errorf("resume projection batches: %w", ctx.Err())
			}
			if errors.Is(runCtx.Err(), context.DeadlineExceeded) {
				result.StopReason = "total_wall_time"
				result.ElapsedMilliseconds = time.Since(started).Milliseconds()
				return result, nil
			}
			var noProgress *apptypes.SearchProjectionNoProgressError
			if !errors.As(err, &noProgress) || noProgress.Code != apptypes.SearchProjectionNoProgressLockDurationCap {
				return result, err
			}
			if batchBudget.Rows <= 1 {
				return result, &apptypes.SearchProjectionNoProgressError{
					Code:   apptypes.SearchProjectionNoProgressSingleRowLockDurationCap,
					Reason: "a single row exceeded the projection lock duration cap at the minimum batch size",
				}
			}
			batchBudget.Rows /= 2
		}
		if result.StopReason == "complete" {
			break
		}
	}
	if result.StopReason == "" {
		result.StopReason = "max_batches"
	}
	result.ElapsedMilliseconds = time.Since(started).Milliseconds()
	return result, nil
}

// resumeCatchUpBatch uses the same adaptive loop as the operator's
// ResumeUntil path. The number of attempts is the halving floor expressed in
// wall time: each failed attempt halves the row budget until one row, followed
// by the final one-row attempt. This keeps a store open from inheriting an
// unbounded retry cost from transient lock contention.
func (u *SearchProjectionUsecase) resumeCatchUpBatch(ctx context.Context, b apptypes.SearchProjectionBudget, now time.Time) (apptypes.SearchProjectionProgress, error) {
	result, err := u.ResumeUntil(ctx, b, apptypes.SearchProjectionRunOptions{
		MaxBatches:    1,
		TotalWallTime: time.Duration(bits.Len(uint(b.Rows))) * b.LockTime,
	}, now)
	return result.Progress, err
}

func (u *SearchProjectionUsecase) Abandon(ctx context.Context, now time.Time) (apptypes.SearchProjectionAbandonResult, error) {
	store, ok := u.store.(SearchProjectionAbandonStore)
	if !ok {
		return apptypes.SearchProjectionAbandonResult{}, &apptypes.SearchProjectionNoProgressError{Reason: "projection store does not support abandon"}
	}
	result, err := store.AbandonSearchProjection(ctx, now.UTC())
	if err != nil {
		return result, xerrors.Errorf("abandon search projection: %w", err)
	}
	return result, nil
}

// markFailed is an explicit recovery transition. It uses the original parent
// and its own lock deadline so an exhausted batch wall budget cannot strand the
// generation in rebuilding.
//
//nolint:wrapcheck // Preserve typed recovery-transition errors.
func (u *SearchProjectionUsecase) markFailed(ctx context.Context, lock time.Duration, generation apptypes.SearchProjectionGeneration, class string, now time.Time) error {
	failureCtx, cancel := context.WithTimeout(ctx, lock)
	defer cancel()
	return u.store.MarkFailed(failureCtx, generation.GenerationID, generation.SourceRevision, class, now)
}

// verifySessionTierBeforeReclaim runs the pre-abandon session-tier gate. A
// failing verification marks the generation failed so the previous generation's
// rows remain until a later successful rebuild cleans them up.
func (u *SearchProjectionUsecase) verifySessionTierBeforeReclaim(
	ctx context.Context,
	lock time.Duration,
	generation apptypes.SearchProjectionGeneration,
	now time.Time,
) error {
	verifyStore, ok := u.store.(SearchProjectionVerifyStore)
	if !ok {
		// Stores without the gate cannot reclaim safely; fail closed rather than
		// delete the previous generation on faith.
		return &apptypes.SearchProjectionNoProgressError{Reason: "projection store does not support session tier verification"}
	}
	if err := verifyStore.VerifySearchProjectionSessionTier(ctx, generation.GenerationID); err != nil {
		var noProgress *apptypes.SearchProjectionNoProgressError
		if errors.As(err, &noProgress) {
			if markErr := u.markFailed(ctx, lock, generation, "session_tier_unverified", now); markErr != nil {
				return markErr
			}
		}
		return xerrors.Errorf("verify session tier before reclaim: %w", err)
	}
	return nil
}

// CatchUp advances the bounded search projection by at most one durable unit of
// work using the existing generation machinery. It is the store-open counterpart
// of event search backfill: no operator command, no full rebuild, resumable.
//
//nolint:wrapcheck // Typed store and projection errors are preserved.
func (u *SearchProjectionUsecase) CatchUp(ctx context.Context, b apptypes.SearchProjectionBudget, now time.Time) (apptypes.SearchProjectionCatchUpResult, error) {
	out := apptypes.SearchProjectionCatchUpResult{Action: "none"}
	if !b.Valid() {
		return out, &apptypes.SearchProjectionNoProgressError{Reason: "invalid generation budget"}
	}
	status, err := u.store.SearchProjectionControlStatus(ctx)
	if err != nil {
		return out, xerrors.Errorf("inspect projection before catch-up: %w", err)
	}
	out.State = status.State
	out.Phase = status.Phase
	out.CutoverIndexFamily = status.CutoverIndexFamily
	out.CutoverFamilyBytesBefore = status.CutoverFamilyBytesBefore
	out.CutoverFamilyBytesAfter = status.CutoverFamilyBytesAfter
	out.CutoverBeforeEvidence = status.CutoverBeforeEvidence
	out.CutoverAfterEvidence = status.CutoverAfterEvidence
	// Capacity semantics version is checked before already_complete so a
	// completed generation built under an older model is replaced (#1679 D5).
	// A version number rather than hashing the budget value is deliberate:
	// hashing would abandon an operator's deliberate --index-family-bytes.
	//
	// Restricted to complete / rebuilding / drifted. A failed generation stays
	// parked (see the failed branch below): un-parking it here would restart
	// a deterministic failure on every store open. The operator's explicit
	// `store search-projection start` picks up the new capacity semantics.
	if status.CapacitySemanticsVersion < apptypes.SearchProjectionCapacitySemanticsVersion &&
		(status.State == "complete" || status.State == "rebuilding" || status.State == "drifted") {
		slog.Info("search projection capacity semantics obsolete; replacing generation",
			"persisted_version", status.CapacitySemanticsVersion,
			"current_version", apptypes.SearchProjectionCapacitySemanticsVersion,
			"state", status.State,
		)
		if status.State == "rebuilding" || (status.State == "drifted" && status.Phase == "cleanup") {
			if _, abandonErr := u.Abandon(ctx, now.UTC()); abandonErr != nil {
				return out, xerrors.Errorf("abandon obsolete capacity generation: %w", abandonErr)
			}
		}
		// complete / drifted (non-cleanup): StartGeneration accepts any
		// non-rebuilding state and replaces the generation.
		generation, startErr := u.StartGeneration(ctx, b, now.UTC())
		if startErr != nil {
			return out, startErr
		}
		out.Action = "start"
		out.GenerationID = generation.GenerationID
		progress, resumeErr := u.resumeCatchUpBatch(ctx, b, now.UTC())
		if resumeErr != nil {
			return out, resumeErr
		}
		return u.finishCatchUpProgress(ctx, out, progress)
	}
	if status.State == "complete" {
		out.Action = "already_complete"
		out.Completed = true
		return out, nil
	}
	// Operator-owned rebuild with a different budget must not be hijacked.
	// Only applies when capacity semantics match; obsolete versions are handled
	// above regardless of ConfigHash.
	if (status.State == "rebuilding" || (status.State == "drifted" && status.Phase == "cleanup")) &&
		status.ConfigHash != "" && status.ConfigHash != b.ConfigHash() {
		out.Action = "skipped"
		out.SkippedReason = "budget does not match generation configuration"
		return out, nil
	}
	// A failed generation is parked, not retried. Every failure class this store
	// can record is deterministic — an oversize row exceeds the same budget on
	// every open, session_tier_unverified fails the same query, and abandoned is
	// an operator decision. Auto-starting a replacement would fail identically
	// and add a lifecycle row per open, forever. If a genuinely transient class
	// is ever introduced, this is where it gets its exception.
	if status.State == "failed" {
		out.Action = "skipped"
		// Naming the recovery command matters: neither resume nor abort clears
		// this state. Resume rejects a failed generation, and abort leaves the
		// row failed with class abandoned. Only an explicit start replaces it.
		out.SkippedReason = "parked after generation failure " + failureClassOrUnknown(status.FailureClass) +
			"; run 'traceary store search-projection start' to replace the generation"
		return out, nil
	}
	switch {
	case status.State == "rebuilding", status.State == "drifted" && status.Phase == "cleanup":
		out.Action = "resume"
	case status.State == "idle", status.State == "drifted":
		// Only auto-start when there is source material. An empty store stays
		// idle so tests and fresh installs are not left mid-rebuild.
		needsWork, workErr := u.catchUpHasSourceWork(ctx)
		if workErr != nil {
			return out, workErr
		}
		if !needsWork && status.State == "idle" {
			out.Action = "skipped"
			out.SkippedReason = "no source events to project"
			return out, nil
		}
		generation, startErr := u.StartGeneration(ctx, b, now.UTC())
		if startErr != nil {
			return out, startErr
		}
		out.Action = "start"
		out.GenerationID = generation.GenerationID
		// Fall through to one Resume so a single open does real work.
	default:
		out.Action = "skipped"
		out.SkippedReason = "projection state " + status.State + " is not auto-catchable"
		return out, nil
	}
	// A lock-duration cap may be transient contention, so this path retries on
	// the next open instead of parking the generation as a deterministic row
	// failure. Oversized rows retain the existing explicit failure transition.
	progress, resumeErr := u.resumeCatchUpBatch(ctx, b, now.UTC())
	if resumeErr != nil {
		return out, resumeErr
	}
	return u.finishCatchUpProgress(ctx, out, progress)
}

// finishCatchUpProgress records one Resume's progress onto the catch-up result
// and refreshes cutover evidence fields from a post-batch status read.
func (u *SearchProjectionUsecase) finishCatchUpProgress(ctx context.Context, out apptypes.SearchProjectionCatchUpResult, progress apptypes.SearchProjectionProgress) (apptypes.SearchProjectionCatchUpResult, error) {
	out.Batches = 1
	out.Selected = progress.Selected
	out.Written = progress.Written
	out.Completed = progress.Completed
	out.GenerationID = progress.GenerationID
	statusAfter, inspectErr := u.store.SearchProjectionControlStatus(ctx)
	if inspectErr != nil {
		if progress.Completed {
			return out, xerrors.Errorf("inspect projection after catch-up complete: %w", inspectErr)
		}
		// Non-terminal progress is still durable; surface the progress without
		// failing the unit of work on a transient status read.
		return out, nil
	}
	out.State = statusAfter.State
	out.Phase = statusAfter.Phase
	out.CutoverIndexFamily = statusAfter.CutoverIndexFamily
	out.CutoverFamilyBytesBefore = statusAfter.CutoverFamilyBytesBefore
	out.CutoverFamilyBytesAfter = statusAfter.CutoverFamilyBytesAfter
	out.CutoverBeforeEvidence = statusAfter.CutoverBeforeEvidence
	out.CutoverAfterEvidence = statusAfter.CutoverAfterEvidence
	if progress.Completed {
		out.SessionTierVerified = true
		out.Completed = statusAfter.State == "complete"
	}
	return out, nil
}

// failureClassOrUnknown keeps the parked-skip reason readable when a store
// recorded a failure without a class.
func failureClassOrUnknown(class string) string {
	if strings.TrimSpace(class) == "" {
		return "(unclassified)"
	}
	return class
}

// catchUpHasSourceWork reports whether the store has events that a generation
// would need to project. Used to keep empty installs idle.
func (u *SearchProjectionUsecase) catchUpHasSourceWork(ctx context.Context) (bool, error) {
	type sourceWorkStore interface {
		SearchProjectionHasSourceWork(context.Context) (bool, error)
	}
	if store, ok := u.store.(sourceWorkStore); ok {
		hasWork, err := store.SearchProjectionHasSourceWork(ctx)
		if err != nil {
			return false, xerrors.Errorf("probe projection source work: %w", err)
		}
		return hasWork, nil
	}
	// Without a probe, only resume existing rebuilds; never auto-start.
	return false, nil
}

//nolint:wrapcheck // The application boundary preserves typed store errors.
func (u *SearchProjectionUsecase) Inspect(ctx context.Context) (apptypes.SearchProjectionStatus, error) {
	return u.store.SearchProjectionStatus(ctx)
}
