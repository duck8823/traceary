package cli

import (
	"fmt"
	"os"
)

// storeSizeWarnBytes is the on-disk size above which multi-GB cold opens
// routinely approach host hook budgets (10s packaged). Measured dogfood
// stores around 2.4 GB already spend 2–4 s on cold open alone.
const storeSizeWarnBytes int64 = 1 << 30 // 1 GiB

const (
	doctorModeMetadataOnlyLargeStore = "metadata_only_large_store"
	// doctorLargeStoreMetadataOnlyBytes is an operational threshold, not a
	// retention target or a store-size limit. Above it, the default doctor
	// command returns a finite metadata-only result instead of opening SQLite
	// and traversing content-bearing diagnostics.
	doctorLargeStoreMetadataOnlyBytes int64 = 2 << 30 // 2 GiB
)

// isLargeStoreForBoundedDoctor uses only filesystem metadata. It must remain
// independent of SQLite so the bounded doctor outcome is still available when
// a writer holds the database lock or the store is otherwise unhealthy.
func isLargeStoreForBoundedDoctor(dbPath string) bool {
	info, err := os.Stat(dbPath)
	return err == nil && info.Mode().IsRegular() && info.Size() >= doctorLargeStoreMetadataOnlyBytes
}

func boundedLargeStoreDoctorCheck(dbPath string) doctorCheck {
	info, err := os.Stat(dbPath)
	if err != nil {
		return doctorCheck{
			Name:    "large-store-diagnostics",
			Status:  doctorStatusFail,
			Message: localizef("failed to stat large SQLite store metadata: %v", "大容量 SQLite ストアの metadata を確認できませんでした: %v", err),
		}
	}
	return doctorCheck{
		Name:   "large-store-diagnostics",
		Status: doctorStatusWarn,
		Message: localizef(
			"bounded metadata-only doctor result for %s store: SQLite open, migrations, event bodies, command payloads, hook spools, credentials, and identifier samples were not read",
			"%s のストアに対する bounded metadata-only doctor 結果です。SQLite の open、migration、event body、command payload、hook spool、credential、identifier sample は読み取りませんでした",
			formatByteSize(info.Size()),
		),
		Hint: Localize(
			"this is capacity-safe, not a lock diagnosis. If an application needs a content diagnostic, first stop competing writers or use a reviewed bounded copy; then run the specific read command. Preview safe remediation with `traceary store gc --dry-run` and archive before applying retention.",
			"これは capacity-safe な結果であり lock 診断ではありません。content diagnostic が必要なら、競合 writer を停止するか、review 済みの bounded copy を使用してから個別の read command を実行してください。安全な remediation は `traceary store gc --dry-run` でプレビューし、retention を適用する前に archive を作成してください。",
		),
		FixCommand: "traceary store gc --dry-run",
	}
}

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
