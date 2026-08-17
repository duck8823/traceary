package cli

import (
	"context"
	"strings"
	"time"

	"golang.org/x/xerrors"

	apptypes "github.com/duck8823/traceary/application/types"
)

func (c *RootCLI) applySearchProjectionRecovery(ctx context.Context, input doctorCommandInput) (doctorFixLog, bool) {
	log := doctorFixLog{Name: "search-projection-parked", Before: "unknown"}
	if c.searchProjection == nil {
		return log, false
	}
	status, err := c.searchProjection.ControlStatus(ctx)
	if err != nil {
		log.Error = err.Error()
		return log, true
	}
	notice := apptypes.SearchProjectionStatus{
		State:        status.State,
		Phase:        status.Phase,
		ConfigHash:   status.ConfigHash,
		FailureClass: status.FailureClass,
		Origin:       status.Origin,
	}
	notice.ApplyParkedNotice(apptypes.DefaultSearchProjectionBudget().ConfigHash())
	// Parked failed/default-budget recovery only. Healthy in-flight rebuilds
	// and operator hash-mismatch belong on compact --projection-rebuild.
	if notice.RecoveryCommand != apptypes.SearchProjectionRecoveryCommand {
		return log, false
	}
	rebuilding := status.State == "rebuilding" || (status.State == "drifted" && status.Phase == "cleanup")
	needsStart := !rebuilding
	budget := apptypes.DefaultSearchProjectionBudget()
	if needsStart {
		if input.dryRun {
			log.Action = "dry-run: would start a replacement search-projection generation"
			return log, true
		}
		if _, err := c.searchProjection.StartGeneration(ctx, budget, time.Now()); err != nil {
			if !strings.Contains(err.Error(), "already rebuilding") {
				log.Error = xerrors.Errorf("%s: %w", Localize("failed to start search-projection recovery", "search-projection 復旧の開始に失敗しました"), err).Error()
				return log, true
			}
		} else {
			rebuilding = true
		}
	}
	if !rebuilding {
		return log, false
	}
	if input.dryRun {
		if log.Action == "" {
			log.Action = "dry-run: would resume bounded search-projection batches"
		}
		return log, true
	}
	result, err := c.searchProjection.ResumeUntil(ctx, budget, defaultProjectionRunOptions(), time.Now())
	if err != nil {
		log.Error = err.Error()
		return log, true
	}
	if result.StopReason == "complete" || result.Progress.Completed {
		log.Action = "completed search-projection recovery"
		return log, true
	}
	log.Action = "resumed search-projection recovery (" + result.StopReason + ")"
	return log, true
}
