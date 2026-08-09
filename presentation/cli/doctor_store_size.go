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
	payloadGrowthWarnBytes     = int64(512 << 20)
	projectionGrowthWarnBytes  = int64(256 << 20)
	doctorGrowthLatencyWarn    = 1500 * time.Millisecond
	doctorGrowthInspectTimeout = 2 * time.Second
)

type storeGrowthEvidence struct {
	DatabaseBytes, EventPayloadBytes, ProjectionBytes, ReclaimableBytes, FilesystemFreeBytes int64
	FilesystemFreeAvailable                                                                  bool
	MeasuredLatency                                                                          time.Duration
}

type storeFileSnapshot struct {
	Size            int64
	Regular, Exists bool
	Err             error
}

func inspectStoreFileSnapshot(path string, stat func(string) (os.FileInfo, error)) storeFileSnapshot {
	info, err := stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return storeFileSnapshot{}
		}
		return storeFileSnapshot{Err: err}
	}
	return storeFileSnapshot{Size: info.Size(), Regular: info.Mode().IsRegular(), Exists: true}
}

func (c *RootCLI) inspectStoreGrowthBudget(ctx context.Context, dbPath string, snapshot storeFileSnapshot) []doctorCheck {
	return c.inspectStoreGrowthBudgetWithClock(ctx, dbPath, snapshot, time.Now)
}

// inspectStoreGrowthBudgetWithClock returns the store-size check and, when the
// retired migration-032 family is still resident, a legacy-search-index check.
// Both come from one capacity report: the object traversal is the expensive
// part, and inspecting twice would double the cost of the whole doctor run.
func (c *RootCLI) inspectStoreGrowthBudgetWithClock(ctx context.Context, dbPath string, snapshot storeFileSnapshot, now func() time.Time) []doctorCheck {
	if snapshot.Err != nil {
		return []doctorCheck{{Name: "store-size", Status: doctorStatusWarn, Message: localizef("failed to inspect SQLite store metadata: %v", "SQLite store metadataを確認できません: %v", snapshot.Err)}}
	}
	if !snapshot.Exists {
		return []doctorCheck{{Name: "store-size", Status: doctorStatusPass, Message: Localize("SQLite store file does not exist yet (no size budget concern)", "SQLite ストアファイルはまだありません（サイズ予算の懸念なし）")}}
	}
	if !snapshot.Regular {
		return []doctorCheck{{Name: "store-size", Status: doctorStatusWarn, Message: Localize("SQLite store path is not a regular file", "SQLite store pathはregular fileではありません")}}
	}
	if c.capacityInspector == nil {
		return []doctorCheck{unknownStoreGrowthCheck(snapshot.Size, dbPath, "capacity inspector unavailable")}
	}
	inspectCtx, cancel := context.WithTimeout(ctx, doctorGrowthInspectTimeout)
	defer cancel()
	started := now()
	report, err := c.capacityInspector.InspectCapacity(inspectCtx)
	latency := now().Sub(started)
	if err != nil {
		return []doctorCheck{unknownStoreGrowthCheck(snapshot.Size, dbPath, "bounded capacity signals unavailable or timed out")}
	}
	evidence := storeGrowthEvidence{DatabaseBytes: report.DatabaseBytes, ReclaimableBytes: report.FreeBytes, MeasuredLatency: latency}
	if evidence.DatabaseBytes == 0 {
		evidence.DatabaseBytes = snapshot.Size
	}
	for _, payload := range report.PayloadClasses {
		evidence.EventPayloadBytes += payload.Bytes
	}
	legacyBytes := int64(0)
	for _, object := range report.Objects {
		name := strings.ToLower(object.Name)
		if strings.Contains(name, "search") || strings.Contains(name, "projection") {
			evidence.ProjectionBytes += object.Bytes
		}
		if isLegacySearchIndexObject(name) {
			legacyBytes += object.Bytes
		}
	}
	if free, freeErr := inspectDoctorDiskFree(dbPath); freeErr == nil {
		evidence.FilesystemFreeBytes = free
		evidence.FilesystemFreeAvailable = true
	}
	check := evaluateStoreGrowthBudget(evidence)
	check.FixCommand = "traceary store compact plan --db-path " + shellQuote(dbPath)
	return []doctorCheck{check, legacySearchIndexCheck(legacyBytes, dbPath)}
}

