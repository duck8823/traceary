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

// SearchProjectionCapacityRederiveStore retries a missed source-ceiling
// re-derivation on an already-complete generation (#1751).
type SearchProjectionCapacityRederiveStore interface {
	RecordSearchProjectionCapacityRederivation(context.Context, string, time.Time)
}

// SearchProjectionCompleteTailStore inventories post-cutover source sequences
// on a generation that is already complete (#2173). High-water stays frozen
// after cutover today, so search fail-open-decodes every later event.
type SearchProjectionCompleteTailStore interface {
	CatchUpCompleteGenerationTail(context.Context, apptypes.SearchProjectionBudget, time.Time) (apptypes.SearchProjectionCatchUpResult, error)
}

type SearchProjectionInventoryStore interface {
	SelectInventory(context.Context, apptypes.SearchProjectionBudget) (apptypes.SearchProjectionInventorySnapshot, error)
	ApplyInventoryBatch(context.Context, apptypes.SearchProjectionInventoryPlan, time.Duration, time.Time) (apptypes.SearchProjectionProgress, error)
}

type SearchProjectionUsecase struct{ store SearchProjectionStore }

type SearchProjectionAbandonStore interface {
	AbandonSearchProjection(context.Context, time.Time) (apptypes.SearchProjectionAbandonResult, error)
}

// SearchProjectionObsoleteReplaceStore replaces an observed obsolete generation
// in one fenced transition. CatchUp must not Abandon then Start.
type SearchProjectionObsoleteReplaceStore interface {
	ReplaceObsoleteCapacityGeneration(context.Context, string, apptypes.SearchProjectionBudget, time.Time) (apptypes.SearchProjectionGeneration, error)
}

// SearchProjectionAutomaticStartStore starts a catch-up generation marked
// automatic so a later default-budget change can replace it (#1861).
type SearchProjectionAutomaticStartStore interface {
	StartAutomatic(context.Context, apptypes.SearchProjectionBudget, time.Time) (apptypes.SearchProjectionGeneration, error)
}

// SearchProjectionStaleAutomaticReplaceStore replaces an automatic generation
// whose ConfigHash no longer matches the catch-up budget, in one fenced
// transition. CatchUp must not Abandon then Start.
type SearchProjectionStaleAutomaticReplaceStore interface {
	ReplaceStaleAutomaticGeneration(context.Context, string, apptypes.SearchProjectionBudget, time.Time) (apptypes.SearchProjectionGeneration, error)
}

// SearchProjectionVerifyStore gates old-generation reclaim on a real
// session-tier query against the generation under construction.
type SearchProjectionVerifyStore interface {
	VerifySearchProjectionSessionTier(context.Context, string) error
}

// SearchProjectionTerminalReclaimStore removes derived rows of terminal,
// non-active generations in bounded pages (#2261).
type SearchProjectionTerminalReclaimStore interface {
	ListTerminalGenerations(context.Context) ([]apptypes.SearchProjectionTerminalGeneration, error)
	ReclaimTerminalGenerationPage(context.Context, string, apptypes.SearchProjectionBudget, int, time.Time) (apptypes.SearchProjectionReclaimProgress, error)
}

// SearchProjectionCleanupNoProgressStore records consecutive cleanup-phase
// catch-up attempts that committed no row and parks the generation after N.
type SearchProjectionCleanupNoProgressStore interface {
	RecordCleanupNoProgressAttempt(context.Context, string, time.Time) (int, bool, error)
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
func (u *SearchProjectionUsecase) startAutomaticGeneration(ctx context.Context, b apptypes.SearchProjectionBudget, now time.Time) (apptypes.SearchProjectionGeneration, error) {
	if auto, ok := u.store.(SearchProjectionAutomaticStartStore); ok {
		if !b.Valid() {
			return apptypes.SearchProjectionGeneration{}, &apptypes.SearchProjectionNoProgressError{Reason: "invalid generation budget"}
		}
		status, err := u.store.SearchProjectionControlStatus(ctx)
		if err != nil {
			return apptypes.SearchProjectionGeneration{}, xerrors.Errorf("inspect projection before automatic start: %w", err)
		}
		if status.State == "rebuilding" {
			return apptypes.SearchProjectionGeneration{}, &apptypes.SearchProjectionNoProgressError{Reason: "a generation is already rebuilding"}
		}
		generation, err := auto.StartAutomatic(ctx, b, now.UTC())
		if err != nil {
			return apptypes.SearchProjectionGeneration{}, err
		}
		return generation, nil
	}
	// Test fakes without origin still start; they look operator-owned.
	return u.StartGeneration(ctx, b, now)
}

//nolint:wrapcheck // The application boundary preserves typed store errors.
func (u *SearchProjectionUsecase) Resume(ctx context.Context, b apptypes.SearchProjectionBudget, now time.Time) (apptypes.SearchProjectionProgress, error) {
	wallCtx, cancel := context.WithTimeout(ctx, b.WallTime)
	defer cancel()
	// Inspect uses the caller context: batch WallTime is 1s by default and
	// is too short to ping a multi-GiB read-only store (#2265 / #2298).
	status, err := u.store.SearchProjectionControlStatus(ctx)
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
		return u.applySourcePlan(wallCtx, plan, b, now.UTC())
	}
	return u.store.CleanupBatch(wallCtx, plan, b.LockTime, now.UTC())
}

