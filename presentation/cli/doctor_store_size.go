package cli

import (
	"fmt"
	"os"
)

// storeSizeWarnBytes is the on-disk size above which multi-GB cold opens
// routinely approach host hook budgets (10s packaged). Measured dogfood
// stores around 2.4 GB already spend 2–4 s on cold open alone.
const storeSizeWarnBytes int64 = 1 << 30 // 1 GiB

func inspectStoreSizeBudget(dbPath string) doctorCheck {
	const name = "store-size"
	info, err := os.Stat(dbPath)
	if err != nil {
		if os.IsNotExist(err) {
			return doctorCheck{
				Name:    name,
				Status:  doctorStatusPass,
				Message: Localize("SQLite store file does not exist yet (no size budget concern)", "SQLite ストアファイルはまだありません（サイズ予算の懸念なし）"),
			}
		}
		return doctorCheck{
			Name:    name,
			Status:  doctorStatusWarn,
			Message: localizef("failed to inspect SQLite store size: %v", "SQLite ストアサイズの検査に失敗しました: %v", err),
		}
	}
	size := info.Size()
	if size < storeSizeWarnBytes {
		return doctorCheck{
			Name:   name,
			Status: doctorStatusPass,
			Message: localizef(
				"SQLite store size is within the hook cold-open budget: %s",
				"SQLite ストアサイズは hook cold-open 予算内です: %s",
				formatByteSize(size),
			),
		}
	}
	return doctorCheck{
		Name:   name,
		Status: doctorStatusWarn,
		Message: localizef(
			"SQLite store is large (%s); cold opens can consume several seconds of the host hook budget and raise timeout-kill / spool backlog risk",
			"SQLite ストアが大きいです (%s)。cold open が host hook budget の数秒を消費し、timeout-kill / spool backlog のリスクが上がります",
			formatByteSize(size),
		),
		Hint: Localize(
			"run `traceary store gc --dry-run` then apply when safe; archive-before-GC is tracked separately. Prefer keeping the live store under ~1 GiB for multi-host dogfood.",
			"`traceary store gc --dry-run` で確認後、安全なら適用してください。archive-before-GC は別 issue です。multi-host dogfood では live store をおおよそ 1 GiB 未満に保つことを推奨します。",
		),
		FixCommand: "traceary store gc --dry-run",
	}
}

func formatByteSize(size int64) string {
	const (
		kib = 1024
		mib = 1024 * kib
		gib = 1024 * mib
	)
	switch {
	case size >= gib:
		return fmt.Sprintf("%.1f GiB", float64(size)/float64(gib))
	case size >= mib:
		return fmt.Sprintf("%.1f MiB", float64(size)/float64(mib))
	case size >= kib:
		return fmt.Sprintf("%.1f KiB", float64(size)/float64(kib))
	default:
		return fmt.Sprintf("%d B", size)
	}
}
