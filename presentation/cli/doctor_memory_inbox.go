package cli

import (
	"context"

	"golang.org/x/xerrors"

	apptypes "github.com/duck8823/traceary/application/types"
	domtypes "github.com/duck8823/traceary/domain/types"
)

const memoryInboxSaturationWarnThreshold = 500

func (c *RootCLI) inspectMemoryInboxSaturation(ctx context.Context) doctorCheck {
	const name = "memory-inbox-saturation"
	if c.memory == nil {
		return doctorCheck{
			Name:    name,
			Status:  doctorStatusSkip,
			Message: Localize("memory usecase is not configured", "memory usecase が設定されていません"),
		}
	}
	// Bounded list: if we hit the threshold page, treat as saturated.
	summaries, err := c.memory.List(ctx, apptypes.NewMemoryListCriteriaBuilder(memoryInboxSaturationWarnThreshold+1).
		Statuses([]domtypes.MemoryStatus{domtypes.MemoryStatusCandidate}).
		Build())
	if err != nil {
		return doctorCheck{
			Name:    name,
			Status:  doctorStatusWarn,
			Message: localizef("failed to list memory candidates: %v", "メモリ候補の一覧取得に失敗しました: %v", err),
		}
	}
	count := len(summaries)
	if count <= memoryInboxSaturationWarnThreshold {
		return doctorCheck{
			Name:   name,
			Status: doctorStatusPass,
			Message: localizef(
				"memory inbox candidate count is within budget (%d ≤ %d)",
				"memory inbox の candidate 件数は予算内です (%d ≤ %d)",
				count, memoryInboxSaturationWarnThreshold,
			),
		}
	}
	return doctorCheck{
		Name:   name,
		Status: doctorStatusWarn,
		Message: localizef(
			"memory inbox is saturated: listed at least %d candidates (threshold %d)",
			"memory inbox が飽和しています: 少なくとも %d 件の candidate (threshold %d)",
			count, memoryInboxSaturationWarnThreshold,
		),
		Hint: Localize(
			"preview with `traceary doctor --fix --dry-run`; apply via `traceary doctor --fix`. Session-end hooks also decay when TRACEARY_MEMORY_DECAY is on (default). Restore mis-expired rows via `traceary memory inbox restore`",
			"`traceary doctor --fix --dry-run` で preview し `traceary doctor --fix` で適用。session-end hook も TRACEARY_MEMORY_DECAY が on（既定）なら decay する。誤 expire は `traceary memory inbox restore` で復元",
		),
		FixCommand:       "traceary doctor --fix",
		AutoFixAvailable: true,
		FixFunc: func(ctx context.Context, dryRun bool) (string, error) {
			if dryRun {
				return Localize("would apply memory decay (limit 500)", "memory decay を適用します (limit 500)"), nil
			}
			result, err := c.memory.Decay(ctx, apptypes.MemoryDecayCriteria{
				OlderThan: domtypes.DefaultMemoryDecayOlderThan,
				Limit:     500,
				Apply:     true,
				Dedupe:    true,
			})
			if err != nil {
				return "", xerrors.Errorf("%s: %w", Localize("memory decay fix failed", "memory decay の自動修正に失敗しました"), err)
			}
			return localizef(
				"memory decay applied: expired=%d superseded=%d remaining=%d",
				"memory decay を適用しました: expired=%d superseded=%d remaining=%d",
				len(result.ExpiredIDs), len(result.SupersededIDs), result.RemainingAfter,
			), nil
		},
	}
}