// applySourcePlan persists a source batch. A single-row hold overrun is the
// #1794 skip-and-record case: persist the exclusion carried on the error
// without selecting a new snapshot (that race would skip the next row).
//
//nolint:wrapcheck // Typed store errors must reach ResumeUntil.
func (u *SearchProjectionUsecase) applySourcePlan(ctx context.Context, plan apptypes.ProjectionBatchPlan, b apptypes.SearchProjectionBudget, now time.Time) (apptypes.SearchProjectionProgress, error) {
	progress, err := u.store.ApplyBatch(ctx, plan, b.LockTime, now)
	if err == nil {
		return progress, err
	}
	// The budget Rows value is not the plan size: a default 128-row resume can
	// still be a one-write plan (last remaining source row). That identity is
	// already on the error; shrinking first would leave single Resume stuck.
	var noProgress *apptypes.SearchProjectionNoProgressError
	if !errors.As(err, &noProgress) || noProgress.Code != apptypes.SearchProjectionNoProgressRowWorkCap || noProgress.Exclusion.EventID == "" {
		return progress, err
	}
	exclude := plan
	exclude.Writes = nil
	exclude.Cleanup = nil
	exclude.Exclusions = []apptypes.ProjectionExclusion{noProgress.Exclusion}
	exclude.NextCheckpoint = noProgress.Exclusion.Sequence
	exclude.NextPhase = ""
	exclude.Completed = false
	exclude.Ledger = apptypes.BudgetLedger{}
	return u.store.ApplyBatch(ctx, exclude, b.LockTime, now)
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
			if !errors.As(err, &noProgress) {
				return result, err
			}
			switch noProgress.Code {
			case apptypes.SearchProjectionNoProgressLockDurationCap:
				if batchBudget.Rows <= 1 {
					return result, &apptypes.SearchProjectionNoProgressError{
						Code:   apptypes.SearchProjectionNoProgressSingleRowLockDurationCap,
						Reason: "a single row exceeded the projection lock duration cap at the minimum batch size",
					}
				}
			case apptypes.SearchProjectionNoProgressRowWorkCap:
				if batchBudget.Rows <= 1 {
					return result, err
				}
			default:
				return result, err
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
		MaxBatches: 1,
		// Resume bounds the whole attempt with WallTime, including status reads,
		// planning, and transaction setup. The total bound must use that same
		// unit so adaptive shrinking can reach its one-row floor.
		TotalWallTime: time.Duration(bits.Len(uint(b.Rows))) * b.WallTime,
	}, now)
	if err == nil && result.Batches == 0 {
		return apptypes.SearchProjectionProgress{}, &apptypes.SearchProjectionNoProgressError{
			Reason: "catch-up total wall time bound exhausted before a batch was committed",
		}
	}
	return result.Progress, err
}

