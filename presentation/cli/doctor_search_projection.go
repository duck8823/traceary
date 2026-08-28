package cli

import (
	"context"

	apptypes "github.com/duck8823/traceary/application/types"
)

func (c *RootCLI) inspectSearchProjectionTerminalRows(ctx context.Context) doctorCheck {
	if c.searchProjection == nil {
		return doctorCheck{
			Name:    "search-projection-terminal-rows",
			Status:  doctorStatusSkip,
			Message: Localize("search projection usecase is not configured", "search projection usecase が設定されていません"),
		}
	}
	status, err := c.searchProjection.Inspect(ctx)
	return searchProjectionTerminalRowsDoctorCheck(status, err)
}

func searchProjectionTerminalRowsDoctorCheck(status apptypes.SearchProjectionStatus, err error) doctorCheck {
	const name = "search-projection-terminal-rows"
	if err != nil {
		return doctorCheck{
			Name:    name,
			Status:  doctorStatusWarn,
			Message: localizef("failed to inspect terminal search-projection rows: %v", "terminal search-projection 行を確認できません: %v", err),
		}
	}
	if status.TerminalGenerations == 0 {
		return doctorCheck{
			Name:    name,
			Status:  doctorStatusPass,
			Message: Localize("no terminal search-projection generations hold derived rows", "terminal な search-projection 世代は derived 行を保持していません"),
		}
	}
	logical := status.TerminalKeywordLogicalBytes + status.TerminalFingerprintLogicalBytes
	if logical < 0 {
		logical = 0
	}
	return doctorCheck{
		Name:   name,
		Status: doctorStatusWarn,
		Message: localizef(
			"%d terminal generation(s) still hold %d keyword rows / %d fingerprint rows (~%s logical)",
			"%d 個の terminal 世代が keyword %d 行 / fingerprint %d 行（論理 ~%s）を保持しています",
			status.TerminalGenerations, status.TerminalKeywordRows, status.TerminalFingerprintRows, formatCompactBytes(uint64(logical)),
		),
		Hint: apptypes.SearchProjectionRecoveryCommand,
	}
}

func (c *RootCLI) inspectSearchProjectionParked(ctx context.Context) doctorCheck {
	if c.searchProjection == nil {
		return doctorCheck{
			Name:    "search-projection-parked",
			Status:  doctorStatusSkip,
			Message: Localize("search projection usecase is not configured", "search projection usecase が設定されていません"),
		}
	}
	status, err := c.searchProjection.ControlStatus(ctx)
	return searchProjectionParkedDoctorCheck(status, err)
}

func searchProjectionParkedDoctorCheck(status apptypes.SearchProjectionControlStatus, err error) doctorCheck {
	const name = "search-projection-parked"
	if err != nil {
		return doctorCheck{
			Name:    name,
			Status:  doctorStatusWarn,
			Message: localizef("failed to inspect search-projection parked state: %v", "search-projection の parked 状態を確認できません: %v", err),
		}
	}
	notice := apptypes.SearchProjectionStatus{
		State:        status.State,
		Phase:        status.Phase,
		ConfigHash:   status.ConfigHash,
		FailureClass: status.FailureClass,
		Origin:       status.Origin,
	}
	notice.ApplyParkedNotice(apptypes.DefaultSearchProjectionBudget().ConfigHash())
	if notice.ParkedReason == "" {
		return doctorCheck{
			Name:    name,
			Status:  doctorStatusSkip,
			Message: Localize("search projection is not parked", "search projection は parked ではありません"),
		}
	}
	// Automatic stale-hash generations are not operator-parked: the next
	// store open replaces them (#1861). Do not advertise start/resume.
	if notice.RecoveryCommand == "" {
		return doctorCheck{
			Name:    name,
			Status:  doctorStatusSkip,
			Message: notice.ParkedReason,
		}
	}
	return doctorCheck{
		Name:    name,
		Status:  doctorStatusWarn,
		Message: notice.ParkedReason,
		Hint:    notice.RecoveryCommand,
	}
}

func (c *RootCLI) inspectSearchProjectionBudget(ctx context.Context) doctorCheck {
	if c.searchProjection == nil {
		return doctorCheck{
			Name:    "search-projection-budget",
			Status:  doctorStatusSkip,
			Message: Localize("search projection usecase is not configured", "search projection usecase が設定されていません"),
		}
	}
	status, err := c.searchProjection.ControlStatus(ctx)
	return searchProjectionBudgetDoctorCheck(status, err)
}

func searchProjectionBudgetDoctorCheck(status apptypes.SearchProjectionControlStatus, err error) doctorCheck {
	const name = "search-projection-budget"
	if err != nil {
		return doctorCheck{
			Name:    name,
			Status:  doctorStatusWarn,
			Message: localizef("failed to inspect search-projection budget verdict: %v", "search-projection の予算判定を確認できません: %v", err),
		}
	}
	if status.State != "complete" {
		return doctorCheck{
			Name:    name,
			Status:  doctorStatusSkip,
			Message: localizef("search projection state is %s; budget verdict is only reported for a complete generation", "search projection の state は %s です。予算判定は complete 世代でのみ報告します", status.State),
		}
	}
	switch status.IndexFamilyWithinBudget {
	case 1:
		return doctorCheck{
			Name:    name,
			Status:  doctorStatusPass,
			Message: Localize("completed search-projection family is within the configured index-family budget", "完了した search-projection ファミリは設定した index-family 予算以下です"),
		}
	case 0:
		return doctorCheck{
			Name:   name,
			Status: doctorStatusWarn,
			Message: Localize(
				"completed search-projection family exceeds the configured index-family budget; search_projection_session_keywords (and its autoindex) is counted, corpus-proportional, and not evictable",
				"完了した search-projection ファミリが設定した index-family 予算を超えています。search_projection_session_keywords（とその autoindex）は計上され、コーパス比例で evict できません",
			),
			Hint: Localize(
				"session keywords bound is one complete generation, cleaned when that generation is replaced; run `traceary store compact --projection-rebuild` with a smaller --index-family-bytes to shrink the evictable recent tier. CatchUp will not correct a complete generation",
				"session keywords の上限は complete な 1 世代分で、世代が置き換わると掃除されます。evict できる recent ティアを縮めるには `--index-family-bytes` を小さくして `traceary store compact --projection-rebuild` を実行してください。CatchUp は complete 世代を直しません",
			),
		}
	default:
		return doctorCheck{
			Name:    name,
			Status:  doctorStatusSkip,
			Message: Localize("search-projection budget verdict is unknown (index_family_within_budget=-1)", "search-projection の予算判定は不明です (index_family_within_budget=-1)"),
		}
	}
}
