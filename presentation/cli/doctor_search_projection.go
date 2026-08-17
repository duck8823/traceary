package cli

import (
	"context"

	apptypes "github.com/duck8823/traceary/application/types"
)

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
			Name:    name,
			Status:  doctorStatusWarn,
			Message: Localize("completed search-projection family exceeds the configured index-family budget", "完了した search-projection ファミリが設定した index-family 予算を超えています"),
			Hint:    Localize("run `traceary store compact --projection-rebuild` with a smaller --index-family-bytes; CatchUp will not correct a complete generation", "`--index-family-bytes` を小さくして `traceary store compact --projection-rebuild` を実行してください。CatchUp は complete 世代を直しません"),
		}
	default:
		return doctorCheck{
			Name:    name,
			Status:  doctorStatusSkip,
			Message: Localize("search-projection budget verdict is unknown (index_family_within_budget=-1)", "search-projection の予算判定は不明です (index_family_within_budget=-1)"),
		}
	}
}