// ReclaimTerminalGenerations pages every terminal generation until Done or the
// wall budget is spent. It never touches active or complete generations (the
// store refuses inside the transaction) and returns partial progress on wall
// exhaustion so the next call resumes.
//
//nolint:wrapcheck // Typed drift and lock-duration errors must cross this boundary.
func (u *SearchProjectionUsecase) ReclaimTerminalGenerations(ctx context.Context, b apptypes.SearchProjectionBudget, opts apptypes.SearchProjectionRunOptions, now time.Time) (apptypes.SearchProjectionReclaimResult, error) {
	var result apptypes.SearchProjectionReclaimResult
	store, ok := u.store.(SearchProjectionTerminalReclaimStore)
	if !ok {
		return result, &apptypes.SearchProjectionNoProgressError{Reason: "projection store does not support terminal reclaim"}
	}
	wall := opts.TotalWallTime
	if wall <= 0 {
		wall = 10 * time.Minute
	}
	runCtx, cancel := context.WithTimeout(ctx, wall)
	defer cancel()
	gens, err := store.ListTerminalGenerations(runCtx)
	if err != nil {
		if runCtx.Err() != nil && ctx.Err() == nil {
			result.StopReason = "wall_time"
			return result, nil
		}
		return result, xerrors.Errorf("list terminal search-projection generations: %w", err)
	}
	result.Generations = gens
	if len(gens) == 0 {
		result.Complete = true
		result.StopReason = "complete"
		return result, nil
	}
	page := apptypes.SearchProjectionTerminalReclaimPageRows
	now = now.UTC()
	for _, g := range gens {
		for {
			if ctx.Err() != nil {
				result.StopReason = "cancelled"
				return result, nil
			}
			if runCtx.Err() != nil {
				result.StopReason = "wall_time"
				return result, nil
			}
			progress, pageErr := store.ReclaimTerminalGenerationPage(runCtx, g.GenerationID, b, page, now)
			if pageErr != nil {
				if ctx.Err() != nil {
					result.StopReason = "cancelled"
					return result, nil
				}
				if errors.Is(runCtx.Err(), context.DeadlineExceeded) {
					result.StopReason = "wall_time"
					return result, nil
				}
				var drift *apptypes.SearchProjectionDriftError
				if errors.As(pageErr, &drift) {
					break
				}
				var noProgress *apptypes.SearchProjectionNoProgressError
				if errors.As(pageErr, &noProgress) && noProgress.Code == apptypes.SearchProjectionNoProgressLockDurationCap {
					if page <= 1 {
						return result, pageErr
					}
					page /= 2
					continue
				}
				return result, pageErr
			}
			result.DeletedRows += progress.Deleted
			result.LogicalBytes += progress.LogicalBytes
			if progress.Done {
				break
			}
		}
	}
	result.Complete = true
	result.StopReason = "complete"
	return result, nil
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
	out.GenerationID = status.GenerationID
	out.Checkpoint = status.Checkpoint
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
	// Restricted to complete / rebuilding / drifted, plus the abandoned
	// corpse left by a crashed two-step replace (#1752). Other failed
	// classes stay parked (see the failed branch below): un-parking them
	// here would restart a deterministic failure on every store open.
	// Operator abandon of a current-semantics generation is not obsolete.
	if status.CapacitySemanticsVersion < apptypes.SearchProjectionCapacitySemanticsVersion &&
		(status.State == "complete" || status.State == "rebuilding" || status.State == "drifted" ||
			(status.State == "failed" && status.FailureClass == "abandoned")) {
		slog.Info("search projection capacity semantics obsolete; replacing generation",
			"persisted_version", status.CapacitySemanticsVersion,
			"current_version", apptypes.SearchProjectionCapacitySemanticsVersion,
			"state", status.State,
			"generation_id", status.GenerationID,
		)
		replaceStore, ok := u.store.(SearchProjectionObsoleteReplaceStore)
		if !ok {
			return out, &apptypes.SearchProjectionNoProgressError{Reason: "projection store does not support obsolete generation replacement"}
		}
		generation, startErr := replaceStore.ReplaceObsoleteCapacityGeneration(ctx, status.GenerationID, b, now.UTC())
		if startErr != nil {
			return out, startErr
		}
		out.Action = "start"
		out.GenerationID = generation.GenerationID
		progress, resumeErr := u.resumeCatchUpBatch(ctx, b, now.UTC())
		if resumeErr != nil {
			return u.refreshCatchUpPosition(ctx, out), resumeErr
		}
		return u.finishCatchUpProgress(ctx, out, progress)
	}
	if status.State == "complete" {
		if status.CapacityRederived == 0 && status.GenerationID != "" {
			if rederive, ok := u.store.(SearchProjectionCapacityRederiveStore); ok {
				rederive.RecordSearchProjectionCapacityRederivation(ctx, status.GenerationID, now.UTC())
			}
		}
		if tailStore, ok := u.store.(SearchProjectionCompleteTailStore); ok {
			tail, tailErr := tailStore.CatchUpCompleteGenerationTail(ctx, b, now.UTC())
			if tailErr != nil {
				return tail, tailErr
			}
			if tail.Selected > 0 || tail.Written > 0 {
				tail.Action = "complete_tail"
				tail.Completed = true
				if tail.State == "" {
					tail.State = status.State
				}
				if tail.GenerationID == "" {
					tail.GenerationID = status.GenerationID
				}
				return tail, nil
			}
		}
		out.Action = "already_complete"
		out.Completed = true
		return out, nil
	}
	// A hash mismatch is either a stale automatic default (#1861) or an
	// operator-owned rebuild. Only the automatic case may be replaced.
	// Obsolete versions are handled above regardless of ConfigHash.
	if (status.State == "rebuilding" || (status.State == "drifted" && status.Phase == "cleanup")) &&
		status.ConfigHash != "" && status.ConfigHash != b.ConfigHash() {
		if status.Origin == apptypes.SearchProjectionOriginAutomatic {
			replaceStore, ok := u.store.(SearchProjectionStaleAutomaticReplaceStore)
			if !ok {
				return out, &apptypes.SearchProjectionNoProgressError{Reason: "projection store does not support stale automatic generation replacement"}
			}
			slog.Info("search projection automatic generation budget stale; replacing generation",
				"persisted_hash", status.ConfigHash,
				"current_hash", b.ConfigHash(),
				"generation_id", status.GenerationID,
			)
			generation, startErr := replaceStore.ReplaceStaleAutomaticGeneration(ctx, status.GenerationID, b, now.UTC())
			if startErr != nil {
				return out, startErr
			}
			out.Action = "start"
			out.GenerationID = generation.GenerationID
			progress, resumeErr := u.resumeCatchUpBatch(ctx, b, now.UTC())
			if resumeErr != nil {
				return u.refreshCatchUpPosition(ctx, out), resumeErr
			}
			return u.finishCatchUpProgress(ctx, out, progress)
		}
		out.Action = "skipped"
		out.GenerationID = status.GenerationID
		out.SkippedReason = "budget does not match generation configuration; run '" +
			apptypes.SearchProjectionStartCommand + "' to replace the generation"
		return out, nil
	}
	// A failed generation is parked, not retried. Every failure class this store
	// can record is deterministic — an oversize row exceeds the same budget on
	// every open, session_tier_unverified fails the same query, abandoned is
	// an operator decision, and cleanup_no_progress already spent N catch-up
	// attempts without a durable cleanup row. Auto-starting a replacement
	// would fail identically and add a lifecycle row per open, forever. If a
	// genuinely transient class is ever introduced, this is where it gets its
	// exception.
	if status.State == "failed" {
		out.Action = "skipped"
		// Naming the recovery command matters: neither resume nor abort clears
		// this state. Resume rejects a failed generation, and abort leaves the
		// row failed with class abandoned. Only an explicit start replaces it.
		out.SkippedReason = "parked after generation failure " + failureClassOrUnknown(status.FailureClass) +
			"; run '" + apptypes.SearchProjectionRecoveryCommand + "' to replace the generation"
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
		generation, startErr := u.startAutomaticGeneration(ctx, b, now.UTC())
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
		out = u.refreshCatchUpPosition(ctx, out)
		parked, parkErr := u.maybeParkCleanupNoProgress(ctx, out, progress, resumeErr, now.UTC())
		if parkErr != nil {
			return out, parkErr
		}
		if parked {
			out.Action = "skipped"
			out.SkippedReason = "parked after generation failure " + apptypes.SearchProjectionFailureCleanupNoProgress +
				"; run '" + apptypes.SearchProjectionRecoveryCommand + "' to replace the generation"
			return u.refreshCatchUpPosition(ctx, out), nil
		}
		return out, resumeErr
	}
	return u.finishCatchUpProgress(ctx, out, progress)
}

