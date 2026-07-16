package cli

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/duck8823/traceary/application"
	apptypes "github.com/duck8823/traceary/application/types"
	appusecase "github.com/duck8823/traceary/application/usecase"
	"github.com/duck8823/traceary/domain/types"
	"golang.org/x/xerrors"
)

func (c *RootCLI) inspectClientEventCoverage(ctx context.Context, client, outputPath, projectDir string, threshold float64) doctorCheck {
	checkName := client + "-event-coverage"
	if c.event == nil {
		return doctorCheck{
			Name:    checkName,
			Status:  doctorStatusSkip,
			Message: localizef("event usecase is not configured", "event usecase が設定されていません"),
		}
	}

	workspace := resolveDoctorEventCoverageWorkspace(ctx, projectDir)
	criteria := apptypes.NewEventListCriteriaBuilder(doctorEventCoverageScanLimit).
		Agent(types.Agent(client))
	if workspace.String() != "" {
		criteria.Workspace(workspace)
	}
	events, err := c.event.List(ctx, criteria.Build())
	if err != nil {
		return doctorCheck{
			Name:    checkName,
			Status:  doctorStatusFail,
			Message: localizef("failed to list recent %s events: %v", "recent %s event の取得に失敗しました: %v", client, err),
		}
	}

	inputs := make([]appusecase.EventCoverageInput, 0, len(events))
	for _, event := range events {
		if event == nil {
			continue
		}
		inputs = append(inputs, appusecase.EventCoverageInput{
			SessionID: event.SessionID().String(),
			Kind:      event.Kind(),
		})
	}
	coverage := appusecase.SummarizeSessionEventCoverage(inputs)
	ratio := coverage.PromptTranscriptMissingRatio()
	if coverage.Sessions < doctorEventCoverageMinSample {
		return doctorCheck{
			Name:   checkName,
			Status: doctorStatusPass,
			Message: localizef(
				"scanned %d recent %s event(s); only %d complete session(s) observed (minimum sample %d), so event coverage is not judged yet",
				"%d 件の recent %s event を検査しました。完全に観測できた session は %d 件だけです (minimum sample %d) のため、event coverage はまだ判定しません",
				len(events),
				client,
				coverage.Sessions,
				doctorEventCoverageMinSample,
			),
		}
	}
	if ratio <= threshold {
		return doctorCheck{
			Name:   checkName,
			Status: doctorStatusPass,
			Message: localizef(
				"scanned %d recent %s event(s); prompt/transcript coverage is healthy (sessions=%d prompt_transcript_missing=%d complete=%d enriched=%d with_prompt=%d with_transcript=%d with_command=%d ratio=%.2f threshold=%.2f)",
				"%d 件の recent %s event を検査しました。prompt/transcript coverage は健全です (sessions=%d prompt_transcript_missing=%d complete=%d enriched=%d with_prompt=%d with_transcript=%d with_command=%d ratio=%.2f threshold=%.2f)",
				len(events),
				client,
				coverage.Sessions,
				coverage.PromptTranscriptMissing,
				coverage.Complete,
				coverage.Enriched,
				coverage.WithPrompt,
				coverage.WithTranscript,
				coverage.WithCommand,
				ratio,
				threshold,
			),
		}
	}

	configCoverage, configCoverageKnown, pluginManaged, pluginKey := c.managedCoverageForEventCoverage(outputPath, client)
	if configCoverageKnown {
		if missing := configCoverage.MissingEnrichment(); len(missing) > 0 {
			if pluginManaged {
				updateCommand := claudePluginUpdateCommand(pluginKey)
				return doctorCheck{
					Name:       checkName,
					Status:     doctorStatusWarn,
					FixCommand: updateCommand,
					Hint: localizef(
						"the plugin-managed Claude hook config is missing %s coverage; update the Claude plugin instead of writing project settings hooks",
						"plugin-managed Claude hook config に %s coverage がありません。project settings hook を書き込まず Claude plugin を更新してください",
						strings.Join(missing, ", "),
					),
					Message: localizef(
						"scanned %d recent %s event(s); %.0f%% of complete sessions lack prompt/transcript coverage (sessions=%d prompt_transcript_missing=%d complete=%d enriched=%d, threshold=%.0f%%). The plugin-managed config is missing Traceary-managed %s hooks",
						"%d 件の recent %s event を検査しました。完全に観測できた session の %.0f%% で prompt/transcript coverage が不足しています (sessions=%d prompt_transcript_missing=%d complete=%d enriched=%d, threshold=%.0f%%)。plugin-managed config に Traceary 管理の %s hook がありません",
						len(events),
						client,
						ratio*100,
						coverage.Sessions,
						coverage.PromptTranscriptMissing,
						coverage.Complete,
						coverage.Enriched,
						threshold*100,
						strings.Join(missing, ", "),
					),
				}
			}
			fixCommand := fmt.Sprintf("traceary doctor --client %s --project-dir %s --fix", client, shellQuote(projectDir))
			return doctorCheck{
				Name:       checkName,
				Status:     doctorStatusWarn,
				FixCommand: fixCommand,
				Hint: localizef(
					"the installed hook config is missing %s coverage; run `%s` to refresh Traceary-managed hooks without touching user hooks",
					"インストール済み hook config に %s coverage がありません。`%s` で Traceary 管理 hook を更新できます (user hook は保持)",
					strings.Join(missing, ", "),
					fixCommand,
				),
				Message: localizef(
					"scanned %d recent %s event(s); %.0f%% of complete sessions lack prompt/transcript coverage (sessions=%d prompt_transcript_missing=%d complete=%d enriched=%d, threshold=%.0f%%). The installed config is missing Traceary-managed %s hooks: %s",
					"%d 件の recent %s event を検査しました。完全に観測できた session の %.0f%% で prompt/transcript coverage が不足しています (sessions=%d prompt_transcript_missing=%d complete=%d enriched=%d, threshold=%.0f%%)。インストール済み config に Traceary 管理の %s hook がありません: %s",
					len(events),
					client,
					ratio*100,
					coverage.Sessions,
					coverage.PromptTranscriptMissing,
					coverage.Complete,
					coverage.Enriched,
					threshold*100,
					strings.Join(missing, ", "),
					outputPath,
				),
			}
		}
	}

	hint := Localize(
		fmt.Sprintf("hook config appears to include enrichment coverage; inspect hook timeouts, host hook cancellations, host behavior, and recent event IDs with `traceary list --agent %s`", client),
		fmt.Sprintf("hook config には enrichment coverage が含まれているようです。hook timeout、host 側の hook cancellation、host 側の挙動、recent event ID を `traceary list --agent %s` で確認してください", client),
	)
	if !configCoverageKnown {
		if pluginManaged {
			updateCommand := claudePluginUpdateCommand(pluginKey)
			hint = localizef(
				"Claude hooks appear to be plugin-managed by %q; project settings hooks are intentionally absent. Inspect plugin cache/update state (`%s`), host hook cancellations/timeouts, and recent event IDs with `traceary list --agent %s`",
				"Claude hooks は plugin %q によって管理されているようです。project settings hook がないのは意図された状態です。plugin cache/update 状態 (`%s`)、host 側の hook cancellation/timeout、recent event ID を `traceary list --agent %s` で確認してください",
				pluginKey,
				updateCommand,
				client,
			)
		} else {
			hint = localizef(
				"the project hook config could not be inspected; first fix %s-config, then rerun doctor",
				"project hook config を検査できませんでした。先に %s-config を修復してから doctor を再実行してください",
				client,
			)
		}
	}
	return doctorCheck{
		Name:   checkName,
		Status: doctorStatusWarn,
		Hint:   hint,
		Message: localizef(
			"scanned %d recent %s event(s); %.0f%% of complete sessions lack prompt/transcript coverage (sessions=%d prompt_transcript_missing=%d complete=%d enriched=%d with_prompt=%d with_transcript=%d with_command=%d, threshold=%.0f%%)",
			"%d 件の recent %s event を検査しました。完全に観測できた session の %.0f%% で prompt/transcript coverage が不足しています (sessions=%d prompt_transcript_missing=%d complete=%d enriched=%d with_prompt=%d with_transcript=%d with_command=%d, threshold=%.0f%%)",
			len(events),
			client,
			ratio*100,
			coverage.Sessions,
			coverage.PromptTranscriptMissing,
			coverage.Complete,
			coverage.Enriched,
			coverage.WithPrompt,
			coverage.WithTranscript,
			coverage.WithCommand,
			threshold*100,
		),
	}
}

