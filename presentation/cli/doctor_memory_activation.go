package cli

import (
	"context"
	"fmt"
	"strings"

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
	commands := memoryActivationCommands(criteria, canonicalMemoryActivateCommandPath)
	check := doctorCheck{
		Name:    "codex-memory-activation",
		Status:  doctorStatusPass,
		Message: fmt.Sprintf("%s: %s", status.TargetPath, status.Message),
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

// inspectClaudeMemoryActivationStatus reports the Claude two-file
// activation pair (CLAUDE.md import stub plus
// .traceary/memories/claude.md external memory file) as a doctor check.
// The status state is the aggregated pair state per the v0.13 ADR, and
// remediation surfaces the same dry-run/apply commands the activation
// CLI uses so operators can re-run the failing path verbatim.
func (c *RootCLI) inspectClaudeMemoryActivationStatus(ctx context.Context, projectDir string) *doctorCheck {
	if c.memory == nil {
		return nil
	}
	criteria := claudeActivationStatusCriteria(ctx, projectDir)
	status, err := c.memory.ActivationStatus(ctx, criteria)
	if err != nil {
		return &doctorCheck{
			Name:    "claude-memory-activation",
			Status:  doctorStatusFail,
			Message: localizef("failed to inspect Claude memory activation: %v", "Claude memory activation の確認に失敗しました: %v", err),
		}
	}
	commands := memoryActivationCommands(criteria, canonicalMemoryActivateCommandPath)
	check := doctorCheck{
		Name:    "claude-memory-activation",
		Status:  doctorStatusPass,
		Message: fmt.Sprintf("%s: %s", status.TargetPath, status.Message),
	}
	switch status.State {
	case apptypes.MemoryActivationStatusInSync:
		check.Status = doctorStatusPass
		check.Message = localizef("Claude memory activation is in sync at %s (%d accepted memories)", "Claude memory activation は %s で同期済みです (%d accepted memories)", status.TargetPath, status.ActivatedCount)
	case apptypes.MemoryActivationStatusMissing:
		check.Status = doctorStatusWarn
		check.Hint = "activate accepted memories into the Claude host import stub"
		check.FixCommand = commands.Apply
		check.Message = localizef(
			"Claude memory activation is missing at %s (%d accepted memories). Preview with `%s`, then apply with `%s`",
			"Claude memory activation が %s にありません (%d accepted memories)。`%s` で確認してから `%s` で反映してください",
			status.TargetPath,
			status.ActivatedCount,
			commands.DryRun,
			commands.Apply,
		)
	case apptypes.MemoryActivationStatusStale:
		check.Status = doctorStatusWarn
		check.Hint = "refresh the Claude host import stub and external memory file"
		check.FixCommand = commands.Apply
		check.Message = localizef(
			"Claude memory activation is stale at %s (%d accepted memories). Preview with `%s`, then refresh with `%s`",
			"Claude memory activation は %s で stale です (%d accepted memories)。`%s` で確認してから `%s` で更新してください",
			status.TargetPath,
			status.ActivatedCount,
			commands.DryRun,
			commands.Apply,
		)
	case apptypes.MemoryActivationStatusInvalid:
		check.Status = doctorStatusFail
		check.Hint = "inspect the Claude host context and external memory files before applying Traceary activation"
		check.Message = localizef(
			"Claude memory activation target is invalid at %s: %s",
			"Claude memory activation target が %s で不正です: %s",
			status.TargetPath,
			status.Message,
		)
	default:
		check.Status = doctorStatusFail
		check.Message = localizef("Claude memory activation returned unknown state at %s: %s", "Claude memory activation が %s で不明な state を返しました: %s", status.TargetPath, status.State.String())
	}
	return &check
}

// inspectGeminiMemoryActivationStatus reports the Gemini two-file
// activation pair (GEMINI.md import stub plus
// .traceary/memories/gemini.md external memory file) as a doctor check.
// The check mirrors the Claude variant: aggregated pair state per the
// v0.13 ADR, dry-run/apply remediation derived from the same activation
// criteria, and the explicit promise that Gemini's `## Gemini Added
// Memories` section is never managed or rewritten by Traceary.
func (c *RootCLI) inspectGeminiMemoryActivationStatus(ctx context.Context, projectDir string) *doctorCheck {
	if c.memory == nil {
		return nil
	}
	criteria := geminiActivationStatusCriteria(ctx, projectDir)
	status, err := c.memory.ActivationStatus(ctx, criteria)
	if err != nil {
		return &doctorCheck{
			Name:    "gemini-memory-activation",
			Status:  doctorStatusFail,
			Message: localizef("failed to inspect Gemini memory activation: %v", "Gemini memory activation の確認に失敗しました: %v", err),
		}
	}
	commands := memoryActivationCommands(criteria, canonicalMemoryActivateCommandPath)
	check := doctorCheck{
		Name:    "gemini-memory-activation",
		Status:  doctorStatusPass,
		Message: fmt.Sprintf("%s: %s", status.TargetPath, status.Message),
	}
	switch status.State {
	case apptypes.MemoryActivationStatusInSync:
		check.Status = doctorStatusPass
		check.Message = localizef("Gemini memory activation is in sync at %s (%d accepted memories)", "Gemini memory activation は %s で同期済みです (%d accepted memories)", status.TargetPath, status.ActivatedCount)
	case apptypes.MemoryActivationStatusMissing:
		check.Status = doctorStatusWarn
		check.Hint = "activate accepted memories into the Gemini host import stub"
		check.FixCommand = commands.Apply
		check.Message = localizef(
			"Gemini memory activation is missing at %s (%d accepted memories). Preview with `%s`, then apply with `%s`",
			"Gemini memory activation が %s にありません (%d accepted memories)。`%s` で確認してから `%s` で反映してください",
			status.TargetPath,
			status.ActivatedCount,
			commands.DryRun,
			commands.Apply,
		)
	case apptypes.MemoryActivationStatusStale:
		check.Status = doctorStatusWarn
		check.Hint = "refresh the Gemini host import stub and external memory file"
		check.FixCommand = commands.Apply
		check.Message = localizef(
			"Gemini memory activation is stale at %s (%d accepted memories). Preview with `%s`, then refresh with `%s`",
			"Gemini memory activation は %s で stale です (%d accepted memories)。`%s` で確認してから `%s` で更新してください",
			status.TargetPath,
			status.ActivatedCount,
			commands.DryRun,
			commands.Apply,
		)
	case apptypes.MemoryActivationStatusInvalid:
		check.Status = doctorStatusFail
		check.Hint = "inspect the Gemini host context and external memory files before applying Traceary activation"
		check.Message = localizef(
			"Gemini memory activation target is invalid at %s: %s",
			"Gemini memory activation target が %s で不正です: %s",
			status.TargetPath,
			status.Message,
		)
	default:
		check.Status = doctorStatusFail
		check.Message = localizef("Gemini memory activation returned unknown state at %s: %s", "Gemini memory activation が %s で不明な state を返しました: %s", status.TargetPath, status.State.String())
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

// claudeActivationStatusCriteria pins the doctor check to the operator's
// project directory so the status walk does not silently re-detect the
// activation root from the working directory of the doctor process.
// `--project-dir` is the doctor flag operators use to scope a check to a
// specific repository, so the criteria sets `Root` directly when the
// caller passed a non-empty `projectDir`. When the project directory is
// empty (auto-detected), the activation usecase falls back to its
// documented `.git`-ancestor walk.
func claudeActivationStatusCriteria(ctx context.Context, projectDir string) apptypes.MemoryActivationCriteria {
	criteria := apptypes.MemoryActivationCriteria{
		Target:        apptypes.MemoryBridgeTargetClaude,
		IncludeGlobal: true,
	}
	if root := strings.TrimSpace(projectDir); root != "" {
		criteria.Root = root
	}
	if workspace := detectActivationStatusWorkspace(ctx, projectDir); workspace != "" {
		resolvedWorkspace, err := domtypes.WorkspaceFrom(workspace)
		if err == nil {
			criteria.Scopes = []domtypes.MemoryScope{domtypes.WorkspaceScopeOf(resolvedWorkspace)}
		}
	}
	return criteria
}

// geminiActivationStatusCriteria mirrors claudeActivationStatusCriteria
// for the Gemini target. Pinning `Root` to `--project-dir` when set
// keeps the doctor check scoped to the same activation root the
// operator would see from `traceary memory activate --target gemini
// --status` inside that repository, instead of the doctor process's
// working directory.
func geminiActivationStatusCriteria(ctx context.Context, projectDir string) apptypes.MemoryActivationCriteria {
	criteria := apptypes.MemoryActivationCriteria{
		Target:        apptypes.MemoryBridgeTargetGemini,
		IncludeGlobal: true,
	}
	if root := strings.TrimSpace(projectDir); root != "" {
		criteria.Root = root
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