// isLegacySearchIndexObject matches the migration-032 family, including the
// FTS5 shadow tables (event_search_fts_data, _idx, _docsize, _config), and
// nothing from the bounded projection, whose objects are named
// search_projection_* / literal_search_*.
func isLegacySearchIndexObject(loweredName string) bool {
	return strings.HasPrefix(loweredName, "event_search_")
}

func legacySearchIndexCheck(bytes int64, dbPath string) doctorCheck {
	const name = "legacy-search-index"
	if bytes <= 0 {
		return doctorCheck{
			Name:    name,
			Status:  doctorStatusPass,
			Message: Localize("the retired legacy search index is not present", "退役済みの legacy search index は残っていません"),
		}
	}
	return doctorCheck{
		Name:   name,
		Status: doctorStatusWarn,
		Message: localizef(
			"the retired legacy search index is still resident and holds %s; nothing reads or maintains it",
			"退役済みの legacy search index が %s を占有したまま残っています。読み取りも更新もされていません",
			formatByteSize(bytes),
		),
		Hint: Localize(
			"run `traceary store search-retire` to drop it, then `traceary store compact plan` to return the bytes to the filesystem; dropping alone only moves pages to the free list",
			"`traceary store search-retire` で削除し、その後 `traceary store compact plan` でファイルサイズを回収してください。DROP だけではページが free list に戻るだけです",
		),
		FixCommand: "traceary store search-retire --db-path " + shellQuote(dbPath),
	}
}

func unknownStoreGrowthCheck(size int64, dbPath, reason string) doctorCheck {
	return doctorCheck{Name: "store-size", Status: doctorStatusWarn, Message: localizef("store growth signals are unknown (%s); database metadata size=%s alone is not enough to select remediation", "store growth signal は不明です (%s)。database metadata size=%s だけではremediationを選択できません", reason, formatByteSize(size)), Hint: Localize("retry bounded diagnostics without competing writers; preview the safe compaction plan before any mutation", "競合writerなしでbounded diagnosticsを再実行し、変更前にsafe compaction planをpreviewしてください"), FixCommand: "traceary store compact plan --db-path " + shellQuote(dbPath)}
}

func evaluateStoreGrowthBudget(e storeGrowthEvidence) doctorCheck {
	reasons := make([]string, 0, 5)
	if e.EventPayloadBytes >= payloadGrowthWarnBytes {
		reasons = append(reasons, "event_payload")
	}
	if e.ProjectionBytes >= projectionGrowthWarnBytes {
		reasons = append(reasons, "projection")
	}
	if e.ReclaimableBytes >= 256<<20 && ratioAtLeast(e.ReclaimableBytes, e.DatabaseBytes, 10) {
		reasons = append(reasons, "reclaimable")
	}
	if !e.FilesystemFreeAvailable {
		reasons = append(reasons, "headroom_unknown")
	} else if e.FilesystemFreeBytes <= compactionHeadroomThreshold(e.DatabaseBytes) {
		reasons = append(reasons, "headroom")
	}
	if e.MeasuredLatency >= doctorGrowthLatencyWarn {
		reasons = append(reasons, "latency")
	}
	if len(reasons) == 0 {
		return doctorCheck{Name: "store-size", Status: doctorStatusPass, Message: localizef("store growth signals are within budget: database=%s event_payload=%s projection=%s free=%s latency=%s", "store growth signal は予算内です: database=%s event_payload=%s projection=%s free=%s latency=%s", formatByteSize(e.DatabaseBytes), formatByteSize(e.EventPayloadBytes), formatByteSize(e.ProjectionBytes), formatDoctorFree(e), e.MeasuredLatency.Round(time.Millisecond))}
	}
	return doctorCheck{Name: "store-size", Status: doctorStatusWarn, Message: localizef("store growth warning (%s): database=%s event_payload=%s projection=%s free=%s measured_latency=%s", "store growth warning (%s): database=%s event_payload=%s projection=%s free=%s measured_latency=%s", strings.Join(reasons, ","), formatByteSize(e.DatabaseBytes), formatByteSize(e.EventPayloadBytes), formatByteSize(e.ProjectionBytes), formatDoctorFree(e), e.MeasuredLatency.Round(time.Millisecond)), Hint: Localize("preview safe compaction first, then follow the reviewed copy/preflight/scrub/compact/swap workflow; retain rollback artifacts until verification succeeds", "まずsafe compactionをpreviewし、その後review済みのcopy/preflight/scrub/compact/swap手順を実行してください。検証成功までrollback artifactを保持してください"), FixCommand: "traceary store compact plan"}
}