func resolveDoctorEventCoverageWorkspace(ctx context.Context, projectDir string) types.Workspace {
	if explicit := resolveExplicitWorkspaceValue(""); explicit != "" {
		return types.Workspace(explicit)
	}
	if detected, err := detectRepoContextFromDir(ctx, projectDir); err == nil {
		return types.Workspace(detected)
	}
	return types.Workspace(normalizeLocalWorkContextPath(projectDir))
}

func (c *RootCLI) managedCoverageForConfigFile(path string, client string) (application.HookManagedCoverage, bool) {
	content, err := os.ReadFile(path)
	if err != nil {
		return application.HookManagedCoverage{}, false
	}
	coverage, err := c.hooksInspector.ManagedCoverage(content, client)
	if err != nil {
		return application.HookManagedCoverage{}, false
	}
	return coverage, true
}

func (c *RootCLI) managedCoverageForEventCoverage(outputPath, client string) (application.HookManagedCoverage, bool, bool, string) {
	if client == "claude" {
		detection := c.detectClaudeTracearyPluginForCLI()
		if detection.Active {
			coverage, known := c.managedCoverageForInstalledClaudePlugin(detection.PluginKey)
			return coverage, known, true, detection.PluginKey
		}
	}
	coverage, known := c.managedCoverageForConfigFile(outputPath, client)
	return coverage, known, false, ""
}

