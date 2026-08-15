package cli

import (
	"context"

	apptypes "github.com/duck8823/traceary/application/types"
)

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
			Hint:    Localize("run `traceary store search-projection start` with a smaller --index-family-bytes; CatchUp will not correct a complete generation", "`--index-family-bytes` を小さくして `traceary store search-projection start` を実行してください。CatchUp は complete 世代を直しません"),
		}
	default:
		return doctorCheck{
			Name:    name,
			Status:  doctorStatusSkip,
			Message: Localize("search-projection budget verdict is unknown (index_family_within_budget=-1)", "search-projection の予算判定は不明です (index_family_within_budget=-1)"),
		}
	}
}