func formatDoctorFree(e storeGrowthEvidence) string {
	if !e.FilesystemFreeAvailable {
		return "unknown"
	}
	return formatByteSize(e.FilesystemFreeBytes)
}

func compactionHeadroomThreshold(databaseBytes int64) int64 {
	if databaseBytes > (int64(^uint64(0)>>1))/2 {
		return int64(^uint64(0) >> 1)
	}
	return maxInt64(1<<30, databaseBytes*2)
}

func ratioAtLeast(part, total, denominator int64) bool {
	if part <= 0 || total <= 0 || denominator <= 0 {
		return false
	}
	threshold := total / denominator
	if total%denominator > 0 {
		threshold++
	}
	return part >= threshold
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
func isLargeStoreForBoundedDoctor(snapshot storeFileSnapshot) bool {
	return snapshot.Err == nil && snapshot.Exists && snapshot.Regular && snapshot.Size >= doctorLargeStoreMetadataOnlyBytes
}

func boundedLargeStoreDoctorCheck(snapshot storeFileSnapshot, dbPath string) doctorCheck {
	if snapshot.Err != nil || !snapshot.Exists || !snapshot.Regular {
		return doctorCheck{
			Name:    "large-store-diagnostics",
			Status:  doctorStatusFail,
			Message: Localize("failed to use large SQLite store metadata snapshot", "大容量SQLite storeのmetadata snapshotを使用できません"),
		}
	}
	quoted := shellQuote(dbPath)
	return doctorCheck{
		Name:   "large-store-diagnostics",
		Status: doctorStatusWarn,
		Message: localizef(
			"bounded metadata-only doctor result for %s store: SQLite open, migrations, event bodies, command payloads, hook spools, credentials, and identifier samples were not read",
			"%s のストアに対する bounded metadata-only doctor 結果です。SQLite の open、migration、event body、command payload、hook spool、credential、identifier sample は読み取りませんでした",
			formatByteSize(snapshot.Size),
		),
		// Whether the retired legacy search index is present cannot be known
		// without opening SQLite, which this mode exists to avoid. The advice
		// is therefore unconditional, which is safe because search-retire is a
		// no-op on a store that no longer carries the family.
		Hint: localizef(
			"this is capacity-safe, not a lock diagnosis. Stop competing writers or use a reviewed bounded copy, then retry diagnostics. If this store predates v0.34 it may still carry the retired legacy search index: run `traceary store search-retire --db-path %s` (a no-op if already removed), then preview safe remediation with `traceary store compact plan --db-path %s`.",
			"これは capacity-safe な結果であり lock 診断ではありません。競合 writer を停止するかreview済みbounded copyを使って診断を再実行してください。v0.34 より前から使っているストアには退役済みの legacy search index が残っている可能性があります。`traceary store search-retire --db-path %s`（削除済みなら no-op）を実行してから、`traceary store compact plan --db-path %s`で安全なremediationをpreviewしてください。",
			quoted, quoted,
		),
		FixCommand: "traceary store search-retire --db-path " + quoted,
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