func (c *RootCLI) managedCoverageForInstalledClaudePlugin(pluginKey string) (application.HookManagedCoverage, bool) {
	result := c.inspectInstalledClaudePluginHookCoverage(pluginKey)
	return result.coverage, result.known
}

type installedClaudePluginHookCoverage struct {
	coverage  application.HookManagedCoverage
	known     bool
	cachePath string
}

func (c *RootCLI) inspectInstalledClaudePluginHookCoverage(pluginKey string) installedClaudePluginHookCoverage {
	if c.pluginCacheInspector == nil || pluginKey == "" {
		return installedClaudePluginHookCoverage{}
	}
	home, err := userHomeDirFunc()
	if err != nil {
		return installedClaudePluginHookCoverage{}
	}
	status := c.pluginCacheInspector.DetectClaudePluginCacheStatus(home, pluginKey)
	if status.CachePath != "" && status.CachedVersion != "" {
		cacheHooksPath := filepath.Join(status.CachePath, status.CachedVersion, "hooks", "hooks.json")
		if coverage, known := c.managedCoverageForConfigFile(cacheHooksPath, "claude"); known {
			return installedClaudePluginHookCoverage{coverage: coverage, known: true, cachePath: cacheHooksPath}
		}
		return installedClaudePluginHookCoverage{cachePath: cacheHooksPath}
	}
	if status.MarketplacePath != "" {
		pluginRoot := filepath.Dir(filepath.Dir(status.MarketplacePath))
		marketplaceHooksPath := filepath.Join(pluginRoot, "hooks", "hooks.json")
		if coverage, known := c.managedCoverageForConfigFile(marketplaceHooksPath, "claude"); known {
			return installedClaudePluginHookCoverage{coverage: coverage, known: true}
		}
	}
	return installedClaudePluginHookCoverage{}
}

func claudePluginUpdateCommand(pluginKey string) string {
	if pluginKey == "" {
		return "claude plugins update"
	}
	return "claude plugins update " + pluginKey
}

// inspectClaudePluginCacheStatus compares the cached Traceary Claude
// Code plugin version against the marketplace manifest the plugin was
// registered from. A stale cache is the exact failure mode v0.8.0
// dogfooding revealed: `brew upgrade traceary` does not refresh the
// plugin cache, so new hooks (#606 transcript / #605 matcher expansion)
// stay dark until the operator runs `claude plugins update`. The check
// returns nil when the plugin is not active or when either side cannot
// be resolved (reported indirectly by the existing claude-config check).

func (c *RootCLI) attachDoctorConfigFix(check *doctorCheck, client, outputPath, projectDir string) {
	if check == nil || check.Name != client+"-config" || check.Status != doctorStatusWarn {
		return
	}
	if check.AutoFixAvailable || check.FixFunc != nil {
		return
	}
	if client == "claude" {
		detection := c.detectClaudeTracearyPluginForCLI()
		if detection.Active {
			return
		}
	}
	check.AutoFixAvailable = true
	check.FixFunc = func(ctx context.Context, dryRun bool) (string, error) {
		action := fmt.Sprintf("upgrade Traceary-managed %s hooks at %s", client, outputPath)
		if dryRun {
			return "would: " + action, nil
		}
		_, _, err := c.hooksOrchestrator.UpgradeWithMatcher(ctx, client, "traceary", projectDir, types.Some(outputPath), "")
		if err != nil {
			return action, xerrors.Errorf("failed to upgrade Traceary-managed hooks: %w", err)
		}
		return action, nil
	}
}
