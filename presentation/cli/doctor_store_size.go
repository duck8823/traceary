package cli

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
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
	checks := c.inspectStoreGrowthBudgetWithClock(ctx, dbPath, snapshot, time.Now)
	return append(checks, inspectCompactRollbackCopies(dbPath))
}

// inspectCompactRollbackCopies reports leftover <db>.rollback-<run> files.
// The copy is retained until the operator deletes it (#1827); doctor only names it.
func inspectCompactRollbackCopies(dbPath string) doctorCheck {
	const name = "compact-rollback-copy"
	if strings.TrimSpace(dbPath) == "" {
		return doctorCheck{
			Name:    name,
			Status:  doctorStatusSkip,
			Message: Localize("store path is empty, so compact rollback copies were not inspected", "store path が空のため compact rollback copy は検査しません"),
		}
	}
	matches, err := filepath.Glob(dbPath + ".rollback-*")
	if err != nil {
		return doctorCheck{
			Name:    name,
			Status:  doctorStatusWarn,
			Message: localizef("failed to inspect compact rollback copies: %v", "compact rollback copy を検査できません: %v", err),
		}
	}
	type retained struct {
		path string
		size int64
	}
	var copies []retained
	for _, match := range matches {
		info, statErr := os.Lstat(match)
		if statErr != nil || info == nil || !info.Mode().IsRegular() {
			continue
		}
		copies = append(copies, retained{path: match, size: info.Size()})
	}
	if len(copies) == 0 {
		return doctorCheck{
			Name:    name,
			Status:  doctorStatusPass,
			Message: Localize("no compact rollback copy is retained beside the store", "store の隣に compact rollback copy は残っていません"),
		}
	}
	parts := make([]string, 0, len(copies))
	var total int64
	for _, copy := range copies {
		total += copy.size
		parts = append(parts, fmt.Sprintf("%s (%s)", copy.path, formatByteSize(copy.size)))
	}
	return doctorCheck{
		Name:   name,
		Status: doctorStatusWarn,
		Message: localizef(
			"compact rollback copy still retained (%s total): %s",
			"compact rollback copy が残っています（合計 %s）: %s",
			formatByteSize(total),
			strings.Join(parts, ", "),
		),
		Hint: Localize(
			"Apply-time verification is not in-use proof. Keep the file until you accept the rewrite, then delete it. Deleting it gives up `traceary store compact rollback RUN_ID` for that run.",
			"apply 時の検証は実使用の正しさの証明ではありません。書き換えを受け入れるまで残し、その後削除してください。削除するとその run の `traceary store compact rollback RUN_ID` は使えなくなります。",
		),
	}
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
		// The retired family is reported by its own check, not as projection
		// growth. Counting it here would attribute 16 GiB of dead index to the
		// live projection's budget and point the operator at compaction, which
		// is the wrong remedy and the wrong first step.
		if isLegacySearchIndexObject(name) {
			legacyBytes += object.Bytes
			continue
		}
		if strings.Contains(name, "search") || strings.Contains(name, "projection") {
			evidence.ProjectionBytes += object.Bytes
		}
	}
	if free, freeErr := inspectDoctorDiskFree(dbPath); freeErr == nil {
		evidence.FilesystemFreeBytes = free
		evidence.FilesystemFreeAvailable = true
	}
	check := evaluateStoreGrowthBudget(evidence)
	check.FixCommand = "traceary store compact --db-path " + shellQuote(dbPath)
	return []doctorCheck{check, legacySearchIndexCheck(legacyBytes, dbPath)}
}

// isLegacySearchIndexObject matches the migration-032 family: the two tables,
// the FTS5 shadow tables (event_search_fts_data, _idx, _docsize, _config), and
// the implicit index behind event_search_documents.event_id UNIQUE, which
// dbstat reports as sqlite_autoindex_event_search_documents_1 — hence Contains
// rather than HasPrefix, since that name holds millions of event IDs on the
// stores this check exists for.
//
// No bounded-projection object contains this substring; they are named
// search_projection_* and literal_search_*.
func isLegacySearchIndexObject(loweredName string) bool {
	return strings.Contains(loweredName, "event_search_")
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
			"run `traceary store compact` to drop the retired index during the rewrite and return the bytes to the filesystem",
			"`traceary store compact` で退役済み index を書き換え時に落とし、ファイルサイズを回収してください",
		),
		FixCommand: "traceary store compact --db-path " + shellQuote(dbPath),
	}
}

func unknownStoreGrowthCheck(size int64, dbPath, reason string) doctorCheck {
	return doctorCheck{Name: "store-size", Status: doctorStatusWarn, Message: localizef("store growth signals are unknown (%s); database metadata size=%s alone is not enough to select remediation", "store growth signal は不明です (%s)。database metadata size=%s だけではremediationを選択できません", reason, formatByteSize(size)), Hint: Localize("retry bounded diagnostics without competing writers; run `traceary store compact` to rewrite the file", "競合writerなしでbounded diagnosticsを再実行し、`traceary store compact` でファイルを書き換えてください"), FixCommand: "traceary store compact --db-path " + shellQuote(dbPath)}
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
	return doctorCheck{Name: "store-size", Status: doctorStatusWarn, Message: localizef("store growth warning (%s): database=%s event_payload=%s projection=%s free=%s measured_latency=%s", "store growth warning (%s): database=%s event_payload=%s projection=%s free=%s measured_latency=%s", strings.Join(reasons, ","), formatByteSize(e.DatabaseBytes), formatByteSize(e.EventPayloadBytes), formatByteSize(e.ProjectionBytes), formatDoctorFree(e), e.MeasuredLatency.Round(time.Millisecond)), Hint: Localize("rewrite with `traceary store compact` (copy-filter, body discard, VACUUM INTO, atomic exchange). This is not a preview. Keep the rollback file until you accept the result (`traceary store compact rollback RUN_ID`). Do not run in-place VACUUM", "書き換えは `traceary store compact` です（copy-filter、本文破棄、VACUUM INTO、atomic exchange）。preview ではありません。受け入れるまで rollback ファイルを残してください（`traceary store compact rollback RUN_ID`）。in-place VACUUM は使わないでください"), FixCommand: "traceary store compact"}
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
		// without opening SQLite, which this mode exists to avoid. compact
		// drops the family if it is still there.
		Hint: localizef(
			"this is capacity-safe, not a lock diagnosis. Stop competing writers or use a reviewed bounded copy, then retry diagnostics. If this store predates v0.34 it may still carry the retired legacy search index: run `traceary store compact --db-path %s`.",
			"これは capacity-safe な結果であり lock 診断ではありません。競合 writer を停止するかreview済みbounded copyを使って診断を再実行してください。v0.34 より前から使っているストアには退役済みの legacy search index が残っている可能性があります。`traceary store compact --db-path %s` を実行してください。",
			quoted,
		),
		FixCommand: "traceary store compact --db-path " + quoted,
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
			"rewrite with `traceary store compact --db-path PATH` (copy-filter, body discard, VACUUM INTO, atomic exchange). This is not a preview. Rollback with `traceary store compact rollback RUN_ID`. Do not run in-place VACUUM or infer cleanup solely from file size.",
			"書き換えは `traceary store compact --db-path PATH` です（copy-filter、本文破棄、VACUUM INTO、atomic exchange）。preview ではありません。取り消すには `traceary store compact rollback RUN_ID`。in-place VACUUM や file size だけの判断はしないでください。",
		),
		FixCommand: "traceary store compact --db-path " + shellQuote(dbPath),
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
