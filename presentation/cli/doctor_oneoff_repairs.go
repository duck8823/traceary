package cli

import (
	"context"

	apptypes "github.com/duck8823/traceary/application/types"
)

func skippedOneOffRepairsCheck() doctorCheck {
	return doctorCheck{
		Name:   "one-off-repairs",
		Status: doctorStatusSkip,
		Message: Localize(
			"default doctor is filesystem-metadata-only for stores at or above 2 GiB; run doctor --fix to apply retired one-off repairs (can take minutes)",
			"2 GiB 以上の store では default doctor は filesystem metadata のみです。retired one-off repair の適用は doctor --fix です（数分かかることがあります）",
		),
		FixCommand: "traceary doctor --fix",
	}
}

func (c *RootCLI) inspectOneOffRepairs(ctx context.Context) doctorCheck {
	const name = "one-off-repairs"
	if c.storeManagement == nil {
		return doctorCheck{
			Name:    name,
			Status:  doctorStatusSkip,
			Message: Localize("store management is not configured", "store management が設定されていません"),
		}
	}
	states, err := c.storeManagement.InspectOneOffRepairRetirement(ctx)
	if err != nil {
		return doctorCheck{
			Name:    name,
			Status:  doctorStatusWarn,
			Message: localizef("failed to inspect one-off repairs: %v", "one-off repair の確認に失敗しました: %v", err),
		}
	}
	message := localizef(
		"epoch-zero-hook-usage=%s workspace-observations=%s",
		"epoch-zero-hook-usage=%s workspace-observations=%s",
		states.Epoch,
		states.Workspace,
	)
	// never-ran still needs doctor --fix so 078 can stamp schema_migrations.
	if states.Epoch != apptypes.OneOffRepairRetired || states.Workspace == apptypes.OneOffRepairOutstanding {
		return doctorCheck{
			Name:       name,
			Status:     doctorStatusWarn,
			Message:    message,
			Hint:       Localize("apply with doctor --fix after reviewing a backup", "backup を確認したあと doctor --fix で適用してください"),
			FixCommand: "traceary doctor --fix",
		}
	}
	return doctorCheck{
		Name:    name,
		Status:  doctorStatusPass,
		Message: message,
	}
}
