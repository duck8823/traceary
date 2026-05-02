package cli

import (
	"context"
	"fmt"

	apptypes "github.com/duck8823/traceary/application/types"
	domtypes "github.com/duck8823/traceary/domain/types"
)

func (c *RootCLI) inspectCodexMemoryActivationStatus(ctx context.Context, projectDir string) *doctorCheck {
	if c.memory == nil {
		return nil
	}
	criteria := codexActivationStatusCriteria(ctx, projectDir)
	status, err := c.memory.ActivationStatus(ctx, criteria)
	if err != nil {
		return &doctorCheck{
			Name:    "codex-memory-activation",
			Status:  doctorStatusFail,
			Message: localizef("failed to inspect Codex memory activation: %v", "Codex memory activation の確認に失敗しました: %v", err),
		}
	}
	commands := memoryActivationCommands(criteria)
	message := fmt.Sprintf("%s: %s", status.TargetPath, status.Message)
	check := doctorCheck{
		Name:    "codex-memory-activation",
		Status:  doctorStatusPass,
		Message: message,
	}
	switch status.State {
	case apptypes.MemoryActivationStatusInSync:
		check.Status = doctorStatusPass
		check.Message = localizef("Codex memory activation is in sync at %s (%d accepted memories)", "Codex memory activation は %s で同期済みです (%d accepted memories)", status.TargetPath, status.ActivatedCount)
	case apptypes.MemoryActivationStatusMissing:
		check.Status = doctorStatusWarn
		check.Hint = "activate accepted memories into Codex native memory"
		check.FixCommand = commands.Apply
		check.Message = localizef(
			"Codex memory activation is missing at %s (%d accepted memories). Preview with `%s`, then apply with `%s`",
			"Codex memory activation が %s にありません (%d accepted memories)。`%s` で確認してから `%s` で反映してください",
			status.TargetPath,
			status.ActivatedCount,
			commands.DryRun,
			commands.Apply,
		)
	case apptypes.MemoryActivationStatusStale:
		check.Status = doctorStatusWarn
		check.Hint = "refresh Codex native memory activation"
		check.FixCommand = commands.Apply
		check.Message = localizef(
			"Codex memory activation is stale at %s (%d accepted memories). Preview with `%s`, then refresh with `%s`",
			"Codex memory activation は %s で stale です (%d accepted memories)。`%s` で確認してから `%s` で更新してください",
			status.TargetPath,
			status.ActivatedCount,
			commands.DryRun,
			commands.Apply,
		)
	case apptypes.MemoryActivationStatusInvalid:
		check.Status = doctorStatusFail
		check.Hint = "inspect the Codex memory target before applying Traceary activation"
		check.Message = localizef(
			"Codex memory activation target is invalid at %s: %s",
			"Codex memory activation target が %s で不正です: %s",
			status.TargetPath,
			status.Message,
		)
	default:
		check.Status = doctorStatusFail
		check.Message = localizef("Codex memory activation returned unknown state at %s: %s", "Codex memory activation が %s で不明な state を返しました: %s", status.TargetPath, status.State.String())
	}
	return &check
}

func codexActivationStatusCriteria(ctx context.Context, projectDir string) apptypes.MemoryActivationCriteria {
	criteria := apptypes.MemoryActivationCriteria{
		Target:        apptypes.MemoryBridgeTargetCodex,
		IncludeGlobal: true,
	}
	if workspace := detectActivationStatusWorkspace(ctx, projectDir); workspace != "" {
		resolvedWorkspace, err := domtypes.WorkspaceFrom(workspace)
		if err == nil {
			criteria.Scopes = []domtypes.MemoryScope{domtypes.WorkspaceScopeOf(resolvedWorkspace)}
		}
	}
	return criteria
}

func detectActivationStatusWorkspace(ctx context.Context, projectDir string) string {
	if explicit := resolveExplicitWorkspaceValue(""); explicit != "" {
		return explicit
	}
	if detected, err := detectRepoContextFromDir(ctx, projectDir); err == nil {
		return detected
	}
	return projectDir
}
