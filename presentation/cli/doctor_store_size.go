package cli

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"
)

// storeSizeWarnBytes is the on-disk size above which multi-GB cold opens
// routinely approach host hook budgets (10s packaged). Measured dogfood
// stores around 2.4 GB already spend 2–4 s on cold open alone.
const storeSizeWarnBytes int64 = 1 << 30 // 1 GiB

const (
	payloadGrowthWarnBytes    = int64(512 << 20)
	projectionGrowthWarnBytes = int64(256 << 20)
	doctorGrowthLatencyWarn   = 2 * time.Second
)

type storeGrowthEvidence struct {
	DatabaseBytes, PayloadBytes, ProjectionBytes, ReclaimableBytes, FilesystemFreeBytes int64
	MeasuredLatency                                                                     time.Duration
}

func (c *RootCLI) inspectStoreGrowthBudget(ctx context.Context, dbPath string) doctorCheck {
	info, err := os.Stat(dbPath)
	if err != nil {
		return inspectStoreSizeBudget(dbPath)
	}
	if c.capacityInspector == nil {
		return unknownStoreGrowthCheck(info.Size(), dbPath, "capacity inspector unavailable")
	}
	inspectCtx, cancel := context.WithTimeout(ctx, doctorGrowthLatencyWarn)
	defer cancel()
	started := time.Now()
	report, err := c.capacityInspector.InspectCapacity(inspectCtx)
	latency := time.Since(started)
	if err != nil {
		return unknownStoreGrowthCheck(info.Size(), dbPath, "bounded capacity signals unavailable or timed out")
	}
	evidence := storeGrowthEvidence{DatabaseBytes: report.DatabaseBytes, ReclaimableBytes: report.FreeBytes, MeasuredLatency: latency}
	if evidence.DatabaseBytes == 0 {
		evidence.DatabaseBytes = info.Size()
	}
	for _, payload := range report.PayloadClasses {
		evidence.PayloadBytes += payload.Bytes
	}
	for _, object := range report.Objects {
		name := strings.ToLower(object.Name)
		if strings.Contains(name, "search") || strings.Contains(name, "projection") {
			evidence.ProjectionBytes += object.Bytes
		}
	}
	if free, freeErr := inspectDoctorDiskFree(dbPath); freeErr == nil {
		evidence.FilesystemFreeBytes = free
	}
	check := evaluateStoreGrowthBudget(evidence)
	check.FixCommand = "traceary store compact plan --db-path " + shellQuote(dbPath)
	return check
}

func unknownStoreGrowthCheck(size int64, dbPath, reason string) doctorCheck {
	return doctorCheck{Name: "store-size", Status: doctorStatusWarn, Message: localizef("store growth signals are unknown (%s); database metadata size=%s alone is not enough to select remediation", "store growth signal は不明です (%s)。database metadata size=%s だけではremediationを選択できません", reason, formatByteSize(size)), Hint: Localize("retry bounded diagnostics without competing writers; preview the safe compaction plan before any mutation", "競合writerなしでbounded diagnosticsを再実行し、変更前にsafe compaction planをpreviewしてください"), FixCommand: "traceary store compact plan --db-path " + shellQuote(dbPath)}
}

func evaluateStoreGrowthBudget(e storeGrowthEvidence) doctorCheck {
	reasons := make([]string, 0, 5)
	if e.DatabaseBytes >= storeSizeWarnBytes {
		reasons = append(reasons, "database")
	}
	if e.PayloadBytes >= payloadGrowthWarnBytes {
		reasons = append(reasons, "payload")
	}
	if e.ProjectionBytes >= projectionGrowthWarnBytes {
		reasons = append(reasons, "projection")
	}
	if e.ReclaimableBytes >= 256<<20 && e.ReclaimableBytes*10 >= e.DatabaseBytes {
		reasons = append(reasons, "reclaimable")
	}
	if e.FilesystemFreeBytes > 0 && e.FilesystemFreeBytes < maxInt64(1<<30, e.DatabaseBytes*2) {
		reasons = append(reasons, "headroom")
	}
	if e.MeasuredLatency >= doctorGrowthLatencyWarn {
		reasons = append(reasons, "latency")
	}
	if len(reasons) == 0 {
		return doctorCheck{Name: "store-size", Status: doctorStatusPass, Message: localizef("store growth signals are within budget: database=%s payload=%s projection=%s free=%s latency=%s", "store growth signal は予算内です: database=%s payload=%s projection=%s free=%s latency=%s", formatByteSize(e.DatabaseBytes), formatByteSize(e.PayloadBytes), formatByteSize(e.ProjectionBytes), formatByteSize(e.FilesystemFreeBytes), e.MeasuredLatency.Round(time.Millisecond))}
	}
	return doctorCheck{Name: "store-size", Status: doctorStatusWarn, Message: localizef("store growth warning (%s): database=%s payload=%s projection=%s free=%s measured_latency=%s", "store growth warning (%s): database=%s payload=%s projection=%s free=%s measured_latency=%s", strings.Join(reasons, ","), formatByteSize(e.DatabaseBytes), formatByteSize(e.PayloadBytes), formatByteSize(e.ProjectionBytes), formatByteSize(e.FilesystemFreeBytes), e.MeasuredLatency.Round(time.Millisecond)), Hint: Localize("preview safe compaction first, then follow the reviewed copy/preflight/scrub/compact/swap workflow; retain rollback artifacts until verification succeeds", "まずsafe compactionをpreviewし、その後review済みのcopy/preflight/scrub/compact/swap手順を実行してください。検証成功までrollback artifactを保持してください"), FixCommand: "traceary store compact plan"}
}

func maxInt64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}

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
			"this is capacity-safe, not a lock diagnosis. Stop competing writers or use a reviewed bounded copy, then retry diagnostics. Preview safe remediation with `traceary store compact plan --db-path PATH`.",
			"これは capacity-safe な結果であり lock 診断ではありません。競合 writer を停止するかreview済みbounded copyを使って診断を再実行し、`traceary store compact plan --db-path PATH`で安全なremediationをpreviewしてください。",
		),
		FixCommand: "traceary store compact plan",
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
			"preview `traceary store compact plan --db-path PATH`; do not run in-place VACUUM or infer cleanup solely from file size.",
			"`traceary store compact plan --db-path PATH`でpreviewしてください。in-place VACUUMやfile sizeだけに基づくcleanup判断は行わないでください。",
		),
		FixCommand: "traceary store compact plan --db-path " + shellQuote(dbPath),
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