func (u *SearchProjectionUsecase) maybeParkCleanupNoProgress(ctx context.Context, out apptypes.SearchProjectionCatchUpResult, progress apptypes.SearchProjectionProgress, resumeErr error, now time.Time) (bool, error) {
	if out.Phase != "cleanup" || !isCleanupCatchUpNoProgress(progress, resumeErr) {
		return false, nil
	}
	store, ok := u.store.(SearchProjectionCleanupNoProgressStore)
	if !ok {
		return false, nil
	}
	generation := out.GenerationID
	if generation == "" {
		generation = progress.GenerationID
	}
	if generation == "" {
		return false, nil
	}
	recordCtx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	_, parked, err := store.RecordCleanupNoProgressAttempt(recordCtx, generation, now)
	if err != nil {
		return false, xerrors.Errorf("record cleanup no-progress attempt: %w", err)
	}
	return parked, nil
}

func isCleanupCatchUpNoProgress(progress apptypes.SearchProjectionProgress, err error) bool {
	if err == nil || progress.Written > 0 || progress.Cleaned > 0 {
		return false
	}
	var drift *apptypes.SearchProjectionDriftError
	if errors.As(err, &drift) {
		return false
	}
	var oversized *apptypes.SearchProjectionOversizeError
	if errors.As(err, &oversized) {
		return false
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	var noProgress *apptypes.SearchProjectionNoProgressError
	if !errors.As(err, &noProgress) {
		return false
	}
	switch noProgress.Code {
	case apptypes.SearchProjectionNoProgressLockDurationCap, apptypes.SearchProjectionNoProgressSingleRowLockDurationCap, apptypes.SearchProjectionNoProgressRowWorkCap:
		return true
	}
	return strings.Contains(noProgress.Reason, "wall") || strings.Contains(noProgress.Reason, "exhausted")
}

// refreshCatchUpPosition re-reads where the projection actually stands before a
// failure is reported. The position read at entry is stale by then in two ways:
// catch-up may have replaced the generation, which resets the checkpoint, and a
// concurrent opener may have committed a batch past it. Both would name a
// position this attempt never stopped at, in a message an operator has nothing
// else to check against. A failed re-read leaves the entry values, which are at
// worst as wrong as they already were.
func (u *SearchProjectionUsecase) refreshCatchUpPosition(ctx context.Context, out apptypes.SearchProjectionCatchUpResult) apptypes.SearchProjectionCatchUpResult {
	status, err := u.store.SearchProjectionControlStatus(ctx)
	if err != nil {
		return out
	}
	out.State = status.State
	out.Phase = status.Phase
	out.GenerationID = status.GenerationID
	out.Checkpoint = status.Checkpoint
	return out
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
	out.Checkpoint = statusAfter.Checkpoint
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

// ControlStatus is the persisted state-machine row, including the completion
// budget verdict. It does not walk dbstat.
func (u *SearchProjectionUsecase) ControlStatus(ctx context.Context) (apptypes.SearchProjectionControlStatus, error) {
	status, err := u.store.SearchProjectionControlStatus(ctx)
	if err != nil {
		return status, xerrors.Errorf("inspect projection control status: %w", err)
	}
	return status, nil
}

// CompleteGeneration starts or resumes a generation and drives it to
// `complete` under the caller's context. Compact holds the exclusive store
// lease across this call so live writers cannot interleave `source changed`
// (#2163). It does not start a replacement generation when one is already
// complete with a matching budget hash.
func (u *SearchProjectionUsecase) CompleteGeneration(ctx context.Context, b apptypes.SearchProjectionBudget, now time.Time) error {
	if !b.Valid() {
		return &apptypes.SearchProjectionNoProgressError{Reason: "invalid generation budget"}
	}
	now = now.UTC()
	status, err := u.ControlStatus(ctx)
	if err != nil {
		return err
	}
	if status.State == "complete" && status.ConfigHash == b.ConfigHash() {
		if tail, ok := u.store.(SearchProjectionCompleteTailStore); ok {
			if _, tailErr := tail.CatchUpCompleteGenerationTail(ctx, b, now); tailErr != nil {
				return xerrors.Errorf("catch up complete generation tail: %w", tailErr)
			}
		}
		status, err = u.ControlStatus(ctx)
		if err != nil {
			return err
		}
		if status.State == "complete" {
			return nil
		}
	}
	if status.GenerationID != "" && status.State != "complete" && status.State != "abandoned" {
		if _, abandonErr := u.Abandon(ctx, now); abandonErr != nil {
			var noProgress *apptypes.SearchProjectionNoProgressError
			if !errors.As(abandonErr, &noProgress) || noProgress.Reason != "no derived generation exists" {
				return abandonErr
			}
		}
	}
	if _, startErr := u.StartGeneration(ctx, b, now); startErr != nil {
		return startErr
	}
	return u.resumeUntilComplete(ctx, b, now)
}

func (u *SearchProjectionUsecase) resumeUntilComplete(ctx context.Context, b apptypes.SearchProjectionBudget, now time.Time) error {
	idle := 0
	for {
		if err := ctx.Err(); err != nil {
			return xerrors.Errorf("complete search projection: %w", err)
		}
		result, err := u.ResumeUntil(ctx, b, apptypes.SearchProjectionRunOptions{
			MaxBatches:    1024,
			TotalWallTime: time.Hour,
		}, now)
		if err != nil {
			return err
		}
		if result.StopReason == "complete" || result.Progress.Completed {
			return nil
		}
		if result.Progress.Selected == 0 && result.Progress.Written == 0 && result.Progress.Cleaned == 0 && result.Progress.Evicted == 0 {
			idle++
			if idle >= 3 {
				return xerrors.Errorf("search projection did not reach complete (stop_reason=%s)", result.StopReason)
			}
			continue
		}
		idle = 0
	}
}
