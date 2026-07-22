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
		if c.fileRetentionCapacity == nil {
			checks = append(checks, doctorCheck{Name: name, Status: doctorStatusFail, Message: Localize("file retention capacity inspection is not configured", "file retention の容量確認が設定されていません")})
			continue
		}
		statuses, err := c.fileRetentionCapacity.InspectCapacity(ctx, apptypes.FileRetentionCapacityRequest{
			DatabasePath: dbPath,
			Classes:      []apptypes.FileRetentionInventoryRequest{{Class: request.class, Root: root}},
		})
		if err != nil {
			checks = append(checks, doctorCheck{Name: name, Status: doctorStatusWarn, Message: localizef("failed to inspect %s capacity: %v", "%s capacity の確認に失敗しました: %v", request.class, err), Hint: fileRetentionCapacityHint(request.class, root)})
			continue
		}
		if len(statuses) != 1 {
			checks = append(checks, doctorCheck{Name: name, Status: doctorStatusWarn, Message: localizef("failed to inspect %s capacity: expected one status, got %d", "%s capacity の確認に失敗しました: status は1件の想定ですが %d 件でした", request.class, len(statuses)), Hint: fileRetentionCapacityHint(request.class, root)})
			continue
		}
		checks = append(checks, fileRetentionCapacityDoctorCheck(name, statuses[0]))
	}
	return checks
}

func fileRetentionCapacityDoctorCheck(name string, status apptypes.FileRetentionCapacityStatus) doctorCheck {
	logical := fmt.Sprintf("%d", status.LogicalBytes)
	if status.LogicalOverflow {
		logical = "overflow"
	}
	allocated := fmt.Sprintf("%d", status.AllocatedBytes)
	if status.AllocatedOverflow {
		allocated = "overflow"
	} else if !status.AllocatedKnown {
		allocated = "partially-known:" + allocated
	}
	floor := normalizeTabularColumn(status.FloorRelativePath)
	if floor == "" {
		floor = "none"
	}
	checkStatus := doctorStatusPass
	rootApplyState := status.RootAccess.ApplyState
	if rootApplyState == "" {
		rootApplyState = apptypes.FileRetentionRootApplyUnsupported
	}
	if status.State == "indeterminate" || status.BlockingCount > 0 || status.UnverifiedCount > 0 || !status.AllocatedKnown || status.LogicalOverflow || status.AllocatedOverflow || rootApplyState != apptypes.FileRetentionRootApplyEligible {
		checkStatus = doctorStatusWarn
	}
	return doctorCheck{
		Name:   name,
		Status: checkStatus,
		Message: localizef(
			"read-only %s capacity-root inspection: state=%s apply_root=%s caller_owned=%t group_or_other_writable=%t files=%d verified=%d unverified=%d blockers=%d logical_bytes=%s allocated_bytes=%s floor=%s; automatic cleanup is disabled",
			"読み取り専用 %s capacity-root 検査: state=%s apply_root=%s caller_owned=%t group_or_other_writable=%t files=%d verified=%d unverified=%d blockers=%d logical_bytes=%s allocated_bytes=%s floor=%s。自動 cleanup は無効です",
			status.Class, status.State, rootApplyState, status.RootAccess.CallerOwned, status.RootAccess.GroupOrOtherWritable, status.FileCount, status.VerifiedCount, status.UnverifiedCount, status.BlockingCount, logical, allocated, floor,
		),
		Hint: fileRetentionCapacityHint(status.Class, status.Root),
	}
}

func fileRetentionCapacityHint(class, root string) string {
	command := renderShellCommand([]string{"traceary", "store", "retention", "files", "plan", "--" + class + "-root", root, "..."})
	return localizef(
		"capacity changes remain manual and opt-in; review `%s` before exact apply",
		"capacity 変更は手動 opt-in のままです。exact apply の前に `%s` を確認してください",
		command,
	)
}
