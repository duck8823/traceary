package cli

import (
	"context"
	"fmt"
	"strings"

	apptypes "github.com/duck8823/traceary/application/types"
)

// inspectFileRetentionCapacity reports opt-in archive/backup roots without creating or applying a plan.
func (c *RootCLI) inspectFileRetentionCapacity(ctx context.Context, dbPath, archiveRoot, backupRoot string) []doctorCheck {
	type rootRequest struct {
		class string
		root  string
	}
	requests := []rootRequest{{class: "archive", root: archiveRoot}, {class: "backup", root: backupRoot}}
	checks := make([]doctorCheck, 0, len(requests))
	for _, request := range requests {
		if strings.TrimSpace(request.root) == "" {
			continue
		}
		name := request.class + "-capacity-retention"
		root, err := resolveRequiredAbsolutePath(request.root)
		if err != nil {
			checks = append(checks, doctorCheck{Name: name, Status: doctorStatusFail, Message: localizef("failed to resolve %s capacity root: %v", "%s capacity root の解決に失敗しました: %v", request.class, err)})
			continue
		}
		if c.fileRetention == nil {
			checks = append(checks, doctorCheck{Name: name, Status: doctorStatusFail, Message: Localize("file retention capacity inspection is not configured", "file retention の容量確認が設定されていません")})
			continue
		}
		statuses, err := c.fileRetention.InspectCapacity(ctx, apptypes.FileRetentionCapacityRequest{
			DatabasePath: dbPath,
			Classes:      []apptypes.FileRetentionInventoryRequest{{Class: request.class, Root: root}},
		})
		if err != nil || len(statuses) != 1 {
			checks = append(checks, doctorCheck{Name: name, Status: doctorStatusWarn, Message: localizef("failed to inspect %s capacity: %v", "%s capacity の確認に失敗しました: %v", request.class, err), Hint: fileRetentionCapacityHint(request.class, root)})
			continue
		}
		checks = append(checks, fileRetentionCapacityDoctorCheck(name, statuses[0]))
	}
	return checks
}

func fileRetentionCapacityDoctorCheck(name string, status apptypes.FileRetentionCapacityStatus) doctorCheck {
	allocated := fmt.Sprintf("%d", status.AllocatedBytes)
	if !status.AllocatedKnown {
		allocated = "partially-known:" + allocated
	}
	floor := status.FloorRelativePath
	if floor == "" {
		floor = "none"
	}
	checkStatus := doctorStatusPass
	if status.State == "indeterminate" || status.BlockingCount > 0 || status.UnverifiedCount > 0 {
		checkStatus = doctorStatusWarn
	}
	return doctorCheck{
		Name:   name,
		Status: checkStatus,
		Message: localizef(
			"read-only %s capacity: state=%s files=%d verified=%d unverified=%d blockers=%d logical_bytes=%d allocated_bytes=%s floor=%s; automatic cleanup is disabled",
			"読み取り専用 %s capacity: state=%s files=%d verified=%d unverified=%d blockers=%d logical_bytes=%d allocated_bytes=%s floor=%s。自動 cleanup は無効です",
			status.Class, status.State, status.FileCount, status.VerifiedCount, status.UnverifiedCount, status.BlockingCount, status.LogicalBytes, allocated, floor,
		),
		Hint: fileRetentionCapacityHint(status.Class, status.Root),
	}
}

func fileRetentionCapacityHint(class, root string) string {
	return localizef(
		"capacity changes remain manual and opt-in; review `traceary store retention files plan --%s-root %s ...` before exact apply",
		"capacity 変更は手動 opt-in のままです。exact apply の前に `traceary store retention files plan --%s-root %s ...` を確認してください",
		class, root,
	)
}
