package cli

import (
	"context"
	"time"

	apptypes "github.com/duck8823/traceary/application/types"
)

func (c *RootCLI) applySearchProjectionTerminalReclaim(ctx context.Context, input doctorCommandInput) (doctorFixLog, bool) {
	log := doctorFixLog{Name: "search-projection-terminal-reclaim", Before: "unknown"}
	if c.searchProjection == nil {
		return log, false
	}
	status, err := c.searchProjection.Inspect(ctx)
	if err != nil {
		log.Error = err.Error()
		return log, true
	}
	if status.TerminalGenerations == 0 {
		return log, false
	}
	rows := status.TerminalKeywordRows + status.TerminalFingerprintRows
	if input.dryRun {
		log.Action = localizef(
			"dry-run: would reclaim %d terminal generation(s) (%d rows)",
			"dry-run: terminal 世代 %d 個（%d 行）を reclaim します",
			status.TerminalGenerations, rows,
		)
		log.Metrics = map[string]int{"generations": status.TerminalGenerations, "rows": int(rows)}
		return log, true
	}
	result, err := c.searchProjection.ReclaimTerminalGenerations(ctx, apptypes.DefaultSearchProjectionBudget(), defaultProjectionRunOptions(), time.Now())
	if err != nil {
		log.Error = err.Error()
		return log, true
	}
	log.Metrics = map[string]int{"generations": len(result.Generations), "rows": int(result.DeletedRows)}
	if result.StopReason == "complete" {
		log.Action = localizef(
			"reclaimed %d terminal search-projection generation(s) (%d rows, ~%s logical)",
			"terminal search-projection 世代 %d 個を reclaim しました（%d 行、論理 ~%s）",
			len(result.Generations), result.DeletedRows, formatCompactBytes(uint64(max64(result.LogicalBytes, 0))),
		)
		return log, true
	}
	log.Action = localizef(
		"reclaimed %d rows from terminal search-projection generations (stopped: %s; re-run doctor --fix to continue)",
		"terminal search-projection 世代から %d 行を reclaim しました（停止: %s。続けるには doctor --fix を再実行）",
		result.DeletedRows, result.StopReason,
	)
	return log, true
}

func max64(v, floor int64) int64 {
	if v < floor {
		return floor
	}
	return v
}
